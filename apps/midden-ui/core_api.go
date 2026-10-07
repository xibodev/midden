package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// coreRequestTimeout bounds one core call made through the person's door.
	coreRequestTimeout = 5 * time.Minute
	collectionDepth    = 5
	collectionLimit    = 100
	collectionWorkers  = 4
)

var coreSourceTools = map[string]bool{"copilot": true, "claude": true, "opencode": true}

// errResponded marks a request whose error response is already written.
var errResponded = errors.New("response already written")

// coreRequest is one core call made on the person's behalf.
type coreRequest struct {
	args    []string
	verdict bool // exit status 1 with JSON on stdout is a result (collection verify)
	resume  bool // the printed command line becomes {"command": ...}
}

type coreBuilder func(a *App, w http.ResponseWriter, r *http.Request, id string) (coreRequest, error)

type coreRoute struct {
	method string
	effect coreEffect
	build  coreBuilder
}

// coreRoutes maps /api/core/<name> to one core call; "*" stands for a view id.
var coreRoutes = map[string]coreRoute{
	"sessions":           {http.MethodGet, effectReadOnly, (*App).coreSessions},
	"find":               {http.MethodGet, effectReadOnly, (*App).coreFind},
	"show":               {http.MethodGet, effectReadOnly, sessionCommand("show")},
	"usage":              {http.MethodGet, effectReadOnly, sessionCommand("usage")},
	"brief":              {http.MethodGet, effectReadOnly, sessionCommand("brief")},
	"resume":             {http.MethodGet, effectReadOnly, (*App).coreResume},
	"views":              {http.MethodPost, effectWritesCache, (*App).coreOpenView},
	"views/*":            {http.MethodGet, effectReadOnly, (*App).coreReadView},
	"views/*/search":     {http.MethodPost, effectWritesCache, (*App).coreSearchView},
	"views/*/context":    {http.MethodPost, effectWritesCache, (*App).coreViewContext},
	"views/*/assets":     {http.MethodGet, effectReadOnly, (*App).coreViewAssets},
	"collect":            {http.MethodPost, effectWritesWorkspace, (*App).coreCollect},
	"collection":         {http.MethodGet, effectReadOnly, (*App).coreInspectCollection},
	"collection/records": {http.MethodGet, effectReadOnly, (*App).coreCollectionRecords},
	"collection/search":  {http.MethodPost, effectReadOnly, (*App).coreSearchCollection},
	"collection/verify":  {http.MethodPost, effectReadOnly, (*App).coreVerifyCollection},
	"collection/export":  {http.MethodPost, effectWritesWorkspace, (*App).coreExportCollection},
	"collection/merge":   {http.MethodPost, effectWritesWorkspace, (*App).coreMergeCollections},
}

// serveCore runs Core for the App's screens: routes under /api/core/ run the
// installed midden with validated arguments, without a model.
func (a *App) serveCore(w http.ResponseWriter, r *http.Request) {
	name, id := coreRouteName(strings.TrimPrefix(r.URL.Path, "/api/core/"))
	if name == "collections" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		a.serveCollections(w, r)
		return
	}
	route, ok := coreRoutes[name]
	if !ok {
		apiError(w, http.StatusNotFound, "route not found")
		return
	}
	if r.Method != route.method {
		methodNotAllowed(w, route.method)
		return
	}
	request, err := route.build(a, w, r, id)
	if errors.Is(err, errResponded) {
		return
	}
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.runCoreRequest(w, r, request, route.effect)
}

func coreRouteName(rest string) (string, string) {
	if view, ok := strings.CutPrefix(rest, "views/"); ok {
		id, action, found := strings.Cut(view, "/")
		if found {
			return "views/*/" + action, id
		}
		return "views/*", id
	}
	return rest, ""
}

func methodNotAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	apiError(w, http.StatusMethodNotAllowed, "use "+method+" for this route")
}

