package main

import (
	"context"
	"crypto/rand"
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

	"github.com/xibodev/facet-studio/pkg/config"
)

const kernelVersion = "v1.0.1-0.20260922143928-0b024a53c4f6"

type Options struct {
	Workspace, State, Core, CoreVersion, Bundle string
	SourceEnv                                   map[string]string
	Frontend                                    fs.FS
}
type Message struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}
type Session struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Updated  time.Time `json:"updated"`
	Messages []Message `json:"messages"`
}
type Model struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Endpoint      string `json:"endpoint"`
	CredentialRef string `json:"credentialRef,omitempty"`
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
type engine interface {
	Process(context.Context, string, string) (string, error)
	Close()
}
type App struct {
	opts           Options
	csrf           string
	mu             sync.Mutex
	model          Model
	credential     string
	sessions       map[string]*Session
	active         *activeTurn
	permissions    map[string]*permission
	events         []Event
	seq            uint64
	watchers       map[chan Event]bool
	runtime        engine
	projected      string
	login          *loginFlow
	authGeneration uint64
	bundles        []BundleInfo
	wg             sync.WaitGroup
}

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("operating system randomness unavailable")
	}
	return hex.EncodeToString(value[:])
}

func NewApp(opts Options) (*App, error) {
	if !filepath.IsAbs(opts.Workspace) || !filepath.IsAbs(opts.State) {
		return nil, fmt.Errorf("workspace and state paths must be absolute")
	}
	workspace, err := filepath.EvalSymlinks(opts.Workspace)
	if err != nil {
		return nil, err
	}
	opts.Workspace = workspace
	opts.State, err = resolvedDestination(opts.State)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace must be an existing directory")
	}
	if opts.Workspace == filepath.Clean(opts.State) {
		return nil, fmt.Errorf("state must not be the workspace root")
	}
	if err := protectSourceStores(opts); err != nil {
		return nil, err
	}
	if err := os.Setenv(config.EnvHome, filepath.Join(opts.State, "kernel")); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(opts.State, 0700); err != nil {
		return nil, err
	}
	app := &App{opts: opts, csrf: randomID(), sessions: map[string]*Session{}, permissions: map[string]*permission{}, watchers: map[chan Event]bool{}, bundles: []BundleInfo{}}
	if opts.Bundle != "" {
		app.projected, app.bundles, err = mountBundle(opts.Bundle, opts.State)
		if err != nil {
			return nil, err
		}
	}
	for name, dest := range map[string]any{"model.json": &app.model, "sessions.json": &app.sessions} {
		raw, err := readBounded(filepath.Join(opts.State, name), 8<<20)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, dest); err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
	}
	if app.sessions == nil {
		app.sessions = map[string]*Session{}
	}
	return app, nil
}

func (a *App) Close() {
	a.mu.Lock()
	if a.active != nil {
		a.active.cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runtime != nil {
		a.runtime.Close()
		a.runtime = nil
	}
}

func (a *App) Status() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	active := ""
	if a.active != nil {
		active = a.active.ID
	}
	return map[string]any{"workspace": a.opts.Workspace, "coreVersion": a.opts.CoreVersion, "kernelVersion": kernelVersion,
		"bundles": a.bundles, "model": a.modelStatusLocked(), "activeTurn": active, "csrfToken": a.csrf,
		"notice": "Kernel candidate build. Approved shell commands run with your account; this is not an OS sandbox."}
}
func (a *App) modelStatusLocked() map[string]any {
	return map[string]any{"provider": a.model.Provider, "model": a.model.Model, "endpoint": a.model.Endpoint,
		"credentialRef": a.model.CredentialRef, "configured": a.model.Model != "", "credentialConfigured": a.model.CredentialRef != "",
		"authStatus": "Credentials are checked by the provider when used."}
}
func (a *App) NewSession(title string) (Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) >= 200 {
		return Session{}, fmt.Errorf("workspace session limit reached")
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
	return copy, nil
}
func (a *App) saveSessionsLocked() error {
	raw, err := json.MarshalIndent(a.sessions, "", "  ")
	if err != nil {
		return err
	}
	if len(raw)+1 > 8<<20 {
		return fmt.Errorf("conversation history limit reached; use another UI state directory before adding more history")
	}
	return writeJSON(filepath.Join(a.opts.State, "sessions.json"), a.sessions)
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
	if a.model.Model == "" {
		a.mu.Unlock()
		return "", fmt.Errorf("configure a model before starting a conversation")
	}
	if a.runtime == nil {
		runtime, err := newKernel(a)
		if err != nil {
			a.mu.Unlock()
			return "", err
		}
		a.runtime = runtime
	}
	ctx, cancel := context.WithCancel(context.Background())
	turn := &activeTurn{ID: randomID(), SessionID: id, cancel: cancel}
	a.active = turn
	s.Messages = append(s.Messages, Message{Role: "user", Content: text, At: time.Now().UTC()})
	s.Updated = time.Now().UTC()
	if s.Title == "New conversation" {
		s.Title = clip(text, 80)
	}
	if err := a.saveSessionsLocked(); err != nil {
		a.active = nil
		cancel()
		s.Messages = s.Messages[:len(s.Messages)-1]
		a.mu.Unlock()
		return "", err
	}
	runtime := a.runtime
	a.wg.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.wg.Done()
		defer cancel()
		result, err := runtime.Process(ctx, text, id)
		a.mu.Lock()
		if err == nil {
			s.Messages = append(s.Messages, Message{Role: "assistant", Content: result, At: time.Now().UTC()})
			s.Updated = time.Now().UTC()
			err = a.saveSessionsLocked()
			if err != nil {
				s.Messages = s.Messages[:len(s.Messages)-1]
			}
		}
		a.active = nil
		a.mu.Unlock()
		if err != nil {
			message := err.Error()
			if errors.Is(err, context.Canceled) {
				message = "Turn cancelled. Files already written were not undone."
			}
			a.emit(Event{Type: "error", SessionID: id, TurnID: turn.ID, Error: message})
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err = tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
