package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	agentexec "github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/integrations"
	"github.com/mekjr1/midden/internal/plugins"
)

type integrationCapabilitiesView struct {
	ID               string   `json:"id"`
	Home             string   `json:"home"`
	Pipelines        []string `json:"pipelines"`
	BacklotAvailable bool     `json:"backlot_available"`
}

func (s *Server) handleIntegrationBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	req, err := decodeIntegrationRequest(r)
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID != integrations.OpenMontageID {
		http.Error(w, "folder selection is not supported for this integration", http.StatusBadRequest)
		return
	}
	path, err := pickFolder(r.Context(), req.Home)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, map[string]string{"path": path})
}

func pickFolder(ctx context.Context, initial string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		script := `
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = 'Select the OpenMontage repository'
$dialog.ShowNewFolderButton = $false
if ($env:MIDDEN_PICKER_INITIAL -and (Test-Path -LiteralPath $env:MIDDEN_PICKER_INITIAL)) {
  $dialog.SelectedPath = $env:MIDDEN_PICKER_INITIAL
}
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write($dialog.SelectedPath)
}`
		command = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-Command", script)
		command.Env = append(os.Environ(), "MIDDEN_PICKER_INITIAL="+initial)
	case "darwin":
		command = exec.CommandContext(ctx, "osascript", "-e",
			`POSIX path of (choose folder with prompt "Select the OpenMontage repository")`)
	default:
		if binary, err := exec.LookPath("zenity"); err == nil {
			command = exec.CommandContext(ctx, binary, "--file-selection", "--directory",
				"--title=Select the OpenMontage repository")
		} else if binary, err := exec.LookPath("kdialog"); err == nil {
			command = exec.CommandContext(ctx, binary, "--getexistingdirectory", initial)
		} else {
			return "", fmt.Errorf("no native folder picker is available; install zenity or kdialog, or paste the absolute path")
		}
	}

	output, err := command.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("folder selection timed out")
		}
		if _, ok := err.(*exec.ExitError); ok {
			return "", nil
		}
		return "", fmt.Errorf("open folder picker: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (s *Server) handleIntegrationCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	req, err := decodeIntegrationRequest(r)
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID != integrations.OpenMontageID {
		http.Error(w, "capability discovery is not supported for this integration", http.StatusBadRequest)
		return
	}
	manifest, _, err := s.effectiveIntegration(req.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pipelines, err := openMontagePipelines(manifest.Runs.Cwd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	_, backlotErr := os.Stat(filepath.Join(manifest.Runs.Cwd, "backlot"))
	writeJSON(w, integrationCapabilitiesView{
		ID: req.ID, Home: manifest.Runs.Cwd, Pipelines: pipelines,
		BacklotAvailable: backlotErr == nil,
	})
}

func openMontagePipelines(home string) ([]string, error) {
	dir := filepath.Join(home, "pipeline_defs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read OpenMontage pipelines: %w", err)
	}
	pipelines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".yaml" && extension != ".yml" {
			continue
		}
		pipelines = append(pipelines, strings.TrimSuffix(entry.Name(), extension))
		if len(pipelines) >= 100 {
			break
		}
	}
	sort.Strings(pipelines)
	if len(pipelines) == 0 {
		return nil, fmt.Errorf("no OpenMontage pipeline definitions were found")
	}
	return pipelines, nil
}

func (s *Server) handleIntegrationOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	req, err := decodeIntegrationRequest(r)
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID != integrations.OpenMontageID {
		http.Error(w, "this integration does not have a local application launcher", http.StatusBadRequest)
		return
	}
	manifest, _, err := s.effectiveIntegration(req.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result := testOpenMontage(r.Context(), manifest)
	if result.Status != plugins.Available {
		http.Error(w, result.Detail, http.StatusConflict)
		return
	}
	if err := startOpenMontageBacklot(manifest.Runs.Cwd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"started": true,
		"detail":  "OpenMontage Backlot is starting in your browser.",
	})
}

