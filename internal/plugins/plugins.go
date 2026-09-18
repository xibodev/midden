// Package plugins reads and checks Midden integration manifests.
package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	Available   = "available"
	Disabled    = "disabled"
	Unavailable = "unavailable"
	NotChecked  = "not_checked"

	maxOpenAPIBody  = 8 << 20
	maxManifestBody = 1 << 20
)

var variable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
var placeholder = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)

type Manifest struct {
	Name      string    `yaml:"name"`
	Kind      string    `yaml:"kind"`
	Enabled   *bool     `yaml:"enabled"`
	Cost      string    `yaml:"cost"`
	Probe     *Probe    `yaml:"probe"`
	API       API       `yaml:"api"`
	Uses      []Use     `yaml:"uses"`
	Push      []Push    `yaml:"push"`
	Runs      Runs      `yaml:"runs"`
	Form      Form      `yaml:"form"`
	Feeds     []Feed    `yaml:"feeds"`
	Progress  Progress  `yaml:"progress"`
	Artifacts Artifacts `yaml:"artifacts"`
	Link      string    `yaml:"link"`
	File      string    `yaml:"-"`
}

type Probe struct {
	Kind         string `yaml:"kind"`
	URL          string `yaml:"url"`
	ExpectStatus int    `yaml:"expect_status"`
	Path         string `yaml:"path"`
	ExpectGlob   string `yaml:"expect_glob"`
}

type API struct {
	Base string `yaml:"base"`
	Auth Auth   `yaml:"auth"`
}

type Auth struct {
	Header   string `yaml:"header"`
	Scheme   string `yaml:"scheme"`
	Env      string `yaml:"env"`
	Optional bool   `yaml:"optional"`
}

type Use struct {
	Method   string `yaml:"method"`
	Path     string `yaml:"path"`
	Encoding string `yaml:"encoding"`
}

// Push describes material Midden would send to a service integration.
// The registry only validates and renders these values; it never sends them.
type Push struct {
	Name         string            `yaml:"name"`
	Label        string            `yaml:"label"`
	Endpoint     string            `yaml:"endpoint"`
	Encoding     string            `yaml:"encoding"`
	Fields       map[string]string `yaml:"fields"`
	ExpectStatus []int             `yaml:"expect_status"`
	Poll         Poll              `yaml:"poll"`
}

type Poll struct {
	URL    string   `yaml:"url"`
	Until  string   `yaml:"until"`
	States []string `yaml:"states"`
}

// The following fields model declarative external capability manifests.
// Midden does not execute it yet, but KnownFields must understand the schema
// it advertises so a typo is rejected while legitimate configuration remains
// loadable.
type Runs struct {
	Backend string `yaml:"backend"`
	Cwd     string `yaml:"cwd"`
	Prompt  string `yaml:"prompt"`
}

type Form struct {
	Source   string       `yaml:"source"`
	Discover FormDiscover `yaml:"discover"`
	Fields   []FormField  `yaml:"fields"`
}

type FormDiscover struct {
	Pipelines FormDiscovery `yaml:"pipelines"`
}

type FormDiscovery struct {
	From  string `yaml:"from"`
	Field string `yaml:"field"`
}

type FormField struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	From        string `yaml:"from"`
	Required    bool   `yaml:"required"`
	Placeholder string `yaml:"placeholder"`
	Default     any    `yaml:"default"`
}

type Feed struct {
	Kind      string `yaml:"kind"`
	Workspace string `yaml:"workspace"`
	Limit     int    `yaml:"limit"`
}

type Progress struct {
	Kind   string `yaml:"kind"`
	Path   string `yaml:"path"`
	Format string `yaml:"format"`
}

type Artifacts struct {
	Reference string `yaml:"reference"`
}

type Loaded struct {
	Manifest Manifest
	Error    error
}

type Result struct {
	Status string
	Detail string
}

// ProbeOptions is intentionally supplied by the caller, not a manifest. A
// manifest controls the target URL; allowing that same file to opt itself into
// network access turns `midden plugins list` into an internal port scanner.
type ProbeOptions struct {
	AllowNetwork bool
}

type Operation struct {
	Method string
	Path   string
	Status string
	Detail string
}

type Verification struct {
	Result     Result
	Operations []Operation
}

