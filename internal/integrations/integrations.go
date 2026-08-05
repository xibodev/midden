// Package integrations defines the local, human-configured integration setup.
package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/plugins"
)

const (
	OpenNotebookID = "open-notebook"
	OpenMontageID  = "openmontage"

	configFileName  = "integrations.json"
	configVersion   = 1
	maxConfigBytes  = 64 << 10
	notebookAPIPath = "/api"
)

// Config is the bounded, credential-free integration configuration.
type Config struct {
	Version       int                   `json:"version"`
	OpenNotebook  *OpenNotebookSettings `json:"open_notebook,omitempty"`
	OpenMontage   *OpenMontageSettings  `json:"open_montage,omitempty"`
	PendingLegacy *LegacyMigration      `json:"pending_legacy,omitempty"`
}

// LegacyMigration records an explicitly requested, recoverable transition
// from an advanced manifest to managed Settings. It contains file locations,
// never credentials or manifest contents.
type LegacyMigration struct {
	ID         string `json:"id"`
	SourcePath string `json:"source_path"`
	BackupPath string `json:"backup_path"`
}

// OpenNotebookSettings configures a locally running Open Notebook instance.
// PasswordRequired records only whether a password is needed; never its value.
type OpenNotebookSettings struct {
	Enabled          bool      `json:"enabled"`
	APIURL           string    `json:"api_url"`
	UIURL            string    `json:"ui_url"`
	PasswordRequired bool      `json:"password_required"`
	LastVerified     time.Time `json:"last_verified"`
	LastStatus       string    `json:"last_status"`
	LastDetail       string    `json:"last_detail"`
}

// OpenMontageSettings configures a checked-out OpenMontage repository.
type OpenMontageSettings struct {
	Enabled      bool      `json:"enabled"`
	Home         string    `json:"home"`
	Backend      string    `json:"backend"`
	LastVerified time.Time `json:"last_verified"`
	LastStatus   string    `json:"last_status"`
	LastDetail   string    `json:"last_detail"`
}

// CatalogEntry describes an integration a person may install and configure.
type CatalogEntry struct {
	ID             string
	Name           string
	Description    string
	Kind           string
	Cost           string
	GitHubURL      string
	InstallSummary string
}

// Catalog returns the supported public upstream integrations. It only
// describes their prerequisites; it never installs or configures either one.
func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{
			ID:             OpenNotebookID,
			Name:           "Open Notebook",
			Description:    "A local notebook and source-ingestion service.",
			Kind:           "service",
			Cost:           "free",
			GitHubURL:      "https://github.com/lfnovo/open-notebook",
			InstallSummary: "Use the upstream Docker Desktop/Compose setup; change the upstream encryption key before first use and review the upstream default UI/API bindings for local-only exposure. Midden does not install it.",
		},
		{
			ID:             OpenMontageID,
			Name:           "OpenMontage",
			Description:    "An agentic local video-production capability.",
			Kind:           "capability",
			Cost:           "spends",
			GitHubURL:      "https://github.com/calesthio/OpenMontage",
			InstallSummary: "Install it yourself from a local repository; it requires Python 3.10+, Node 18+, FFmpeg, and an agentic CLI.",
		},
	}
}

// ConfigPath returns the one settings file owned by this package below root.
func ConfigPath(root string) string {
	return filepath.Join(root, configFileName)
}