type pythonCommand struct {
	path   string
	prefix []string
}

func resolveOpenMontagePython(home string) (pythonCommand, error) {
	local := []string{
		filepath.Join(home, ".venv", "Scripts", "python.exe"),
		filepath.Join(home, "venv", "Scripts", "python.exe"),
		filepath.Join(home, ".venv", "bin", "python"),
		filepath.Join(home, "venv", "bin", "python"),
	}
	for _, candidate := range local {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return pythonCommand{path: candidate}, nil
		}
	}
	for _, name := range []string{"python3", "python", "py"} {
		if path, err := exec.LookPath(name); err == nil {
			if strings.EqualFold(filepath.Base(path), "py.exe") || filepath.Base(path) == "py" {
				return pythonCommand{path: path, prefix: []string{"-3"}}, nil
			}
			return pythonCommand{path: path}, nil
		}
	}
	return pythonCommand{}, fmt.Errorf("Python 3.10+ was not found")
}

func testOpenMontage(ctx context.Context, manifest plugins.Manifest) plugins.Result {
	probed := plugins.ProbeManifestWithOptions(ctx, manifest, nil, plugins.ProbeOptions{AllowNetwork: true})
	if probed.Status != plugins.Available {
		return probed
	}
	home := manifest.Runs.Cwd
	for _, required := range []string{"AGENT_GUIDE.md", "PROJECT_CONTEXT.md"} {
		info, err := os.Stat(filepath.Join(home, required))
		if err != nil || !info.Mode().IsRegular() {
			return plugins.Result{Status: plugins.Unavailable, Detail: required + " is missing from the selected repository"}
		}
	}
	if info, err := os.Stat(filepath.Join(home, "backlot")); err != nil || !info.IsDir() {
		return plugins.Result{Status: plugins.Unavailable, Detail: "Backlot UI is missing from the selected repository"}
	}
	pipelines, err := openMontagePipelines(home)
	if err != nil {
		return plugins.Result{Status: plugins.Unavailable, Detail: err.Error()}
	}
	python, err := resolveOpenMontagePython(home)
	if err != nil {
		return plugins.Result{Status: plugins.Unavailable, Detail: err.Error()}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := append(append([]string{}, python.prefix...), "-c", "import backlot; print('backlot ready')")
	command := exec.CommandContext(checkCtx, python.path, args...)
	command.Dir = home
	if output, err := command.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return plugins.Result{Status: plugins.Unavailable, Detail: "OpenMontage Python setup is incomplete: " + detail}
	}
	if _, err := exec.LookPath("node"); err != nil {
		return plugins.Result{Status: plugins.Unavailable, Detail: "Node.js 18+ was not found on PATH"}
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return plugins.Result{Status: plugins.Unavailable, Detail: "FFmpeg was not found on PATH"}
	}
	if _, err := agentexec.Detect(manifest.Runs.Backend); err != nil {
		return plugins.Result{Status: plugins.Unavailable, Detail: err.Error()}
	}
	return plugins.Result{
		Status: plugins.Available,
		Detail: fmt.Sprintf(
			"Repository, Backlot, Python, Node.js, FFmpeg, %s, and %d pipeline(s) are ready.",
			manifest.Runs.Backend, len(pipelines)),
	}
}

func startOpenMontageBacklot(home string) error {
	python, err := resolveOpenMontagePython(home)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(index.Dir(), "logs"), 0o700); err != nil {
		return fmt.Errorf("create integration log directory: %w", err)
	}
	logPath := filepath.Join(index.Dir(), "logs", "openmontage-backlot.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open Backlot log: %w", err)
	}
	args := append(append([]string{}, python.prefix...), "-m", "backlot", "open")
	command := exec.Command(python.path, args...)
	command.Dir = home
	command.Env = append(os.Environ(), "NO_COLOR=1")
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		log.Close()
		return fmt.Errorf("start OpenMontage Backlot: %w", err)
	}
	go func() {
		_ = command.Wait()
		_ = log.Close()
	}()
	return nil
}
