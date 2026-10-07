package main

// The App's assistant is compa-kernel, Compa's agent runtime, in its own
// process: midden-ui starts `compa-kernel gateway -E` with the App's kernel
// folder as its home, the App's workspace as its workspace and the installed
// skills as its built-in skills. midden-ui chats with it over its web socket
// and hears its turns, tool calls and approval asks through a process hook,
// `midden-ui kernel-hook`, which relays them to the loopback address and
// secret named in the hook's own environment.

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	envKernelRelay         = "MIDDEN_KERNEL_RELAY"
	envKernelRelaySecret   = "MIDDEN_KERNEL_RELAY_SECRET"
	kernelStartTimeout     = 30 * time.Second
	kernelStopTimeout      = 10 * time.Second
	defaultApprovalTimeout = 60 * time.Second
	kernelCrashLimit       = 3
	kernelCrashWindow      = 5 * time.Minute
)

// kernelObservedEvents are the runtime events the hook relays.
var kernelObservedEvents = []string{"agent.turn.end", "agent.tool.exec_start", "agent.tool.exec_end", "agent.tool.exec_skipped"}

// kernelSetup is where the kernel runs and what it is given.
type kernelSetup struct {
	Executable string   // compa-kernel
	Home       string   // its home, COMPA_HOME
	Workspace  string   // its workspace
	Skills     string   // the installed skills, read-only to the agent
	Tools      string   // the App's own programs, such as Pandoc; may be ""
	Install    string   // the programs folder, first on the agent's PATH
	Hook       []string // the process hook's command
	Log        string   // where the kernel's output goes
}

// kernelEvent is one runtime event of a web chat turn.
type kernelEvent struct {
	Kind, ChatID, MessageID string
	Payload                 map[string]any
}

// kernelApprovalRequest is a tool call the kernel's approval policy asks about.
type kernelApprovalRequest struct {
	ChatID, MessageID, Tool string
	Arguments               map[string]any
}

type kernelApprovalAnswer struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

// kernelObserver hears what the hook relays.
type kernelObserver interface {
	kernelEvent(kernelEvent)
	kernelApproval(context.Context, kernelApprovalRequest) kernelApprovalAnswer
}

// kernelProcess runs one compa-kernel gateway and restarts it when it stops on
// its own, unless it keeps stopping.
type kernelProcess struct {
	setup    kernelSetup
	observer kernelObserver

	relay       net.Listener
	relaySecret string
	relayServer *http.Server
	job         processJob
	closeOnce   sync.Once

	mu              sync.Mutex
	state           string // stopped, starting, ready or failed
	failure         error
	ready           chan struct{} // closed when the current start succeeds or fails
	cmd             *exec.Cmd
	exited          chan struct{} // closed when the current process exits
	port            int
	webToken        string
	pidToken        string
	approvalTimeout time.Duration
	stopping        bool
	crashes         []time.Time
}

func newKernelProcess(setup kernelSetup) (*kernelProcess, error) {
	relay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open the kernel relay: %w", err)
	}
	closed := make(chan struct{})
	close(closed)
	k := &kernelProcess{setup: setup, relay: relay, relaySecret: randomID(), state: "stopped", ready: closed, exited: closed,
		approvalTimeout: defaultApprovalTimeout, job: newProcessJob()}
	k.relayServer = &http.Server{Handler: http.HandlerFunc(k.serveRelay), ReadHeaderTimeout: 10 * time.Second}
	go k.relayServer.Serve(relay)
	return k, nil
}

// Start starts the kernel in the background, unless it runs or is starting.
func (k *kernelProcess) Start() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state == "starting" || k.state == "ready" {
		return
	}
	k.state, k.failure, k.stopping = "starting", nil, false
	ready := make(chan struct{})
	k.ready = ready
	go func() {
		err := k.launch()
		k.mu.Lock()
		defer k.mu.Unlock()
		if k.stopping {
			err = errors.New("the assistant was stopped")
		}
		if err != nil {
			k.state, k.failure = "failed", err
		} else {
			k.state = "ready"
		}
		close(ready)
	}()
}

// launch prepares the kernel's settings, starts it and waits until it is
// ready.
func (k *kernelProcess) launch() error {
	k.stopStale()
	timeout, err := prepareKernelSettings(k.setup, k.relayURL(), k.relaySecret)
	if err != nil {
		return err
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return err
	}
	webToken := randomID()
	log, err := os.Create(k.setup.Log)
	if err != nil {
		return fmt.Errorf("open the kernel log: %w", err)
	}
	cmd := exec.Command(k.setup.Executable, "gateway", "-E")
	cmd.Dir = k.setup.Workspace
	cmd.Env = kernelEnv(os.Environ(), k.setup, port, webToken)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		return fmt.Errorf("start compa-kernel: %w", err)
	}
	k.job.add(cmd.Process)
	exited := make(chan struct{})
	k.mu.Lock()
	k.cmd, k.exited, k.port, k.webToken, k.pidToken, k.approvalTimeout = cmd, exited, port, webToken, "", timeout
	k.mu.Unlock()
	go func() {
		err := cmd.Wait()
		log.Close()
		close(exited)
		k.exitedOnItsOwn(cmd, err)
	}()
	pidToken, err := k.waitStarted(cmd, port, exited)
	if err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	k.mu.Lock()
	k.pidToken = pidToken
	k.mu.Unlock()
	return nil
}

