package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var errHistoryLimit = errors.New("conversation history limit reached; remove conversations before adding more history")

// sandboxNotice says what the assistant's commands can reach.
const sandboxNotice = "The assistant runs commands with your account; this is not an OS sandbox."

type Options struct {
	Data          string // the App's data root
	Core          string // the midden executable
	CoreVersion   string
	Skills        string // the installed skills
	Kernel        string // compa-kernel; empty when it is not installed
	KernelVersion string
	Install       string            // the programs folder
	Tools         string            // the App's own programs, such as Pandoc; empty when absent
	SourceEnv     map[string]string // the session record roots Core reads, as set when the App started
	CoreEnv       map[string]string // more settings for Core, such as a separate state in tests
	Frontend      fs.FS
}
type Message struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}
type Session struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Updated    time.Time     `json:"updated"`
	Messages   []Message     `json:"messages"`
	Outcomes   []TurnOutcome `json:"outcomes,omitempty"`
	ActiveTurn string        `json:"activeTurn,omitempty"`
}
type TurnOutcome struct {
	TurnID       string    `json:"turnId"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	At           time.Time `json:"at"`
	MessageIndex int       `json:"messageIndex"`
}
type Event struct {
	Seq          uint64 `json:"seq"`
	Type         string `json:"type"`
	SessionID    string `json:"sessionId,omitempty"`
	TurnID       string `json:"turnId,omitempty"`
	Text         string `json:"text,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Arguments    any    `json:"arguments,omitempty"`
	PermissionID string `json:"permissionId,omitempty"`
	Allow        *bool  `json:"allow,omitempty"`
	Status       string `json:"status,omitempty"`
	Error        string `json:"error,omitempty"`

	CallID          string `json:"callId,omitempty"`
	Effect          string `json:"effect,omitempty"`
	Result          string `json:"result,omitempty"`
	ResultTruncated bool   `json:"resultTruncated,omitempty"`
}
type permission struct {
	ID, Tool  string
	Arguments any
	decision  chan bool
}
type activeTurn struct {
	ID, SessionID string
	cancel        context.CancelFunc
}

// engine runs one turn of a conversation and returns the assistant's reply.
type engine interface {
	Process(ctx context.Context, sessionID, turnID, text string) (string, error)
}

type App struct {
	opts        Options
	paths       appPaths
	notice      string
	csrf, key   string
	mu          sync.Mutex
	modelMu     sync.Mutex // serializes model configuration changes; never held with mu across I/O
	sessions    map[string]*Session
	active      *activeTurn
	permissions map[string]*permission
	events      []Event
	seq         uint64
	watchers    map[chan Event]bool
	runtime     engine
	kernel      *kernelProcess
	skills      []skillInfo
	wg          sync.WaitGroup

	// modelChanging is set under mu while a model change runs; no turn starts meanwhile.
	modelChanging bool
}

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("operating system randomness unavailable")
	}
	return hex.EncodeToString(value[:])
}

func NewApp(opts Options) (*App, error) {
	if !filepath.IsAbs(opts.Data) {
		return nil, fmt.Errorf("the data folder must be an absolute path")
	}
	data, err := resolvedDestination(opts.Data)
	if err != nil {
		return nil, err
	}
	opts.Data = data
	if err := protectSourceStores(opts); err != nil {
		return nil, err
	}
	paths, notice, err := prepareData(opts.Data)
	if err != nil {
		return nil, err
	}
	if paths.Files, err = filepath.EvalSymlinks(paths.Files); err != nil {
		return nil, err
	}
	if err := writeAgentInstructions(paths.Workspace); err != nil {
		return nil, fmt.Errorf("write the assistant's instructions: %w", err)
	}
	app := &App{opts: opts, paths: paths, notice: notice, csrf: randomID(), key: randomID(), sessions: map[string]*Session{},
		permissions: map[string]*permission{}, watchers: map[chan Event]bool{}, skills: listSkills(opts.Skills)}
	raw, err := readBounded(filepath.Join(paths.App, "sessions.json"), 8<<20)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &app.sessions); err != nil {
			return nil, fmt.Errorf("read sessions.json: %w", err)
		}
	}
	if app.sessions == nil {
		app.sessions = map[string]*Session{}
	}
	recovered := false
	for _, s := range app.sessions {
		if s == nil {
			return nil, fmt.Errorf("conversation history contains a null session")
		}
		for i := range s.Outcomes {
			if s.Outcomes[i].Status == "running" {
				s.Outcomes[i].Status = "interrupted"
				s.Outcomes[i].Error = "Midden stopped before this turn finished. Files already written were not undone; review them before retrying."
				s.Outcomes[i].At = time.Now().UTC()
				recovered = true
			}
		}
	}
	if recovered {
		if err := app.saveSessionsLocked(); err != nil {
			return nil, fmt.Errorf("save interrupted turn outcomes: %w", err)
		}
	}
	return app, nil
}