// LoadDir reads YAML manifests in deterministic filename order. A malformed
// manifest remains an entry so one bad file cannot hide any others.
func LoadDir(dir string) ([]Loaded, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".yaml", ".yml":
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	loaded := make([]Loaded, 0, len(names))
	for _, name := range names {
		file := filepath.Join(dir, name)
		entry, err := os.Lstat(file)
		if err != nil {
			loaded = append(loaded, Loaded{Manifest: Manifest{Name: strings.TrimSuffix(name, filepath.Ext(name)), File: file}, Error: err})
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
			loaded = append(loaded, Loaded{
				Manifest: Manifest{Name: strings.TrimSuffix(name, filepath.Ext(name)), File: file},
				Error:    fmt.Errorf("manifest must be a regular non-link file"),
			})
			continue
		}
		if entry.Size() > maxManifestBody {
			loaded = append(loaded, Loaded{
				Manifest: Manifest{Name: strings.TrimSuffix(name, filepath.Ext(name)), File: file},
				Error:    fmt.Errorf("manifest exceeds %d byte limit", maxManifestBody),
			})
			continue
		}
		handle, err := os.Open(file)
		if err != nil {
			loaded = append(loaded, Loaded{
				Manifest: Manifest{Name: strings.TrimSuffix(name, filepath.Ext(name)), File: file},
				Error:    err,
			})
			continue
		}
		data, err := io.ReadAll(io.LimitReader(handle, maxManifestBody+1))
		handle.Close()
		if err == nil && len(data) > maxManifestBody {
			err = fmt.Errorf("manifest exceeds %d byte limit", maxManifestBody)
		}
		if err != nil {
			loaded = append(loaded, Loaded{
				Manifest: Manifest{Name: strings.TrimSuffix(name, filepath.Ext(name)), File: file},
				Error:    err,
			})
			continue
		}

		declaredName := manifestDeclaredName(data)
		// Retired built-in video manifests must not reappear as generic actions.
		// Keep the user's file intact, but do not load or probe it.
		if strings.EqualFold(strings.TrimSpace(declaredName), "openmontage") {
			continue
		}
		var manifest Manifest
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		err = decoder.Decode(&manifest)
		if err == nil {
			var extra any
			if trailing := decoder.Decode(&extra); trailing != io.EOF {
				if trailing == nil {
					err = fmt.Errorf("manifest must contain exactly one YAML document")
				} else {
					err = fmt.Errorf("read trailing YAML document: %w", trailing)
				}
			}
		}
		manifest.File = file
		if err != nil && manifest.Name == "" {
			manifest.Name = declaredName
			if manifest.Name == "" {
				manifest.Name = strings.TrimSuffix(name, filepath.Ext(name))
			}
		}
		loaded = append(loaded, Loaded{Manifest: manifest, Error: err})
	}
	// Names are action identities. Allowing two valid files to share a name
	// makes a UI click ambiguous: filename sort would silently choose a
	// different destination from the card the operator saw.
	byName := map[string][]int{}
	for i, item := range loaded {
		// yaml.v3 can retain fields decoded before it encounters an unknown
		// key. A malformed `name: open-notebook` is still an identity
		// collision: ignoring it lets the UI show a later valid manifest
		// while the action loader selects the earlier broken one.
		if item.Manifest.Name != "" {
			byName[item.Manifest.Name] = append(byName[item.Manifest.Name], i)
		}
	}
	for name, positions := range byName {
		if len(positions) < 2 {
			continue
		}
		for _, i := range positions {
			if loaded[i].Error != nil {
				loaded[i].Error = fmt.Errorf("%v; duplicate manifest name %q", loaded[i].Error, name)
			} else {
				loaded[i].Error = fmt.Errorf("duplicate manifest name %q", name)
			}
		}
	}
	return loaded, nil
}

// manifestDeclaredName extracts the first document's top-level scalar name
// without applying the strict manifest schema. It is used only to quarantine
// malformed manifests that collide with another integration identity.
func manifestDeclaredName(data []byte) string {
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return ""
	}
	name, ok := values["name"].(string)
	if !ok {
		return ""
	}
	return name
}

func (m Manifest) IsEnabled() bool {
	return m.Enabled == nil || *m.Enabled
}