// waitStarted waits until the kernel has written its pid file and answers
// ready, and returns the token of its control endpoints.
func (k *kernelProcess) waitStarted(cmd *exec.Cmd, port int, exited chan struct{}) (string, error) {
	deadline := time.Now().Add(kernelStartTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return "", fmt.Errorf("compa-kernel stopped while starting: %s", logTail(k.setup.Log))
		case <-time.After(150 * time.Millisecond):
		}
		pid, err := readKernelPid(k.setup.Home)
		if err != nil || pid.PID != cmd.Process.Pid {
			continue
		}
		if response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/ready", port)); err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return pid.Token, nil
			}
		}
	}
	return "", fmt.Errorf("compa-kernel did not become ready in %s: %s", kernelStartTimeout, logTail(k.setup.Log))
}

// exitedOnItsOwn records a kernel that stopped without being asked, and
// restarts it unless it keeps stopping.
func (k *kernelProcess) exitedOnItsOwn(cmd *exec.Cmd, err error) {
	k.mu.Lock()
	if k.cmd != cmd || k.stopping {
		k.mu.Unlock()
		return
	}
	k.cmd = nil
	now := time.Now()
	recent := []time.Time{now}
	for _, at := range k.crashes {
		if now.Sub(at) < kernelCrashWindow {
			recent = append(recent, at)
		}
	}
	k.crashes = recent
	restart := len(recent) < kernelCrashLimit
	if k.state == "ready" {
		k.state, k.failure = "failed", fmt.Errorf("compa-kernel stopped unexpectedly (%v): %s", err, logTail(k.setup.Log))
	}
	k.mu.Unlock()
	if restart {
		time.AfterFunc(2*time.Second, func() {
			k.mu.Lock()
			stopping := k.stopping
			k.mu.Unlock()
			if !stopping {
				k.Start()
			}
		})
	}
}