// Load reads settings without creating root or any other filesystem entry.
// A missing settings file is an unconfigured integration state, not an error.
func Load(root string) (Config, error) {
	path := ConfigPath(root)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("inspect integrations settings: %w", err)
	}
	if err := regularSettingsFile(info); err != nil {
		return Config{}, err
	}
	if info.Size() > maxConfigBytes {
		return Config{}, fmt.Errorf("integrations settings exceeds %d byte limit", maxConfigBytes)
	}

	handle, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open integrations settings: %w", err)
	}
	opened, statErr := handle.Stat()
	if statErr != nil {
		handle.Close()
		return Config{}, fmt.Errorf("inspect opened integrations settings: %w", statErr)
	}
	if err := regularSettingsFile(opened); err != nil {
		handle.Close()
		return Config{}, err
	}
	if !os.SameFile(info, opened) {
		handle.Close()
		return Config{}, fmt.Errorf("integrations settings changed while opening")
	}
	data, readErr := io.ReadAll(io.LimitReader(handle, maxConfigBytes+1))
	closeErr := handle.Close()
	if readErr != nil {
		return Config{}, fmt.Errorf("read integrations settings: %w", readErr)
	}
	if closeErr != nil {
		return Config{}, fmt.Errorf("close integrations settings: %w", closeErr)
	}
	if len(data) > maxConfigBytes {
		return Config{}, fmt.Errorf("integrations settings exceeds %d byte limit", maxConfigBytes)
	}

	config, err := decodeConfig(data)
	if err != nil {
		return Config{}, err
	}
	return normalizeConfig(config)
}