// runCoreRequest validates with the shared validator, checks the route's
// declared effect and passes the core's JSON through unchanged.
func (a *App) runCoreRequest(w http.ResponseWriter, r *http.Request, request coreRequest, effect coreEffect) {
	if err := a.validateCoreArgs(request.args); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if actual, err := classifyCoreArgs(request.args); err != nil || actual != effect {
		apiError(w, http.StatusInternalServerError, "core call does not match the route's declared effect")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), coreRequestTimeout)
	defer cancel()
	out, err := a.runCore(ctx, request.args)
	switch {
	case err != nil && ctx.Err() != nil:
		apiError(w, http.StatusGatewayTimeout, "the Midden core did not finish; narrow the request and retry")
	case err == nil && request.resume:
		command := firstLine(out.Stdout)
		if command == "" {
			apiError(w, http.StatusUnprocessableEntity, "the Midden core printed no resume command")
			return
		}
		respond(w, map[string]string{"command": command})
	case err == nil || request.verdict && out.ExitCode == 1:
		switch {
		case out.StdoutClipped:
			apiError(w, http.StatusUnprocessableEntity, "the core result exceeds the host's 64 KiB limit; narrow the request")
		case json.Valid([]byte(out.Stdout)):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(out.Stdout))
			if effect == effectWritesWorkspace && err == nil {
				// Every view that lists the person's files refreshes, as after an assistant turn.
				a.emit(Event{Type: "files_changed"})
			}
		case err != nil:
			apiError(w, http.StatusUnprocessableEntity, coreFailure(out))
		default:
			apiError(w, http.StatusUnprocessableEntity, "the Midden core did not return JSON")
		}
	case out.ExitCode < 0:
		apiError(w, http.StatusInternalServerError, "the Midden core could not be run")
	default:
		apiError(w, http.StatusUnprocessableEntity, coreFailure(out))
	}
}

var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// coreFailure is the first useful line the core printed on stderr.
func coreFailure(out coreOutput) string {
	fallback := ""
	for _, line := range strings.Split(out.Stderr, "\n") {
		line = strings.TrimRight(line, "\r")
		if i := strings.LastIndex(line, "\r"); i >= 0 {
			line = line[i+1:] // progress output rewrites a line in place
		}
		line = strings.TrimSpace(ansiSequence.ReplaceAllString(line, ""))
		if message, ok := strings.CutPrefix(line, "error:"); ok && strings.TrimSpace(message) != "" {
			return clip(strings.TrimSpace(message), 500)
		}
		if fallback == "" && line != "" && !strings.HasPrefix(line, "warning:") && !strings.HasPrefix(line, "note:") {
			fallback = line
		}
	}
	if fallback != "" {
		return clip(fallback, 500)
	}
	return fmt.Sprintf("the Midden core failed with exit status %d", out.ExitCode)
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// coreQuery admits only the named query parameters, each at most once.
func coreQuery(r *http.Request, names ...string) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid query string")
	}
	for key, list := range values {
		if !slices.Contains(names, key) {
			return nil, fmt.Errorf("unknown query parameter %q", key)
		}
		if len(list) > 1 {
			return nil, fmt.Errorf("query parameter %q may appear only once", key)
		}
	}
	return values, nil
}

// decodeCore reads one JSON body with no query parameters.
func decodeCore(w http.ResponseWriter, r *http.Request, value any) error {
	if _, err := coreQuery(r); err != nil {
		return err
	}
	if !decode(w, r, value) {
		return errResponded
	}
	return nil
}

// coreArgs collects core arguments and the first input problem.
type coreArgs struct {
	list []string
	err  error
}

func (c *coreArgs) add(values ...string) { c.list = append(c.list, values...) }
func (c *coreArgs) check(err error) bool {
	if err != nil && c.err == nil {
		c.err = err
	}
	return err == nil
}
func (c *coreArgs) fail(format string, args ...any) { c.check(fmt.Errorf(format, args...)) }
func (c *coreArgs) request() (coreRequest, error) {
	if c.err != nil {
		return coreRequest{}, c.err
	}
	return coreRequest{args: c.list}, nil
}

