package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		apiError(w, http.StatusForbidden, "only loopback hosts are accepted")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme != "http" || parsed.Host != r.Host {
			apiError(w, http.StatusForbidden, "foreign origins are not accepted")
			return
		}
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-Midden-CSRF") != a.csrf {
		apiError(w, http.StatusForbidden, "missing session CSRF token")
		return
	}
	switch {
	case r.URL.Path == "/api/status" && r.Method == "GET":
		respond(w, a.Status())
	case r.URL.Path == "/api/auth/copilot/start" && r.Method == "POST":
		var input struct {
			Model string `json:"model"`
		}
		if !decode(w, r, &input) {
			return
		}
		flow, err := a.StartLogin(input.Model)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		respond(w, flow)
	case r.URL.Path == "/api/auth/copilot/poll" && r.Method == "POST":
		var input struct {
			ID string `json:"id"`
		}
		if !decode(w, r, &input) {
			return
		}
		result, err := a.PollLogin(input.ID)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		respond(w, result)
	case r.URL.Path == "/api/sessions" && r.Method == "GET":
		respond(w, map[string]any{"sessions": a.Sessions()})
	case r.URL.Path == "/api/sessions" && r.Method == "POST":
		var input struct {
			Title string `json:"title"`
		}
		if !decode(w, r, &input) {
			return
		}
		s, err := a.NewSession(input.Title)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		respond(w, s)
	case strings.HasPrefix(r.URL.Path, "/api/sessions/"):
		rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
		id, action, _ := strings.Cut(rest, "/")
		if action == "" && r.Method == "GET" {
			s, err := a.Session(id)
			if err != nil {
				apiError(w, 404, "session not found")
				return
			}
			respond(w, s)
			return
		}
		if action == "turn" && r.Method == "POST" {
			var input struct {
				Message string `json:"message"`
			}
			if !decode(w, r, &input) {
				return
			}
			turn, err := a.StartTurn(id, input.Message)
			if err != nil {
				apiError(w, 409, err.Error())
				return
			}
			w.WriteHeader(http.StatusAccepted)
			respond(w, map[string]string{"turnId": turn})
			return
		}
		apiError(w, 404, "route not found")
	case r.URL.Path == "/api/cancel" && r.Method == "POST":
		var input struct {
			TurnID string `json:"turnId"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := a.Cancel(input.TurnID); err != nil {
			apiError(w, 409, err.Error())
			return
		}
		respond(w, map[string]bool{"cancelled": true})
	case strings.HasPrefix(r.URL.Path, "/api/permissions/") && r.Method == "POST":
		var input struct {
			Allow *bool `json:"allow"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Allow == nil {
			apiError(w, 400, "allow must be true or false")
			return
		}
		if err := a.Decide(strings.TrimPrefix(r.URL.Path, "/api/permissions/"), *input.Allow); err != nil {
			apiError(w, 409, err.Error())
			return
		}
		respond(w, map[string]bool{"accepted": true})
	case r.URL.Path == "/api/events" && r.Method == "GET":
		a.serveEvents(w, r)
	case r.URL.Path == "/api/model" && r.Method == "GET":
		a.mu.Lock()
		data := a.modelStatusLocked()
		a.mu.Unlock()
		respond(w, data)
	case r.URL.Path == "/api/providers" && r.Method == "GET":
		respond(w, map[string]any{"providers": providerRoster()})
	case r.URL.Path == "/api/models" && r.Method == "POST":
		var input ModelInput
		if !decode(w, r, &input) {
			return
		}
		catalog, err := a.DiscoverModels(r.Context(), input)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		respond(w, catalog)
	case r.URL.Path == "/api/model/check" && r.Method == "POST":
		var input ModelInput
		if !decode(w, r, &input) {
			return
		}
		if err := a.CheckModel(r.Context(), input); err != nil {
			apiError(w, 400, err.Error())
			return
		}
		respond(w, map[string]any{"ok": true, "message": "The selected model returned the required tool call. No workspace files were read or changed."})
	case r.URL.Path == "/api/model" && r.Method == "PUT":
		var input ModelInput
		if !decode(w, r, &input) {
			return
		}
		if err := a.SetModel(input); err != nil {
			apiError(w, 400, err.Error())
			return
		}
		a.mu.Lock()
		data := a.modelStatusLocked()
		a.mu.Unlock()
		respond(w, data)
	case r.URL.Path == "/api/files" && r.Method == "GET":
		files, err := a.Files()
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		respond(w, map[string]any{"files": files})
	case r.URL.Path == "/api/file" && r.Method == "GET":
		data, err := a.ReadFile(r.URL.Query().Get("path"))
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		respond(w, data)
	case (r.URL.Path == "/preview" || r.URL.Path == "/download") && r.Method == "GET":
		a.serveArtifact(w, r)
	case r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/app.js" || r.URL.Path == "/style.css"):
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if a.opts.Frontend == nil {
			apiError(w, 404, "frontend is not mounted")
			return
		}
		raw, err := fs.ReadFile(a.opts.Frontend, name)
		if err != nil {
			apiError(w, 404, "frontend file missing")
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
		w.Write(raw)
	default:
		apiError(w, 404, "route not found")
	}
}

func respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}
func apiError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	reader := http.MaxBytesReader(w, r.Body, 128<<10)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		apiError(w, 400, "invalid JSON request: "+err.Error())
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		apiError(w, 400, "one JSON value is required")
		return false
	}
	return true
}
func (a *App) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, 500, "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	last, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	ch := make(chan Event, 64)
	a.mu.Lock()
	replay := []Event{}
	for _, event := range a.events {
		if event.Seq > last {
			replay = append(replay, event)
		}
	}
	a.watchers[ch] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.watchers, ch); a.mu.Unlock() }()
	send := func(event Event) error {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.Seq, raw)
		flusher.Flush()
		return err
	}
	for _, event := range replay {
		if send(event) != nil {
			return
		}
	}
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-ch:
			if !open || send(event) != nil {
				return
			}
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
func (a *App) serveArtifact(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	path, err := a.path(name)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	file, err := os.Open(path)
	if err != nil {
		apiError(w, 404, "artifact unavailable")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 64<<20 {
		apiError(w, 400, "artifact exceeds preview limit")
		return
	}
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
	if r.URL.Path == "/download" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(name)}))
	}
	http.ServeContent(w, r, filepath.Base(name), info.ModTime(), file)
}
