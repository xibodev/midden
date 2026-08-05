package integrations

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/plugins"
)

func TestLoadMissingDoesNotCreateRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing", "settings")

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Config{}) {
		t.Fatalf("Load() = %#v, want zero Config", got)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("Load() created root or returned unexpected error: %v", err)
	}
}

func TestSaveLoadRoundTripAndNormalizesVersion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "settings")
	verified := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	want := Config{
		OpenNotebook: &OpenNotebookSettings{
			Enabled:          true,
			APIURL:           "http://127.0.0.1:5055/api/",
			UIURL:            "http://localhost:8502",
			PasswordRequired: true,
			LastVerified:     verified,
			LastStatus:       "available",
			LastDetail:       "OpenAPI verified",
		},
		OpenMontage: &OpenMontageSettings{
			Enabled:      true,
			Home:         filepath.Join(root, "OpenMontage"),
			Backend:      "copilot",
			LastVerified: verified,
			LastStatus:   "available",
			LastDetail:   "pipeline definitions found",
		},
	}

	if err := Save(root, want); err != nil {
		t.Fatal(err)
	}
	path := ConfigPath(root)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\n  \"open_notebook\"") || !strings.HasSuffix(string(body), "\n") {
		t.Fatalf("settings are not pretty JSON: %q", body)
	}
	if strings.Contains(string(body), "OPEN_NOTEBOOK_PASSWORD") {
		t.Fatalf("settings unexpectedly stored a credential reference: %s", body)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("settings mode = %#o, want 0600", info.Mode().Perm())
		}
	}

	want.OpenMontage.LastStatus = "reverified"
	if err := Save(root, want); err != nil {
		t.Fatalf("Save() did not atomically replace existing settings: %v", err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want.Version = configVersion
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestLoadRejectsUnknownAndTrailingJSON(t *testing.T) {
	root := t.TempDir()
	path := ConfigPath(root)
	for name, body := range map[string]string{
		"unknown":  `{"version":1,"unknown":true}`,
		"nested":   `{"version":1,"open_notebook":{"api_url":"http://localhost:5055","ui_url":"http://localhost:8502","unexpected":true}}`,
		"trailing": `{"version":1} {"version":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil {
				t.Fatal("Load() accepted invalid JSON")
			}
		})
	}
}

func TestLoadRejectsOversizedAndSymlinkSettings(t *testing.T) {
	root := t.TempDir()
	path := ConfigPath(root)
	if err := os.WriteFile(path, make([]byte, maxConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized Load() error = %v", err)
	}

	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "regular non-link") {
		t.Fatalf("symlink Load() error = %v", err)
	}
	if err := Save(root, Config{}); err == nil || !strings.Contains(err.Error(), "regular non-link") {
		t.Fatalf("symlink Save() error = %v", err)
	}
}

func TestValidationRejectsUnsafeNotebookURLs(t *testing.T) {
	valid := OpenNotebookSettings{
		APIURL: "https://[::1]:5055/api/",
		UIURL:  "http://localhost:8502/",
	}
	if err := ValidateOpenNotebook(valid); err != nil {
		t.Fatalf("valid loopback URLs rejected: %v", err)
	}

	for name, mutate := range map[string]func(*OpenNotebookSettings){
		"remote host": func(s *OpenNotebookSettings) { s.APIURL = "https://example.com/api" },
		"bad scheme":  func(s *OpenNotebookSettings) { s.APIURL = "file:///api" },
		"userinfo":    func(s *OpenNotebookSettings) { s.UIURL = "http://user@localhost:8502" },
		"fragment":    func(s *OpenNotebookSettings) { s.UIURL = "http://localhost:8502/#fragment" },
		"query":       func(s *OpenNotebookSettings) { s.APIURL = "http://localhost:5055/api?token=nope" },
		"bad api path": func(s *OpenNotebookSettings) {
			s.APIURL = "http://localhost:5055/other"
		},
		"empty api": func(s *OpenNotebookSettings) { s.APIURL = "" },
		"empty ui":  func(s *OpenNotebookSettings) { s.UIURL = "" },
	} {
		t.Run(name, func(t *testing.T) {
			settings := valid
			mutate(&settings)
			if err := ValidateOpenNotebook(settings); err == nil {
				t.Fatal("ValidateOpenNotebook() accepted unsafe settings")
			}
		})
	}
}

func TestValidateOpenMontageRejectsRelativeOrInvalidSettings(t *testing.T) {
	valid := OpenMontageSettings{
		Home:    filepath.Join(t.TempDir(), "OpenMontage"),
		Backend: "claude",
	}
	if err := ValidateOpenMontage(valid); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}

	for name, mutate := range map[string]func(*OpenMontageSettings){
		"relative home": func(s *OpenMontageSettings) { s.Home = "OpenMontage" },
		"empty home":    func(s *OpenMontageSettings) { s.Home = "" },
		"expanded home": func(s *OpenMontageSettings) {
			s.Home = filepath.Join(string(filepath.Separator), "${OPENMONTAGE_HOME}")
		},
		"bad backend": func(s *OpenMontageSettings) { s.Backend = "cursor" },
	} {
		t.Run(name, func(t *testing.T) {
			settings := valid
			mutate(&settings)
			if err := ValidateOpenMontage(settings); err == nil {
				t.Fatal("ValidateOpenMontage() accepted invalid settings")
			}
		})
	}
}

func TestOpenNotebookManifestHasSafeP3Contract(t *testing.T) {
	manifest, err := OpenNotebookManifest(OpenNotebookSettings{
		Enabled:          true,
		APIURL:           "http://127.0.0.1:5055/api/",
		UIURL:            "http://localhost:8502/",
		PasswordRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != OpenNotebookID || manifest.Kind != "service" || manifest.Cost != "free" ||
		manifest.Enabled == nil || !*manifest.Enabled {
		t.Fatalf("identity = %#v", manifest)
	}
	if manifest.Probe == nil || manifest.Probe.URL != "http://127.0.0.1:5055/openapi.json" {
		t.Fatalf("probe = %#v", manifest.Probe)
	}
	if manifest.API.Base != "http://127.0.0.1:5055/api" ||
		manifest.API.Auth.Header != "Authorization" ||
		manifest.API.Auth.Scheme != "Bearer" ||
		manifest.API.Auth.Env != "OPEN_NOTEBOOK_PASSWORD" ||
		manifest.API.Auth.Optional {
		t.Fatalf("API = %#v", manifest.API)
	}
	if len(manifest.Uses) != 2 || manifest.Uses[0].Method != "POST" || manifest.Uses[0].Path != "/sources" ||
		manifest.Uses[1].Method != "GET" || manifest.Uses[1].Path != "/sources/{source_id}/status" {
		t.Fatalf("uses = %#v", manifest.Uses)
	}
	if len(manifest.Push) != 1 || manifest.Push[0].Encoding != "multipart" ||
		!strings.Contains(manifest.Push[0].Fields["content"], "{{body}}") ||
		!strings.Contains(manifest.Push[0].Fields["notebooks"], "{{notebook_id}}") {
		t.Fatalf("push = %#v", manifest.Push)
	}
	if !strings.Contains(manifest.Link, "{{notebook_id|urlencode}}") {
		t.Fatalf("link = %q", manifest.Link)
	}
	if errs := plugins.Validate(manifest); len(errs) != 0 {
		t.Fatalf("plugins.Validate() = %v", errs)
	}

	optional, err := OpenNotebookManifest(OpenNotebookSettings{
		APIURL: "http://localhost:5055",
		UIURL:  "http://localhost:8502/notebooks",
	})
	if err != nil {
		t.Fatal(err)
	}
	if optional.API.Base != "http://localhost:5055/api" || optional.Probe.URL != "http://localhost:5055/openapi.json" ||
		optional.API.Auth != (plugins.Auth{}) || optional.Link != "http://localhost:8502/notebooks/{{notebook_id|urlencode}}" {
		t.Fatalf("normalized optional manifest = %#v", optional)
	}
}

func TestOpenMontageManifestUsesLiteralConfiguredPaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "OpenMontage")
	manifest, err := OpenMontageManifest(OpenMontageSettings{
		Enabled: true,
		Home:    home,
		Backend: "opencode",
	})
	if err != nil {
		t.Fatal(err)
	}
	pipelineDefs := filepath.Join(home, "pipeline_defs")
	if manifest.Name != OpenMontageID || manifest.Kind != "capability" || manifest.Cost != "spends" ||
		manifest.Enabled == nil || !*manifest.Enabled {
		t.Fatalf("identity = %#v", manifest)
	}
	if manifest.Probe == nil || manifest.Probe.Kind != "directory" || manifest.Probe.Path != pipelineDefs ||
		manifest.Probe.ExpectGlob != "*.yaml" {
		t.Fatalf("probe = %#v", manifest.Probe)
	}
	if manifest.Runs.Backend != "opencode" || manifest.Runs.Cwd != home ||
		!strings.Contains(manifest.Runs.Prompt, "{{pipeline}}") ||
		!strings.Contains(manifest.Runs.Prompt, "{{topic}}") ||
		!strings.Contains(manifest.Runs.Prompt, "{{budget}}") ||
		manifest.Form.Source != "schema" ||
		manifest.Form.Discover.Pipelines.From != filepath.Join(pipelineDefs, "*.yaml") ||
		manifest.Form.Discover.Pipelines.Field != "name" {
		t.Fatalf("capability declaration = %#v", manifest)
	}
	if len(manifest.Form.Fields) != 3 ||
		manifest.Form.Fields[0].Name != "pipeline" || manifest.Form.Fields[0].Type != "enum" ||
		manifest.Form.Fields[0].From != "pipelines" || !manifest.Form.Fields[0].Required ||
		manifest.Form.Fields[1].Name != "topic" || manifest.Form.Fields[1].Type != "text" ||
		!manifest.Form.Fields[1].Required ||
		manifest.Form.Fields[2].Name != "budget" || manifest.Form.Fields[2].Type != "number" ||
		manifest.Form.Fields[2].Default != 2.00 ||
		len(manifest.Feeds) != 1 || manifest.Feeds[0].Kind != "nuggets" ||
		manifest.Feeds[0].Workspace != "{{workspace}}" || manifest.Feeds[0].Limit != 40 ||
		manifest.Progress.Path != filepath.Join(home, "projects", "{{project}}", "pipeline") ||
		manifest.Artifacts.Reference != filepath.Join(home, "projects", "{{project}}", "renders") {
		t.Fatalf("capability metadata = %#v", manifest)
	}
	for _, value := range []string{
		manifest.Probe.Path,
		manifest.Runs.Cwd,
		manifest.Form.Discover.Pipelines.From,
		manifest.Progress.Path,
		manifest.Artifacts.Reference,
	} {
		if strings.Contains(value, "${") {
			t.Fatalf("manifest expanded an environment variable: %q", value)
		}
	}
	if errs := plugins.Validate(manifest); len(errs) != 0 {
		t.Fatalf("plugins.Validate() = %v", errs)
	}
}

func TestCatalogIdentifiesOnlyKnownUpstreams(t *testing.T) {
	got := Catalog()
	if len(got) != 2 {
		t.Fatalf("Catalog() returned %d entries, want 2", len(got))
	}
	byID := make(map[string]CatalogEntry, len(got))
	for _, entry := range got {
		byID[entry.ID] = entry
		if strings.Contains(strings.ToLower(entry.InstallSummary), "automatically") {
			t.Fatalf("catalog claims automatic installation: %#v", entry)
		}
	}
	notebook, ok := byID[OpenNotebookID]
	if !ok || notebook.GitHubURL != "https://github.com/lfnovo/open-notebook" ||
		!strings.Contains(notebook.InstallSummary, "Docker Desktop/Compose") ||
		!strings.Contains(notebook.InstallSummary, "encryption key") ||
		!strings.Contains(notebook.InstallSummary, "local-only") ||
		!strings.Contains(notebook.InstallSummary, "Midden does not install") {
		t.Fatalf("Open Notebook catalog entry = %#v", notebook)
	}
	montage, ok := byID[OpenMontageID]
	if !ok || montage.GitHubURL != "https://github.com/calesthio/OpenMontage" {
		t.Fatalf("OpenMontage catalog entry = %#v", montage)
	}
	for _, required := range []string{"local repository", "Python 3.10+", "Node 18+", "FFmpeg", "agentic CLI"} {
		if !strings.Contains(montage.InstallSummary, required) {
			t.Fatalf("OpenMontage install summary %q lacks %q", montage.InstallSummary, required)
		}
	}
}

func TestLoadNormalizesStoredVersionZero(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(ConfigPath(root), []byte(`{"version":0}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != configVersion {
		t.Fatalf("Version = %d, want %d", got.Version, configVersion)
	}
}

func TestSettingsLockSerializesProcesses(t *testing.T) {
	root := t.TempDir()
	first, err := AcquireSettingsLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := AcquireSettingsLock(root)
	if !errors.Is(err, ErrSettingsLocked) {
		if second != nil {
			second.Release()
		}
		t.Fatalf("second lock error=%v, want ErrSettingsLocked", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireSettingsLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
}