// attachKernel runs the App's turns on kernel.
func (a *App) attachKernel(kernel *kernelProcess) {
	chat := newKernelChat(a, kernel)
	kernel.observer = chat
	a.mu.Lock()
	a.kernel, a.runtime = kernel, chat
	a.mu.Unlock()
}

// kernelSetup is how this App runs its kernel.
func (a *App) kernelSetup(self string) kernelSetup {
	return kernelSetup{Executable: a.opts.Kernel, Home: a.paths.Kernel, Workspace: a.paths.Workspace, Skills: a.opts.Skills,
		Tools: a.opts.Tools, Install: a.opts.Install, Hook: []string{self, "kernel-hook"}, Log: filepath.Join(a.paths.App, "kernel.log")}
}

func (a *App) Close() {
	a.mu.Lock()
	if a.active != nil {
		a.active.cancel()
	}
	kernel := a.kernel
	a.mu.Unlock()
	a.wg.Wait()
	if kernel != nil {
		kernel.Close()
	}
}

func (a *App) Status() map[string]any {
	model := a.storedModelStatus()
	kernel := map[string]string{"state": "missing", "error": "compa-kernel is not installed with this App"}
	if a.kernel != nil {
		state, failure := a.kernel.status()
		kernel = map[string]string{"state": state, "error": failure}
	}
	notices := []string{sandboxNotice}
	if a.notice != "" {
		notices = append(notices, a.notice)
	}
	if kernel["error"] != "" {
		notices = append(notices, "The assistant is not running: "+kernel["error"])
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	active := ""
	if a.active != nil {
		active = a.active.ID
	}
	identity := sha256.Sum256([]byte(a.paths.Files))
	return map[string]any{"workspace": a.paths.Files, "workspaceId": hex.EncodeToString(identity[:]), "uiVersion": version,
		"coreVersion": a.opts.CoreVersion, "kernelName": "Compa", "kernelVersion": a.opts.KernelVersion, "kernel": kernel,
		"skills": a.skills, "model": model, "activeTurn": active, "csrfToken": a.csrf, "notice": strings.Join(notices, " ")}
}
func (a *App) NewSession(title string) (Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) >= 200 {
		return Session{}, fmt.Errorf("conversation limit reached")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New conversation"
	}
	if len([]rune(title)) > 120 {
		title = string([]rune(title)[:120])
	}
	s := Session{ID: randomID(), Title: title, Updated: time.Now().UTC(), Messages: []Message{}}
	a.sessions[s.ID] = &s
	if err := a.saveSessionsLocked(); err != nil {
		delete(a.sessions, s.ID)
		return Session{}, err
	}
	return s, nil
}
func (a *App) Sessions() []Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []Session{}
	for _, s := range a.sessions {
		copy := *s
		copy.Messages = nil
		copy.Outcomes = nil
		out = append(out, copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}
func (a *App) Session(id string) (Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	if !ok {
		return Session{}, os.ErrNotExist
	}
	copy := *s
	copy.Messages = append([]Message{}, s.Messages...)
	copy.Outcomes = append([]TurnOutcome{}, s.Outcomes...)
	if a.active != nil && a.active.SessionID == id {
		copy.ActiveTurn = a.active.ID
	}
	return copy, nil
}

// saveSessionsLocked writes the App's own transcript of its conversations;
// the kernel keeps its own history for the model.
func (a *App) saveSessionsLocked() error {
	raw, err := json.MarshalIndent(a.sessions, "", "  ")
	if err != nil {
		return err
	}
	remaining := (8 << 20) - len(raw) - 1
	for _, session := range a.sessions {
		for _, outcome := range session.Outcomes {
			if outcome.Status == "running" {
				// A 2,000-character terminal error can exceed 12 KiB after JSON escaping.
				remaining -= 16 << 10
				if remaining < 0 {
					return errHistoryLimit
				}
			}
		}
	}
	if remaining < 0 {
		return errHistoryLimit
	}
	return writeJSON(filepath.Join(a.paths.App, "sessions.json"), a.sessions)
}

func (a *App) StartTurn(id, text string) (string, error) {
	if strings.TrimSpace(text) == "" || len(text) > 64000 {
		return "", fmt.Errorf("message must be nonempty and at most 64000 bytes")
	}
	a.mu.Lock()
	s, ok := a.sessions[id]
	if !ok {
		a.mu.Unlock()
		return "", os.ErrNotExist
	}
	if a.active != nil {
		a.mu.Unlock()
		return "", fmt.Errorf("a turn is already running; stop it or wait")
	}
	if a.modelChanging {
		a.mu.Unlock()
		return "", fmt.Errorf("model settings are being changed or checked; try again when that is done")
	}
	if a.runtime == nil {
		a.mu.Unlock()
		return "", errors.New("the assistant is not available: compa-kernel is not installed with this App")
	}
	if !a.storedModelStatus().Configured {
		a.mu.Unlock()
		return "", errors.New(chooseModelMessage)
	}
	ctx, cancel := context.WithCancel(context.Background())
	turn := &activeTurn{ID: randomID(), SessionID: id, cancel: cancel}
	a.active = turn
	oldTitle, oldUpdated := s.Title, s.Updated
	outcomeIndex := len(s.Outcomes)
	s.Outcomes = append(s.Outcomes, TurnOutcome{TurnID: turn.ID, Status: "running", At: time.Now().UTC(), MessageIndex: len(s.Messages)})
	s.Messages = append(s.Messages, Message{Role: "user", Content: text, At: time.Now().UTC()})
	s.Updated = time.Now().UTC()
	if s.Title == "New conversation" {
		s.Title = clip(text, 80)
	}
	if err := a.saveSessionsLocked(); err != nil {
		a.active = nil
		cancel()
		s.Messages = s.Messages[:len(s.Messages)-1]
		s.Outcomes = s.Outcomes[:outcomeIndex]
		s.Title, s.Updated = oldTitle, oldUpdated
		a.mu.Unlock()
		return "", err
	}
	runtime := a.runtime
	a.wg.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.wg.Done()
		defer cancel()
		result, err := runtime.Process(ctx, id, turn.ID, text)
		if err != nil && !errors.Is(err, context.Canceled) {
			err = errors.New(a.withoutSecrets(err.Error()))
		}
		a.mu.Lock()
		outcome := &s.Outcomes[outcomeIndex]
		outcome.At = time.Now().UTC()
		s.Updated = outcome.At
		if err == nil {
			s.Messages = append(s.Messages, Message{Role: "assistant", Content: result, At: time.Now().UTC()})
			outcome.Status = "completed"
			err = a.saveSessionsLocked()
			if err != nil {
				s.Messages = s.Messages[:len(s.Messages)-1]
			}
		}
		if err != nil {
			outcome.Status = "failed"
			outcome.Error = clip(err.Error(), 2000)
			if errors.Is(err, context.Canceled) {
				outcome.Status = "cancelled"
				outcome.Error = "Turn cancelled. Files already written were not undone."
			}
			if saveErr := a.saveSessionsLocked(); saveErr != nil {
				err = errors.Join(err, fmt.Errorf("could not persist turn outcome: %w", saveErr))
				outcome.Error = clip(err.Error(), 2000)
			}
		}
		failure := outcome.Error
		a.active = nil
		a.mu.Unlock()
		if err != nil {
			a.emit(Event{Type: "error", SessionID: id, TurnID: turn.ID, Error: failure})
		} else {
			a.emit(Event{Type: "message", SessionID: id, TurnID: turn.ID, Text: result})
		}
		a.emit(Event{Type: "files_changed", SessionID: id, TurnID: turn.ID})
		a.emit(Event{Type: "turn_done", SessionID: id, TurnID: turn.ID})
	}()
	return turn.ID, nil
}
func (a *App) Cancel(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil || a.active.ID != id {
		return fmt.Errorf("turn is not active")
	}
	a.active.cancel()
	return nil
}
func (a *App) emit(event Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	event.Seq = a.seq
	if a.active != nil {
		if event.SessionID == "" {
			event.SessionID = a.active.SessionID
		}
		if event.TurnID == "" && event.SessionID == a.active.SessionID {
			event.TurnID = a.active.ID
		}
	}
	a.events = append(a.events, event)
	if len(a.events) > 256 {
		a.events = a.events[len(a.events)-256:]
	}
	for ch := range a.watchers {
		select {
		case ch <- event:
		default:
			close(ch)
			delete(a.watchers, ch)
		}
	}
}