// Validate checks the values that make a manifest safe to advertise.
func Validate(m Manifest) []string {
	var errs []string
	if m.Name == "" {
		errs = append(errs, "missing name")
	}
	switch m.Kind {
	case "service", "capability", "coordinator":
	default:
		errs = append(errs, fmt.Sprintf("kind must be service|capability|coordinator, got %q", m.Kind))
	}
	switch m.Cost {
	case "free", "spends":
	default:
		errs = append(errs, fmt.Sprintf("cost must be free|spends, got %q", m.Cost))
	}
	if m.Probe == nil {
		errs = append(errs, "missing probe")
	} else {
		switch m.Probe.Kind {
		case "http":
			if strings.TrimSpace(m.Probe.URL) == "" {
				errs = append(errs, "http probe URL must not be empty")
			}
		case "directory":
			if strings.TrimSpace(m.Probe.Path) == "" {
				errs = append(errs, "directory probe path must not be empty")
			}
		default:
			errs = append(errs, fmt.Sprintf("probe kind must be http|directory, got %q", m.Probe.Kind))
		}
	}
	if m.Kind == "service" {
		for i, push := range m.Push {
			prefix := fmt.Sprintf("push[%d]", i)
			if !validAPIPath(push.Endpoint) {
				errs = append(errs, prefix+" endpoint must be an absolute API path")
			}
			switch push.Encoding {
			case "json", "multipart":
			default:
				errs = append(errs, fmt.Sprintf("%s encoding must be json|multipart, got %q", prefix, push.Encoding))
			}
			if len(push.Fields) == 0 {
				errs = append(errs, prefix+" fields must not be empty")
			}
		}
	}
	return errs
}

// ProbeManifest establishes availability with a bounded unauthenticated read.
// The client parameter is retained for API compatibility but deliberately
// ignored: probes must not inherit caller redirects, proxies, or credentials.
func ProbeManifest(ctx context.Context, m Manifest, _ *http.Client) Result {
	return ProbeManifestWithOptions(ctx, m, nil, ProbeOptions{})
}

// ProbeManifestWithOptions establishes availability with a bounded,
// unauthenticated read. By default HTTP probes are loopback-only; callers
// must explicitly authorize a network target.
func ProbeManifestWithOptions(ctx context.Context, m Manifest, _ *http.Client, options ProbeOptions) Result {
	if !m.IsEnabled() {
		return Result{Status: Disabled, Detail: "plugin is disabled"}
	}
	if m.Probe == nil {
		return Result{Status: Unavailable, Detail: "no probe declared - cannot establish availability"}
	}
	switch m.Probe.Kind {
	case "http":
		return probeHTTP(ctx, m.Probe, options)
	case "directory":
		return probeDirectory(m.Probe, options)
	default:
		return Result{Status: Unavailable, Detail: fmt.Sprintf("unknown probe kind: %s", m.Probe.Kind)}
	}
}

func probeHTTP(ctx context.Context, spec *Probe, options ProbeOptions) Result {
	if strings.Contains(spec.URL, "${") {
		// A manifest controls the host. Expanding arbitrary process
		// environment variables in its URL would send AWS_SECRET_ACCESS_KEY
		// (or any other ambient secret) to that host via a query/path.
		return Result{Status: Unavailable, Detail: "HTTP probe URLs may not interpolate environment variables"}
	}
	u, err := url.Parse(spec.URL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return Result{Status: Unavailable, Detail: "invalid unauthenticated http probe URL"}
	}
	if !options.AllowNetwork && !isLoopbackHost(u.Hostname()) {
		return Result{Status: Unavailable, Detail: "network probe requires explicit authorization"}
	}

	requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{Status: Unavailable, Detail: err.Error()}
	}
	resp, err := probeHTTPClient().Do(req)
	if err != nil {
		return Result{Status: Unavailable, Detail: fmt.Sprintf("%s unreachable (%T)", safeProbeTarget(u), err)}
	}
	defer resp.Body.Close()

	want := expectedStatus(spec)
	if resp.StatusCode != want {
		return Result{Status: Unavailable, Detail: fmt.Sprintf("%s returned %d, expected %d", safeProbeTarget(u), resp.StatusCode, want)}
	}
	return Result{Status: Available, Detail: fmt.Sprintf("%s -> %d", safeProbeTarget(u), resp.StatusCode)}
}

