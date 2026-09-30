package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type WorkspaceFile struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Kind     string    `json:"kind"`
}
type FileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

func (a *App) path(name string) (string, error) {
	if name == "" || !filepath.IsLocal(filepath.FromSlash(name)) || strings.Contains(name, "\\") && filepath.Separator != '\\' {
		return "", fmt.Errorf("use a workspace-relative file path")
	}
	path := filepath.Join(a.opts.Workspace, filepath.FromSlash(name))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if containsPath(a.opts.State, resolved) {
		return "", fmt.Errorf("host state is not an artifact")
	}
	rel, err := filepath.Rel(a.opts.Workspace, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("file escapes workspace")
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".git" || part == ".midden" || part == ".midden-ui" || part == "sessions" {
			return "", fmt.Errorf("host state is not an artifact")
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	return resolved, nil
}
func (a *App) ReadFile(name string) (FileContent, error) {
	path, err := a.path(name)
	if err != nil {
		return FileContent{}, err
	}
	raw, err := readBounded(path, 1<<20)
	if err != nil {
		return FileContent{}, err
	}
	sum := sha256.Sum256(raw)
	return FileContent{filepath.ToSlash(name), string(raw), hex.EncodeToString(sum[:])}, nil
}
func (a *App) Files() ([]WorkspaceFile, error) {
	out := []WorkspaceFile{}
	err := filepath.WalkDir(a.opts.Workspace, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if containsPath(a.opts.State, path) {
				return filepath.SkipDir
			}
			if path != a.opts.Workspace && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "sessions" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(a.opts.Workspace, path)
		kind := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		out = append(out, WorkspaceFile{filepath.ToSlash(rel), info.Size(), info.ModTime(), kind})
		if len(out) > 2000 {
			return fmt.Errorf("workspace file list exceeds 2000 entries; use a focused workspace")
		}
		return nil
	})
	return out, err
}
