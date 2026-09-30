package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func containsPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}
func resolvedDestination(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	suffix := []string{}
	for {
		_, err = os.Lstat(absolute)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", err
		}
		suffix = append(suffix, filepath.Base(absolute))
		absolute = parent
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		absolute = filepath.Join(absolute, suffix[i])
	}
	return absolute, nil
}
func (a *App) writePath(name string) error {
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.opts.Workspace, path)
	}
	resolved, err := resolvedDestination(path)
	if err != nil {
		return err
	}
	if containsPath(a.opts.State, resolved) {
		return fmt.Errorf("host state cannot be replaced by an artifact")
	}
	return withinWorkspace(a.opts.Workspace, name)
}

func protectSourceStores(opts Options) error {
	roots := opts.SourceEnv
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		roots = map[string]string{"MIDDEN_COPILOT_ROOT": filepath.Join(home, ".copilot"), "MIDDEN_CLAUDE_ROOT": filepath.Join(home, ".claude"), "MIDDEN_OPENCODE_DB": filepath.Join(home, ".local", "share", "opencode", "opencode.db")}
	}
	for _, source := range roots {
		if strings.TrimSpace(source) == "" {
			continue
		}
		resolved, err := resolvedDestination(source)
		if err != nil {
			return err
		}
		for _, target := range []string{opts.Workspace, opts.State} {
			path, err := resolvedDestination(target)
			if err != nil {
				return err
			}
			if containsPath(resolved, path) || containsPath(path, resolved) {
				return fmt.Errorf("workspace and UI state must be separate from source stores")
			}
		}
	}
	return nil
}