func probeDirectory(spec *Probe, options ProbeOptions) Result {
	dir, err := expand(spec.Path)
	if err != nil {
		return Result{Status: Unavailable, Detail: err.Error()}
	}
	if strings.TrimSpace(dir) == "" {
		return Result{Status: Unavailable, Detail: "directory probe path must not be empty"}
	}
	// A directory path can be an automount, FUSE/NFS/CIFS mount, mapped drive,
	// or junction. Determining which may itself trigger I/O, so do not touch
	// it until the operator explicitly authorizes directory/network probing.
	if !options.AllowNetwork {
		return Result{Status: Unavailable, Detail: "directory probe requires explicit authorization"}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return Result{Status: Unavailable, Detail: fmt.Sprintf("could not resolve directory probe path: %v", err)}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Status: Unavailable, Detail: fmt.Sprintf("%s does not exist", safeDirectoryTarget(spec.Path))}
		}
		return Result{Status: Unavailable, Detail: fmt.Sprintf("%s is unreadable", safeDirectoryTarget(spec.Path))}
	}
	if spec.ExpectGlob == "" {
		return Result{Status: Available, Detail: safeDirectoryTarget(spec.Path)}
	}
	for _, entry := range entries {
		matched, err := filepath.Match(spec.ExpectGlob, entry.Name())
		if err != nil {
			return Result{Status: Unavailable, Detail: fmt.Sprintf("invalid expect_glob %q: %v", spec.ExpectGlob, err)}
		}
		if matched {
			return Result{Status: Available, Detail: fmt.Sprintf("%s contains %s", safeDirectoryTarget(spec.Path), spec.ExpectGlob)}
		}
	}
	return Result{Status: Unavailable, Detail: fmt.Sprintf("%s contains no %s", safeDirectoryTarget(spec.Path), spec.ExpectGlob)}
}

// VerifyService checks an available service manifest's declared operations
// against the OpenAPI document supplied by its HTTP probe.
func VerifyService(ctx context.Context, m Manifest, client *http.Client) Verification {
	return VerifyServiceWithOptions(ctx, m, client, ProbeOptions{})
}

// VerifyServiceWithOptions checks a service's declared operations against the
// live OpenAPI document after applying the same target policy as its probe.
func VerifyServiceWithOptions(ctx context.Context, m Manifest, client *http.Client, options ProbeOptions) Verification {
	result := ProbeManifestWithOptions(ctx, m, client, options)
	if result.Status != Available {
		return Verification{Result: result}
	}
	if m.Probe == nil || m.Probe.Kind != "http" {
		return Verification{Result: Result{Status: Unavailable, Detail: "service verification requires an http probe"}}
	}

	if strings.Contains(m.Probe.URL, "${") {
		return Verification{Result: Result{Status: Unavailable, Detail: "HTTP probe URLs may not interpolate environment variables"}}
	}
	rawURL := m.Probe.URL
	probeURL, err := url.Parse(rawURL)
	if err != nil || probeURL.Scheme == "" || probeURL.Host == "" {
		return Verification{Result: Result{Status: Unavailable, Detail: "invalid unauthenticated http probe URL"}}
	}
	target := safeProbeTarget(probeURL)
	if err := validateAPIOrigin(probeURL, m.API.Base); err != nil {
		return Verification{Result: Result{Status: Unavailable, Detail: err.Error()}}
	}
	requestCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Verification{Result: Result{Status: Unavailable, Detail: err.Error()}}
	}
	resp, err := probeHTTPClient().Do(req)
	if err != nil {
		return Verification{Result: Result{Status: Unavailable, Detail: fmt.Sprintf("%s OpenAPI unavailable (%T)", target, err)}}
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus(m.Probe) {
		return Verification{Result: Result{
			Status: Unavailable,
			Detail: fmt.Sprintf("%s returned %d, expected %d", target, resp.StatusCode, expectedStatus(m.Probe)),
		}}
	}

	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOpenAPIBody)).Decode(&document); err != nil {
		return Verification{Result: Result{Status: Unavailable, Detail: fmt.Sprintf("could not parse the spec: %v", err)}}
	}

	base, err := apiBasePath(m.API.Base)
	if err != nil {
		return Verification{Result: Result{Status: Unavailable, Detail: err.Error()}}
	}
	uses := append([]Use(nil), m.Uses...)
	seenPush := map[string]bool{}
	for _, use := range uses {
		seenPush[strings.ToUpper(use.Method)+"\x00"+use.Path] = true
	}
	for _, push := range m.Push {
		key := "POST\x00" + push.Endpoint
		if !seenPush[key] {
			uses = append(uses, Use{Method: "POST", Path: push.Endpoint, Encoding: push.Encoding})
			seenPush[key] = true
		}
	}
	verification := Verification{
		Result:     result,
		Operations: make([]Operation, 0, len(uses)),
	}
	for _, use := range uses {
		wantPath := joinPath(base, use.Path)
		method := strings.ToLower(use.Method)
		entry, found := document.Paths[wantPath]
		op := Operation{Method: strings.ToUpper(method), Path: wantPath}
		if !found {
			op.Status, op.Detail = "missing", "path not present upstream"
		} else if _, found := entry[method]; !found {
			op.Status, op.Detail = "missing", fmt.Sprintf("verb not present (has %s)", methods(entry))
		} else {
			op.Status, op.Detail = "ok", "declared operation exists"
		}
		verification.Operations = append(verification.Operations, op)
	}
	for _, op := range verification.Operations {
		if op.Status != "ok" {
			verification.Result = Result{
				Status: Unavailable,
				Detail: fmt.Sprintf("%s is incompatible: one or more declared operations are missing", target),
			}
			break
		}
	}
	return verification
}

func probeHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func expand(value string) (string, error) {
	var missing string
	expanded := variable.ReplaceAllStringFunc(value, func(match string) string {
		name := variable.FindStringSubmatch(match)[1]
		if got, ok := os.LookupEnv(name); ok {
			return got
		}
		missing = name
		return match
	})
	if missing != "" {
		return "", fmt.Errorf("%s is not set", missing)
	}
	return expanded, nil
}

// RenderFields substitutes {{placeholder}} values without interpreting their
// resulting content, preserving form strings such as embedded JSON.
func RenderFields(fields map[string]string, values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(fields))
	for name, raw := range fields {
		var missing string
		rendered := placeholder.ReplaceAllStringFunc(raw, func(match string) string {
			key := placeholder.FindStringSubmatch(match)[1]
			value, ok := values[key]
			if !ok {
				missing = key
				return match
			}
			return value
		})
		if missing != "" {
			return nil, fmt.Errorf("field %s needs value for %q", name, missing)
		}
		out[name] = rendered
	}
	return out, nil
}

func expectedStatus(probe *Probe) int {
	if probe.ExpectStatus == 0 {
		return http.StatusOK
	}
	return probe.ExpectStatus
}

func apiBasePath(base string) (string, error) {
	if base == "" {
		return "", nil
	}
	if strings.Contains(base, "${") {
		return "", fmt.Errorf("API base URLs may not interpolate environment variables")
	}
	u, err := url.Parse(base)
	if err != nil || u.User != nil ||
		(u.Scheme != "" && ((u.Scheme != "http" && u.Scheme != "https") || u.Host == "")) ||
		(u.Scheme == "" && (u.Host != "" || !strings.HasPrefix(u.Path, "/"))) {
		return "", fmt.Errorf("invalid api base")
	}
	return u.Path, nil
}

func validateAPIOrigin(probe *url.URL, base string) error {
	if base == "" || strings.HasPrefix(base, "/") {
		return nil
	}
	if strings.Contains(base, "${") {
		return fmt.Errorf("API base URLs may not interpolate environment variables")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return fmt.Errorf("invalid api base")
	}
	if !strings.EqualFold(u.Scheme, probe.Scheme) || !strings.EqualFold(u.Host, probe.Host) {
		return fmt.Errorf("API base must share the probe origin")
	}
	return nil
}

func validAPIPath(raw string) bool {
	if raw == "" || !strings.HasPrefix(raw, "/") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "" && u.Host == "" && u.RawQuery == "" && u.Fragment == "" && !strings.HasPrefix(raw, "//")
}

func safeProbeTarget(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// safeDirectoryTarget preserves a configured variable name but never the
// expanded value. A manifest can name ${CAPABILITY_HOME}, but it must not be
// able to expand ${AWS_SECRET_ACCESS_KEY} and return that ambient secret
// through the CLI or browser API.
func safeDirectoryTarget(raw string) string {
	if strings.Contains(raw, "${") {
		return raw
	}
	return filepath.Clean(raw)
}

func joinPath(prefix, suffix string) string {
	if prefix == "" {
		return "/" + strings.TrimPrefix(suffix, "/")
	}
	return "/" + strings.TrimPrefix(path.Join(prefix, suffix), "/")
}

func methods(entry map[string]json.RawMessage) string {
	var found []string
	for method := range entry {
		switch strings.ToLower(method) {
		case "get", "put", "post", "delete", "options", "head", "patch", "trace":
			found = append(found, strings.ToUpper(method))
		}
	}
	sort.Strings(found)
	return strings.Join(found, ", ")
}