// stopStale asks a kernel an earlier midden-ui left running in this home to
// stop, so the new one can start.
func (k *kernelProcess) stopStale() {
	pid, err := readKernelPid(k.setup.Home)
	if err != nil || pid.Token == "" || pid.Port == 0 {
		return
	}
	host := pid.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	base := "http://" + net.JoinHostPort(host, fmt.Sprint(pid.Port))
	if postKernel(context.Background(), base+"/shutdown", pid.Token, 2*time.Second) != nil {
		return
	}
	deadline := time.Now().Add(kernelStopTimeout)
	for time.Now().Before(deadline) {
		if _, err := readKernelPid(k.setup.Home); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitReady waits for the current start; an error says why the kernel does
// not run.
func (k *kernelProcess) waitReady(ctx context.Context) error {
	k.mu.Lock()
	ready := k.ready
	k.mu.Unlock()
	select {
	case <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state != "ready" {
		if k.failure != nil {
			return k.failure
		}
		return errors.New("compa-kernel is not running")
	}
	return nil
}

// endpoint is where the running kernel's web chat listens, its token, and a
// channel closed when that process exits.
func (k *kernelProcess) endpoint() (int, string, <-chan struct{}) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.port, k.webToken, k.exited
}

func (k *kernelProcess) approvalWait() time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.approvalTimeout
}

// status is the kernel's state and, when it does not run, why.
func (k *kernelProcess) status() (string, string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failure != nil {
		return k.state, k.failure.Error()
	}
	return k.state, ""
}

// reload makes the kernel read its settings again. A kernel that failed is
// started again, since new settings may be what it lacked.
func (k *kernelProcess) reload(ctx context.Context) error {
	k.mu.Lock()
	state := k.state
	k.mu.Unlock()
	switch state {
	case "stopped":
		return nil
	case "failed":
		k.Start()
	}
	waitCtx, cancel := context.WithTimeout(ctx, kernelStartTimeout)
	defer cancel()
	if err := k.waitReady(waitCtx); err != nil || state == "failed" {
		return err
	}
	k.mu.Lock()
	port, token := k.port, k.pidToken
	k.mu.Unlock()
	return postKernel(ctx, fmt.Sprintf("http://127.0.0.1:%d/reload", port), token, 2*time.Minute)
}

// Stop asks the kernel to stop, and ends it if it does not.
func (k *kernelProcess) Stop() {
	k.mu.Lock()
	k.stopping = true
	cmd, exited, port, token := k.cmd, k.exited, k.port, k.pidToken
	k.cmd, k.state = nil, "stopped"
	k.mu.Unlock()
	if cmd == nil {
		return
	}
	if token != "" {
		_ = postKernel(context.Background(), fmt.Sprintf("http://127.0.0.1:%d/shutdown", port), token, 2*time.Second)
	}
	select {
	case <-exited:
	case <-time.After(kernelStopTimeout):
		_ = cmd.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	}
}

// Close stops the kernel and its relay.
func (k *kernelProcess) Close() {
	k.closeOnce.Do(func() {
		k.Stop()
		_ = k.relayServer.Close()
		k.job.close()
	})
}

func (k *kernelProcess) relayURL() string { return "http://" + k.relay.Addr().String() + "/relay" }

// serveRelay takes what the hook relays: runtime events, answered at once,
// and approval asks, answered with the person's decision.
func (k *kernelProcess) serveRelay(w http.ResponseWriter, r *http.Request) {
	given, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Method != http.MethodPost || r.URL.Path != "/relay" || subtle.ConstantTimeCompare([]byte(given), []byte(k.relaySecret)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
	var message struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err != nil || json.Unmarshal(body, &message) != nil {
		http.Error(w, "unreadable message", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	observer := k.observer
	switch message.Method {
	case "hook.runtime_event":
		var event struct {
			Kind  string `json:"kind"`
			Scope struct {
				ChatID    string `json:"chat_id"`
				MessageID string `json:"message_id"`
			} `json:"scope"`
			Payload map[string]any `json:"payload"`
		}
		if json.Unmarshal(message.Params, &event) == nil && observer != nil {
			observer.kernelEvent(kernelEvent{Kind: event.Kind, ChatID: event.Scope.ChatID, MessageID: event.Scope.MessageID, Payload: event.Payload})
		}
		io.WriteString(w, "{}")
	case "hook.approve_tool":
		var request struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
			Context   struct {
				Inbound struct {
					ChatID    string `json:"chat_id"`
					MessageID string `json:"message_id"`
				} `json:"inbound"`
			} `json:"context"`
		}
		answer := kernelApprovalAnswer{Reason: "Midden could not read the request"}
		if json.Unmarshal(message.Params, &request) == nil && observer != nil {
			answer = observer.kernelApproval(r.Context(), kernelApprovalRequest{ChatID: request.Context.Inbound.ChatID,
				MessageID: request.Context.Inbound.MessageID, Tool: request.Tool, Arguments: request.Arguments})
		}
		_ = json.NewEncoder(w).Encode(answer)
	default:
		io.WriteString(w, "{}")
	}
}

// kernelPid is the kernel's pid file: its process, and the token and
// address of its control endpoints.
type kernelPid struct {
	PID   int    `json:"pid"`
	Token string `json:"token"`
	Port  int    `json:"port"`
	Host  string `json:"host"`
}

func readKernelPid(home string) (kernelPid, error) {
	var pid kernelPid
	data, err := os.ReadFile(filepath.Join(home, ".compa.pid"))
	if err == nil {
		err = json.Unmarshal(data, &pid)
	}
	return pid, err
}

// postKernel calls one of the kernel's control endpoints.
func postKernel(ctx context.Context, address, token string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 300 {
		return nil
	}
	var failure struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&failure)
	if failure.Error == "" {
		failure.Error = response.Status
	}
	return errors.New(failure.Error)
}

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// logTail is the end of the kernel's output, for a failure message.
func logTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "no output"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	tail := strings.TrimSpace(strings.Join(lines, " | "))
	if tail == "" {
		return "no output"
	}
	return clip(tail, 600)
}

// prepareKernelSettings writes the settings Midden owns into the kernel's
// config.json: the web chat channel and the hook entry. Everything else stays
// as the person or Compa left it. It returns how long the kernel waits for an
// approval.
func prepareKernelSettings(setup kernelSetup, relayURL, relaySecret string) (time.Duration, error) {
	for attempt := 0; ; attempt++ {
		cfg, err := loadKernelConfig(setup.Home)
		if err != nil {
			return 0, err
		}
		timeout, err := applyKernelSettings(cfg, setup, relayURL, relaySecret)
		if err != nil {
			return 0, err
		}
		if err = saveKernelConfig(setup.Home, cfg); err == nil {
			return timeout, nil
		}
		if !errors.Is(err, errSettingsChanged) || attempt > 2 {
			return 0, fmt.Errorf("write the kernel settings: %w", err)
		}
	}
}

func applyKernelSettings(cfg *kernelConfig, setup kernelSetup, relayURL, relaySecret string) (time.Duration, error) {
	channels, err := cfg.rawObject("channel_list")
	if err != nil {
		return 0, err
	}
	// An entry replaces Compa's default for the channel whole, so it says
	// everything.
	channels["web"], _ = json.Marshal(map[string]any{"enabled": true, "type": "web", "dm_policy": "open", "group_policy": "open",
		"settings": map[string]any{"ping_interval": 30, "read_timeout": 60, "write_timeout": 10, "max_connections": 100,
			"streaming": map[string]any{"enabled": true}}})
	if err := cfg.setRawObject("channel_list", channels); err != nil {
		return 0, err
	}
	hooks, err := cfg.rawObject("hooks")
	if err != nil {
		return 0, err
	}
	processes := map[string]json.RawMessage{}
	if raw, ok := hooks["processes"]; ok && string(bytes.TrimSpace(raw)) != "null" {
		if err := decodeJSON(raw, &processes); err != nil || processes == nil {
			return 0, errors.New("config.json hooks.processes is not an object")
		}
	}
	processes["midden"], _ = json.Marshal(map[string]any{"enabled": true, "command": setup.Hook,
		"env":     map[string]string{envKernelRelay: relayURL, envKernelRelaySecret: relaySecret},
		"observe": kernelObservedEvents, "intercept": []string{"approve_tool"}})
	hooks["processes"], _ = json.Marshal(processes)
	hooks["enabled"] = json.RawMessage("true")
	timeout := defaultApprovalTimeout
	if raw, ok := hooks["defaults"]; ok {
		var defaults struct {
			ApprovalTimeoutMS int `json:"approval_timeout_ms"`
		}
		if json.Unmarshal(raw, &defaults) == nil && defaults.ApprovalTimeoutMS > 0 {
			timeout = time.Duration(defaults.ApprovalTimeoutMS) * time.Millisecond
		}
	}
	return timeout, cfg.setRawObject("hooks", hooks)
}

// kernelEnv is the kernel's environment: the person's, without settings for
// another Compa, with this App's own and its programs first on PATH.
func kernelEnv(base []string, setup kernelSetup, port int, webToken string) []string {
	env, path := kernelBaseEnv(base, setup.Home)
	paths := []string{setup.Install}
	if setup.Tools != "" {
		paths = append(paths, setup.Tools)
	}
	if path != "" {
		paths = append(paths, path)
	}
	return append(env,
		"PATH="+strings.Join(paths, string(os.PathListSeparator)),
		"COMPA_AGENTS_DEFAULTS_WORKSPACE="+setup.Workspace,
		"COMPA_GATEWAY_HOST=127.0.0.1",
		fmt.Sprintf("COMPA_GATEWAY_PORT=%d", port),
		"COMPA_BUILTIN_SKILLS="+setup.Skills,
		"COMPA_TOOLS_ALLOW_READ_PATHS="+readOnlyPattern(setup.Skills),
		"COMPA_CHANNELS_WEB_TOKEN="+webToken,
	)
}

// kernelCommandEnv is the environment of a compa-kernel command run on the
// App's kernel home.
func kernelCommandEnv(home string) []string {
	env, path := kernelBaseEnv(os.Environ(), home)
	if path != "" {
		env = append(env, "PATH="+path)
	}
	return env
}

// kernelBaseEnv drops every COMPA_ setting and PATH from base, returns PATH
// apart, and names home.
func kernelBaseEnv(base []string, home string) ([]string, string) {
	env, path := []string{}, ""
	for _, entry := range base {
		key, value, _ := strings.Cut(entry, "=")
		switch {
		case strings.HasPrefix(strings.ToUpper(key), "COMPA_"):
		case strings.EqualFold(key, "PATH"):
			path = value
		default:
			env = append(env, entry)
		}
	}
	return append(env, "COMPA_HOME="+home), path
}

// readOnlyPattern matches dir and everything in it, for the kernel's
// comma-separated read allowlist.
func readOnlyPattern(dir string) string {
	pattern := "^" + regexp.QuoteMeta(filepath.Clean(dir)) + `(?:[\\/]|$)`
	if runtime.GOOS == "windows" {
		pattern = "(?i)" + pattern
	}
	return strings.ReplaceAll(pattern, ",", `\x2c`)
}

// probeKernelVersion is the release a compa-kernel reports, such as "3.0.0".
func probeKernelVersion(executable, home string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "version")
	command.Env = kernelCommandEnv(home)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("compa-kernel version: %w", err)
	}
	fields := strings.Fields(string(output))
	for i, field := range fields {
		if field == "compa-kernel" && i+1 < len(fields) {
			return strings.TrimPrefix(fields[i+1], "v"), nil
		}
	}
	return "", fmt.Errorf("compa-kernel version printed no version: %q", clip(string(output), 120))
}
