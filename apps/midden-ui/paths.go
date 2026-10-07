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

// writePath checks a new or replaced output path: inside the person's files,
// and not in a hidden folder such as .git.
func (a *App) writePath(name string) error {
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.paths.Files, path)
	}
	resolved, err := resolvedDestination(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(a.paths.Files, resolved)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return fmt.Errorf("output path must be inside your files")
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("output may not go into a hidden folder such as .git")
		}
	}
	return nil
}

// protectSourceStores keeps the App's data apart from the session records
// Core reads.
func protectSourceStores(opts Options) error {
	roots := opts.SourceEnv
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		roots = map[string]string{"MIDDEN_COPILOT_ROOT": filepath.Join(home, ".copilot"), "MIDDEN_CLAUDE_ROOT": filepath.Join(home, ".claude"), "MIDDEN_OPENCODE_DB": filepath.Join(home, ".local", "share", "opencode", "opencode.db")}
	}
	data, err := resolvedDestination(opts.Data)
	if err != nil {
		return err
	}
	for _, source := range roots {
		if strings.TrimSpace(source) == "" {
			continue
		}
		resolved, err := resolvedDestination(source)
		if err != nil {
			return err
		}
		if containsPath(resolved, data) || containsPath(data, resolved) {
			return fmt.Errorf("the App's data folder must be separate from session records")
		}
	}
	return nil
}