// askPermission shows the person a permission card for a tool call the
// kernel's approval policy asks about, and returns their answer. The kernel
// stops waiting after timeout, and the card stops waiting with it.
func (a *App) askPermission(ctx context.Context, sessionID, turnID, tool string, arguments any, timeout time.Duration) (bool, string) {
	pending := &permission{ID: randomID(), Tool: tool, Arguments: arguments, decision: make(chan bool, 1)}
	a.mu.Lock()
	a.permissions[pending.ID] = pending
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.permissions, pending.ID); a.mu.Unlock() }()
	a.emit(Event{Type: "permission", SessionID: sessionID, TurnID: turnID, Tool: tool, Arguments: arguments, PermissionID: pending.ID})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	allow, reason := false, ""
	select {
	case allow = <-pending.decision:
		reason = "Denied by the person; do not retry it another way"
		if allow {
			reason = "Allowed once by the person"
		}
	case <-ctx.Done():
		reason = "The turn ended before the person answered"
	case <-timer.C:
		reason = "The person did not answer in time"
	}
	a.emit(Event{Type: "permission_result", SessionID: sessionID, TurnID: turnID, PermissionID: pending.ID, Allow: &allow})
	return allow, reason
}

func (a *App) Decide(id string, allow bool) error {
	a.mu.Lock()
	pending := a.permissions[id]
	a.mu.Unlock()
	if pending == nil {
		return fmt.Errorf("permission request is no longer pending")
	}
	select {
	case pending.decision <- allow:
		return nil
	default:
		return fmt.Errorf("permission request already answered")
	}
}

// withoutSecrets removes the keys and tokens of the kernel's auth store from
// text, such as an error that repeats what a provider was sent.
func (a *App) withoutSecrets(text string) string {
	credentials, err := kernelAuth(a.paths.Kernel).read()
	if err != nil {
		return text
	}
	secrets := []string{}
	for _, raw := range credentials {
		var credential authCredential
		if json.Unmarshal(raw, &credential) != nil {
			continue
		}
		for _, secret := range []string{credential.AccessToken, credential.RefreshToken, credential.IDToken} {
			if len(strings.TrimSpace(secret)) >= 8 {
				secrets = append(secrets, secret)
			}
		}
	}
	return redact(text, secrets...)
}

func clip(text string, n int) string {
	r := []rune(text)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return text
}

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > max {
		return nil, fmt.Errorf("file exceeds readable limit")
	}
	raw := make([]byte, info.Size())
	n, err := f.ReadAt(raw, 0)
	if err != nil && n != len(raw) {
		return nil, err
	}
	return raw, nil
}
func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return replaceFile(path, append(raw, '\n'))
}