// addValue appends a flag and its value, or a positional value when flag is empty.
func (c *coreArgs) addValue(flag, value string) {
	if flag == "" {
		c.add(value)
		return
	}
	c.add(flag, value)
}
func (c *coreArgs) when(set bool, flag string) {
	if set {
		c.add(flag)
	}
}
func (c *coreArgs) tool(value string, required bool) {
	switch {
	case value == "" && !required:
	case !coreSourceTools[value]:
		c.fail("tool must be copilot, claude or opencode")
	default:
		c.add("--tool", value)
	}
}
func (c *coreArgs) id(flag, name, value string) {
	if c.check(coreToken(name, value)) {
		c.addValue(flag, value)
	}
}

// ids passes a list as one comma-joined value; ids never contain commas.
func (c *coreArgs) ids(flag, name string, values []string) {
	for _, value := range values {
		if !c.check(coreToken(name, value)) {
			return
		}
	}
	if len(values) > 0 {
		c.add(flag, strings.Join(values, ","))
	}
}
func (c *coreArgs) text(flag, name, value string, lo, hi int) {
	if c.check(coreText(name, value, lo, hi, flag == "")) {
		c.addValue(flag, value)
	}
}
func (c *coreArgs) filter(flag, name, value string) {
	if value != "" {
		c.text(flag, name, value, 1, 1024)
	}
}
func (c *coreArgs) number(flag, name string, value *int, lo, hi int) {
	if value == nil {
		return
	}
	if *value < lo || *value > hi {
		c.fail("%s must be from %d to %d", name, lo, hi)
		return
	}
	c.add(flag, strconv.Itoa(*value))
}
func (c *coreArgs) queryNumber(flag, name, text string, lo, hi int) {
	if text == "" {
		return
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		c.fail("%s must be a whole number from %d to %d", name, lo, hi)
		return
	}
	c.number(flag, name, &value, lo, hi)
}
func (c *coreArgs) queryBool(flag, name, text string) {
	switch text {
	case "", "0", "false":
	case "1", "true":
		c.add(flag)
	default:
		c.fail("%s must be 1, true, 0 or false", name)
	}
}
func (c *coreArgs) relative(flag, name, value string) {
	clean, err := coreRelative(name, value)
	if c.check(err) {
		c.addValue(flag, clean)
	}
}

// destination admits only a new workspace-relative output path.
func (c *coreArgs) destination(a *App, value string) {
	clean, err := coreRelative("out", value)
	if c.check(err) && c.check(a.newDestination(clean)) {
		c.add("--out", clean)
	}
}

// coreToken checks an identifier the core receives as one argument.
func coreToken(name, value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%s is required", name)
	case len(value) > 256:
		return fmt.Errorf("%s must be at most 256 bytes", name)
	case strings.HasPrefix(value, "-"):
		return fmt.Errorf("%s must not start with '-'", name)
	case !utf8.ValidString(value):
		return fmt.Errorf("%s contains an unsupported character", name)
	}
	for _, r := range value {
		if r == ',' || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return fmt.Errorf("%s contains an unsupported character", name)
		}
	}
	return nil
}

// coreText checks free text the core receives as one argument; positional
// text cannot start with '-' because the core would read it as a flag.
func coreText(name, value string, lo, hi int, positional bool) error {
	switch {
	case strings.TrimSpace(value) == "":
		return fmt.Errorf("%s is required", name)
	case len(strings.TrimSpace(value)) < lo:
		return fmt.Errorf("%s must contain at least %d characters", name, lo)
	case len(value) > hi:
		return fmt.Errorf("%s must be at most %d bytes", name, hi)
	case !utf8.ValidString(value) || strings.ContainsRune(value, 0):
		return fmt.Errorf("%s contains an unsupported character", name)
	case positional && strings.HasPrefix(value, "-"):
		return fmt.Errorf("%s must not start with '-'", name)
	}
	return nil
}