// Save validates and atomically replaces the settings file. It never stores a
// password or token because the configuration types contain no such fields.
func Save(root string, config Config) error {
	config, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode integrations settings: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxConfigBytes {
		return fmt.Errorf("integrations settings exceeds %d byte limit", maxConfigBytes)
	}

	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create integrations root: %w", err)
	}

	path := ConfigPath(root)
	if err := safeSettingsTarget(path); err != nil {
		return err
	}

	handle, err := os.CreateTemp(root, ".integrations-*.tmp")
	if err != nil {
		return fmt.Errorf("create integrations settings temporary file: %w", err)
	}
	tempPath := handle.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = handle.Close()
			_ = os.Remove(tempPath)
		}
	}()

	if err := restrictFilePermissions(tempPath); err != nil {
		return err
	}
	if _, err := handle.Write(body); err != nil {
		return fmt.Errorf("write integrations settings: %w", err)
	}
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync integrations settings: %w", err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("close integrations settings: %w", err)
	}

	// Do not replace a path that became a link or special file after the
	// temporary file was written.
	if err := safeSettingsTarget(path); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace integrations settings: %w", err)
	}
	if err := restrictFilePermissions(path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

// ValidateOpenNotebook checks the local-only inputs without contacting them.
func ValidateOpenNotebook(settings OpenNotebookSettings) error {
	if _, _, err := normalizedNotebookAPI(settings.APIURL); err != nil {
		return fmt.Errorf("invalid Open Notebook API URL: %w", err)
	}
	if _, err := normalizedNotebookLink(settings.UIURL); err != nil {
		return fmt.Errorf("invalid Open Notebook UI URL: %w", err)
	}
	return nil
}

// ValidateOpenMontage checks a configured repository path without touching it.
func ValidateOpenMontage(settings OpenMontageSettings) error {
	if strings.TrimSpace(settings.Home) == "" {
		return fmt.Errorf("OpenMontage home is required")
	}
	if strings.Contains(settings.Home, "${") {
		return fmt.Errorf("OpenMontage home must be a literal path")
	}
	if !filepath.IsAbs(settings.Home) {
		return fmt.Errorf("OpenMontage home must be an absolute path")
	}
	switch settings.Backend {
	case "copilot", "claude", "opencode":
	default:
		return fmt.Errorf("OpenMontage backend must be copilot|claude|opencode, got %q", settings.Backend)
	}
	return nil
}

// OpenNotebookManifest builds the fixed P3 integration contract from safe
// local settings. It does not probe the service or read the password.
func OpenNotebookManifest(settings OpenNotebookSettings) (plugins.Manifest, error) {
	if err := ValidateOpenNotebook(settings); err != nil {
		return plugins.Manifest{}, err
	}
	apiBase, probeURL, err := normalizedNotebookAPI(settings.APIURL)
	if err != nil {
		return plugins.Manifest{}, err
	}
	link, err := normalizedNotebookLink(settings.UIURL)
	if err != nil {
		return plugins.Manifest{}, err
	}
	enabled := settings.Enabled
	auth := plugins.Auth{}
	if settings.PasswordRequired {
		auth = plugins.Auth{
			Header:   "Authorization",
			Scheme:   "Bearer",
			Env:      "OPEN_NOTEBOOK_PASSWORD",
			Optional: false,
		}
	}
	manifest := plugins.Manifest{
		Name:    OpenNotebookID,
		Kind:    "service",
		Enabled: &enabled,
		Cost:    "free",
		Probe: &plugins.Probe{
			Kind:         "http",
			URL:          probeURL,
			ExpectStatus: 200,
		},
		API: plugins.API{
			Base: apiBase,
			Auth: auth,
		},
		Uses: []plugins.Use{
			{Method: "POST", Path: "/sources", Encoding: "multipart"},
			{Method: "GET", Path: "/sources/{source_id}/status"},
		},
		Push: []plugins.Push{
			{
				Name:         "nuggets-as-source",
				Label:        "Send nuggets",
				Endpoint:     "/sources",
				Encoding:     "multipart",
				ExpectStatus: []int{201},
				Fields: map[string]string{
					"type":             "text",
					"content":          "{{body}}",
					"title":            "{{title}}",
					"notebooks":        `["{{notebook_id}}"]`,
					"embed":            "true",
					"async_processing": "true",
				},
				Poll: plugins.Poll{
					URL:    "/sources/{{source_id}}/status",
					Until:  "status",
					States: []string{"completed", "failed"},
				},
			},
		},
		Link: link,
	}
	if errs := plugins.Validate(manifest); len(errs) != 0 {
		return plugins.Manifest{}, fmt.Errorf("build Open Notebook manifest: %s", strings.Join(errs, "; "))
	}
	return manifest, nil
}

// OpenMontageManifest builds the local capability declaration. Its paths are
// derived from the supplied literal home, never from environment expansion.
func OpenMontageManifest(settings OpenMontageSettings) (plugins.Manifest, error) {
	if err := ValidateOpenMontage(settings); err != nil {
		return plugins.Manifest{}, err
	}

	enabled := settings.Enabled
	pipelineDefs := filepath.Join(settings.Home, "pipeline_defs")
	manifest := plugins.Manifest{
		Name:    OpenMontageID,
		Kind:    "capability",
		Enabled: &enabled,
		Cost:    "spends",
		Probe: &plugins.Probe{
			Kind:       "directory",
			Path:       pipelineDefs,
			ExpectGlob: "*.yaml",
		},
		Runs: plugins.Runs{
			Backend: settings.Backend,
			Cwd:     settings.Home,
			Prompt:  "Run OpenMontage pipeline {{pipeline}} for {{topic}} with budget {{budget}}; estimate, reserve, and reconcile provider spend.",
		},
		Form: plugins.Form{
			Source: "schema",
			Discover: plugins.FormDiscover{
				Pipelines: plugins.FormDiscovery{
					From:  filepath.Join(pipelineDefs, "*.yaml"),
					Field: "name",
				},
			},
			Fields: []plugins.FormField{
				{
					Name:        "pipeline",
					Type:        "enum",
					From:        "pipelines",
					Required:    true,
					Placeholder: "Choose an OpenMontage pipeline",
				},
				{
					Name:        "topic",
					Type:        "text",
					Required:    true,
					Placeholder: "Describe the video topic",
				},
				{
					Name:    "budget",
					Type:    "number",
					Default: 2.00,
				},
			},
		},
		Feeds: []plugins.Feed{
			{Kind: "nuggets", Workspace: "{{workspace}}", Limit: 40},
		},
		Progress: plugins.Progress{
			Kind:   "checkpoint",
			Path:   filepath.Join(settings.Home, "projects", "{{project}}", "pipeline"),
			Format: "json",
		},
		Artifacts: plugins.Artifacts{
			Reference: filepath.Join(settings.Home, "projects", "{{project}}", "renders"),
		},
	}
	if errs := plugins.Validate(manifest); len(errs) != 0 {
		return plugins.Manifest{}, fmt.Errorf("build OpenMontage manifest: %s", strings.Join(errs, "; "))
	}
	return manifest, nil
}

func decodeConfig(data []byte) (Config, error) {
	if body := bytes.TrimSpace(data); len(body) == 0 || body[0] != '{' {
		return Config{}, fmt.Errorf("integrations settings must be one JSON object")
	}

	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode integrations settings: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("integrations settings must contain exactly one JSON value")
		}
		return Config{}, fmt.Errorf("decode trailing integrations settings: %w", err)
	}
	return config, nil
}