// coreRelative checks a workspace-relative path and returns its clean slash
// form. Backslashes are separators only on Windows.
func coreRelative(name, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if strings.Contains(value, `\`) {
		if filepath.Separator != '\\' {
			return "", fmt.Errorf("%s must use forward slashes", name)
		}
		value = strings.ReplaceAll(value, `\`, "/")
	}
	invalid := len(value) > 1024 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.ContainsRune(value, ':')
	for _, r := range value {
		invalid = invalid || r < 0x20 || r == 0x7f
	}
	if invalid {
		return "", fmt.Errorf("%s must be a workspace-relative path", name)
	}
	if slices.Contains(strings.Split(value, "/"), "..") {
		return "", fmt.Errorf("%s must not contain '..'", name)
	}
	clean := path.Clean(value)
	if clean == "." || strings.HasPrefix(clean, "-") || !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", fmt.Errorf("%s must be a workspace-relative path", name)
	}
	return clean, nil
}

func (a *App) coreSessions(_ http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	q, err := coreQuery(r, "tool", "days", "workspace", "repo", "all", "offset", "limit")
	if err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"ls", "--json"}}
	c.tool(q.Get("tool"), false)
	c.queryNumber("--days", "days", q.Get("days"), 0, 3650)
	c.filter("--workspace", "workspace", q.Get("workspace"))
	c.filter("--repo", "repo", q.Get("repo"))
	c.queryBool("--all", "all", q.Get("all"))
	c.queryNumber("--offset", "offset", q.Get("offset"), 0, 100000)
	c.queryNumber("--limit", "limit", q.Get("limit"), 1, 100)
	return c.request()
}

func (a *App) coreFind(_ http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	q, err := coreQuery(r, "q", "days", "tool", "limit")
	if err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"find"}}
	c.text("", "q", q.Get("q"), 1, 300)
	c.add("--json")
	c.queryNumber("--days", "days", q.Get("days"), 0, 3650)
	c.tool(q.Get("tool"), false)
	c.queryNumber("--limit", "limit", q.Get("limit"), 1, 100)
	return c.request()
}

// sessionCommand builds show, usage and brief for one exact session.
func sessionCommand(command string) coreBuilder {
	return func(_ *App, _ http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
		q, err := coreQuery(r, "tool", "id")
		if err != nil {
			return coreRequest{}, err
		}
		c := coreArgs{list: []string{command}}
		c.tool(q.Get("tool"), true)
		c.id("--session", "id", q.Get("id"))
		c.add("--json")
		return c.request()
	}
}

func (a *App) coreResume(_ http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	q, err := coreQuery(r, "id")
	if err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"resume"}}
	c.id("", "id", q.Get("id"))
	request, err := c.request()
	request.resume = true
	return request, err
}

func (a *App) coreOpenView(w http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	var input struct {
		Tool         string `json:"tool"`
		Session      string `json:"session"`
		Limit        *int   `json:"limit"`
		Chars        *int   `json:"chars"`
		IncludeTools bool   `json:"includeTools"`
	}
	if err := decodeCore(w, r, &input); err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"read"}}
	c.tool(input.Tool, true)
	c.id("--session", "session", input.Session)
	c.add("--json")
	c.number("--limit", "limit", input.Limit, 1, 256)
	c.number("--chars", "chars", input.Chars, 80, 8192)
	c.when(input.IncludeTools, "--include-tools")
	return c.request()
}

func (a *App) coreReadView(_ http.ResponseWriter, r *http.Request, id string) (coreRequest, error) {
	q, err := coreQuery(r, "offset", "limit", "chars", "includeTools")
	if err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"read"}}
	c.id("--view", "view id", id)
	c.add("--json")
	c.queryNumber("--offset", "offset", q.Get("offset"), 0, 10000)
	c.queryNumber("--limit", "limit", q.Get("limit"), 1, 256)
	c.queryNumber("--chars", "chars", q.Get("chars"), 80, 8192)
	c.queryBool("--include-tools", "includeTools", q.Get("includeTools"))
	return c.request()
}

func (a *App) coreSearchView(w http.ResponseWriter, r *http.Request, id string) (coreRequest, error) {
	var input struct {
		Query        string `json:"query"`
		Limit        *int   `json:"limit"`
		Chars        *int   `json:"chars"`
		IncludeTools bool   `json:"includeTools"`
	}
	if err := decodeCore(w, r, &input); err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"search"}}
	c.text("", "query", input.Query, 3, 300)
	c.id("--view", "view id", id)
	c.add("--json")
	c.number("--limit", "limit", input.Limit, 1, 256)
	c.number("--chars", "chars", input.Chars, 80, 8192)
	c.when(input.IncludeTools, "--include-tools")
	return c.request()
}

func (a *App) coreViewContext(w http.ResponseWriter, r *http.Request, id string) (coreRequest, error) {
	var input struct {
		Records []string `json:"records"`
		Before  *int     `json:"before"`
		After   *int     `json:"after"`
		Chars   *int     `json:"chars"`
	}
	if err := decodeCore(w, r, &input); err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"read"}}
	c.id("--view", "view id", id)
	if len(input.Records) < 1 || len(input.Records) > 16 {
		c.fail("records must list 1 to 16 record ids")
	}
	for _, record := range input.Records {
		c.id("--record", "record id", record)
	}
	c.add("--json")
	c.number("--before", "before", input.Before, 0, 5)
	c.number("--after", "after", input.After, 0, 5)
	c.number("--chars", "chars", input.Chars, 80, 8192)
	return c.request()
}

func (a *App) coreViewAssets(_ http.ResponseWriter, r *http.Request, id string) (coreRequest, error) {
	q, err := coreQuery(r, "offset", "limit")
	if err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"assets"}}
	c.id("--view", "view id", id)
	c.add("--json")
	c.queryNumber("--offset", "offset", q.Get("offset"), 0, 10000)
	c.queryNumber("--limit", "limit", q.Get("limit"), 1, 100)
	return c.request()
}

func (a *App) coreCollect(w http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	var input struct {
		Views   []string `json:"views"`
		Records []string `json:"records"`
		Assets  bool     `json:"assets"`
		Out     string   `json:"out"`
	}
	if err := decodeCore(w, r, &input); err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"collect"}}
	switch {
	case len(input.Views) < 1 || len(input.Views) > 25:
		c.fail("views must list 1 to 25 view ids")
	case len(input.Records) > 256:
		c.fail("records may list at most 256 record ids")
	case input.Assets && len(input.Records) == 0:
		c.fail("assets require selected records")
	}
	c.ids("--view", "view id", input.Views)
	c.ids("--record", "record id", input.Records)
	c.when(input.Assets, "--assets")
	c.destination(a, input.Out)
	c.add("--json")
	return c.request()
}

func (a *App) coreInspectCollection(_ http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	q, err := coreQuery(r, "path")
	if err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"collection", "inspect"}}
	c.relative("", "path", q.Get("path"))
	c.add("--json")
	return c.request()
}

func (a *App) coreCollectionRecords(_ http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	q, err := coreQuery(r, "path", "offset", "limit")
	if err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"collection", "read"}}
	c.relative("", "path", q.Get("path"))
	c.add("--json")
	c.queryNumber("--offset", "offset", q.Get("offset"), 0, 10000)
	c.queryNumber("--limit", "limit", q.Get("limit"), 1, 256)
	return c.request()
}

func (a *App) coreSearchCollection(w http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	var input struct {
		Path   string `json:"path"`
		Query  string `json:"query"`
		Offset *int   `json:"offset"`
		Limit  *int   `json:"limit"`
	}
	if err := decodeCore(w, r, &input); err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"collection", "search"}}
	c.relative("", "path", input.Path)
	c.text("--query", "query", input.Query, 1, 300)
	c.add("--json")
	c.number("--offset", "offset", input.Offset, 0, 10000)
	c.number("--limit", "limit", input.Limit, 1, 256)
	return c.request()
}

func (a *App) coreVerifyCollection(w http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	var input struct {
		Path   string `json:"path"`
		Record string `json:"record"`
		Quote  string `json:"quote"`
	}
	if err := decodeCore(w, r, &input); err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"collection", "verify"}}
	c.relative("", "path", input.Path)
	switch {
	case input.Record == "" && input.Quote == "":
	case input.Record == "" || input.Quote == "":
		c.fail("record and quote must be given together")
	default:
		c.id("--record", "record", input.Record)
		c.text("--quote", "quote", input.Quote, 1, 16384)
	}
	c.add("--json")
	request, err := c.request()
	request.verdict = true
	return request, err
}

func (a *App) coreExportCollection(w http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	var input struct {
		Path   string `json:"path"`
		Out    string `json:"out"`
		Format string `json:"format"`
	}
	if err := decodeCore(w, r, &input); err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"collection", "export"}}
	c.relative("", "path", input.Path)
	c.destination(a, input.Out)
	if slices.Contains([]string{"directory", "jsonl", "markdown"}, input.Format) {
		c.add("--format", input.Format)
	} else {
		c.fail("format must be directory, jsonl or markdown")
	}
	c.add("--json")
	return c.request()
}

func (a *App) coreMergeCollections(w http.ResponseWriter, r *http.Request, _ string) (coreRequest, error) {
	var input struct {
		Paths []string `json:"paths"`
		Out   string   `json:"out"`
	}
	if err := decodeCore(w, r, &input); err != nil {
		return coreRequest{}, err
	}
	c := coreArgs{list: []string{"collection", "merge"}}
	if len(input.Paths) < 1 || len(input.Paths) > 25 {
		c.fail("paths must list 1 to 25 collections")
	}
	for _, value := range input.Paths {
		c.relative("", "path", value)
	}
	c.destination(a, input.Out)
	c.add("--json")
	return c.request()
}

// serveCollections lists workspace collections, inspecting each with the core.
func (a *App) serveCollections(w http.ResponseWriter, r *http.Request) {
	if _, err := coreQuery(r); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), coreRequestTimeout)
	defer cancel()
	paths, err := a.findCollections(ctx)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "the workspace could not be listed")
		return
	}
	found := make([]json.RawMessage, len(paths))
	slots := make(chan struct{}, collectionWorkers)
	var wg sync.WaitGroup
	for i, rel := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			found[i] = a.inspectCollection(ctx, rel)
		}()
	}
	wg.Wait()
	collections := []json.RawMessage{}
	for _, info := range found {
		if info != nil {
			collections = append(collections, info)
		}
	}
	respond(w, map[string]any{"collections": collections})
}

// inspectCollection returns the core's inspection, or nil when the collection
// cannot be inspected.
func (a *App) inspectCollection(ctx context.Context, rel string) json.RawMessage {
	args := []string{"collection", "inspect", rel, "--json"}
	if a.validateCoreArgs(args) != nil {
		return nil
	}
	if effect, err := classifyCoreArgs(args); err != nil || effect != effectReadOnly {
		return nil
	}
	out, err := a.runCore(ctx, args)
	if err != nil || out.StdoutClipped || !json.Valid([]byte(out.Stdout)) {
		return nil
	}
	return json.RawMessage(out.Stdout)
}

// findCollections walks the person's files for collection manifests,
// skipping hidden directories and node_modules.
func (a *App) findCollections(ctx context.Context) ([]string, error) {
	found := []string{}
	err := filepath.WalkDir(a.paths.Files, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if path == a.paths.Files {
			return err
		}
		if err != nil || !entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(a.paths.Files, path)
		if err != nil {
			return filepath.SkipDir
		}
		if isCollectionManifest(filepath.Join(path, "manifest.json")) {
			found = append(found, filepath.ToSlash(rel))
			if len(found) >= collectionLimit {
				return filepath.SkipAll
			}
			return filepath.SkipDir
		}
		if strings.Count(rel, string(filepath.Separator))+1 >= collectionDepth {
			return filepath.SkipDir
		}
		return nil
	})
	return found, err
}

func isCollectionManifest(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	raw, err := readBounded(path, 1<<20)
	if err != nil {
		return false
	}
	var manifest struct {
		Schema string `json:"schema"`
	}
	return json.Unmarshal(raw, &manifest) == nil && manifest.Schema == "midden.collection/v1"
}