func normalizeConfig(config Config) (Config, error) {
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	if config.Version == 0 {
		config.Version = configVersion
	}
	return config, nil
}

func validateConfig(config Config) error {
	switch config.Version {
	case 0, configVersion:
	default:
		return fmt.Errorf("unsupported integrations settings version %d", config.Version)
	}
	if config.OpenNotebook != nil {
		if err := ValidateOpenNotebook(*config.OpenNotebook); err != nil {
			return err
		}
	}
	if config.OpenMontage != nil {
		if err := ValidateOpenMontage(*config.OpenMontage); err != nil {
			return err
		}
	}
	if config.PendingLegacy != nil {
		switch config.PendingLegacy.ID {
		case OpenNotebookID, OpenMontageID:
		default:
			return fmt.Errorf("unknown pending legacy integration %q", config.PendingLegacy.ID)
		}
		if !filepath.IsAbs(config.PendingLegacy.SourcePath) || !filepath.IsAbs(config.PendingLegacy.BackupPath) {
			return fmt.Errorf("pending legacy migration paths must be absolute")
		}
	}
	return nil
}

func regularSettingsFile(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("integrations settings must be a regular non-link file")
	}
	return nil
}

func safeSettingsTarget(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect integrations settings target: %w", err)
	}
	return regularSettingsFile(info)
}

func restrictFilePermissions(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict integrations settings permissions: %w", err)
	}
	return nil
}

func normalizedNotebookAPI(raw string) (base string, probe string, err error) {
	target, err := parseLoopbackURL(raw)
	if err != nil {
		return "", "", err
	}
	switch strings.TrimRight(target.Path, "/") {
	case "", notebookAPIPath:
	default:
		return "", "", fmt.Errorf("API URL path must be empty or %s", notebookAPIPath)
	}
	origin := loopbackOrigin(target)
	return origin + notebookAPIPath, origin + "/openapi.json", nil
}

func normalizedNotebookLink(raw string) (string, error) {
	target, err := parseLoopbackURL(raw)
	if err != nil {
		return "", err
	}

	path := strings.TrimRight(target.EscapedPath(), "/")
	base := loopbackOrigin(target) + path
	if strings.TrimRight(target.Path, "/") == "/notebooks" {
		return base + "/{{notebook_id|urlencode}}", nil
	}
	return base + "/notebooks/{{notebook_id|urlencode}}", nil
}

func parseLoopbackURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("URL is required")
	}
	if raw != strings.TrimSpace(raw) || strings.Contains(raw, "${") || strings.Contains(raw, "{{") {
		return nil, fmt.Errorf("URL must be literal")
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	if target.Opaque != "" || target.Host == "" {
		return nil, fmt.Errorf("URL must include a host")
	}
	if !strings.EqualFold(target.Scheme, "http") && !strings.EqualFold(target.Scheme, "https") {
		return nil, fmt.Errorf("URL scheme must be http or https")
	}
	if target.User != nil {
		return nil, fmt.Errorf("URL must not include user info")
	}
	if target.Fragment != "" {
		return nil, fmt.Errorf("URL must not include a fragment")
	}
	if target.RawQuery != "" {
		return nil, fmt.Errorf("URL must not include a query")
	}
	if !loopbackHost(target.Hostname()) {
		return nil, fmt.Errorf("URL host must be localhost or a loopback IP")
	}
	return target, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackOrigin(target *url.URL) string {
	return strings.ToLower(target.Scheme) + "://" + target.Host
}
