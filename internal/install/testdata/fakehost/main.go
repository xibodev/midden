package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type state struct {
	ModuleID string `json:"module_id"`
	Binary   string `json:"binary"`
	Enabled  bool   `json:"enabled"`
	Adds     int    `json:"adds"`
}

type declaredContent struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type descriptor struct {
	Module        string            `json:"module"`
	AgentOverlays []declaredContent `json:"agent_overlays"`
	Skills        []declaredContent `json:"skills"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	home := os.Getenv("MIDDEN_FAKE_MODULE_HOST")
	statePath := filepath.Join(home, "host-state.json")
	load := func() (state, error) {
		var current state
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return current, err
		}
		return current, json.Unmarshal(raw, &current)
	}
	save := func(current state) error {
		raw, err := json.Marshal(current)
		if err != nil {
			return err
		}
		return os.WriteFile(statePath, raw, 0o600)
	}
	if len(args) == 0 {
		return fmt.Errorf("missing fake host command")
	}
	switch args[0] {
	case "modules-add":
		if len(args) != 2 {
			return fmt.Errorf("modules-add requires a binary")
		}
		descriptor, err := describeModule(args[1])
		if err != nil {
			return err
		}
		if descriptor.Module != "midden" {
			return fmt.Errorf("unexpected module id %q", descriptor.Module)
		}
		current, err := load()
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		destDir := filepath.Join(home, "modules", descriptor.Module)
		dest := filepath.Join(destDir, filepath.Base(args[1]))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := copyFile(args[1], dest); err != nil {
			return err
		}
		for _, content := range append(descriptor.AgentOverlays, descriptor.Skills...) {
			rel, err := safeRelativePath(content.Path)
			if err != nil {
				return fmt.Errorf("declared content %q: %w", content.ID, err)
			}
			src := filepath.Join(filepath.Dir(args[1]), rel)
			raw, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("declared content %q is not readable beside module binary: %w", content.ID, err)
			}
			if actual := digest(raw); actual != content.Digest {
				return fmt.Errorf("declared content %q digest is %s, want %s", content.ID, actual, content.Digest)
			}
			contentDest := filepath.Join(destDir, rel)
			if err := os.MkdirAll(filepath.Dir(contentDest), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(contentDest, raw, 0o644); err != nil {
				return err
			}
		}
		current.ModuleID = descriptor.Module
		current.Binary = dest
		current.Enabled = true
		current.Adds++
		if err := save(current); err != nil {
			return err
		}
		fmt.Println("installed module midden")
	case "modules-list":
		current, err := load()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(current)
	case "modules-disable":
		current, err := load()
		if err != nil {
			return err
		}
		current.Enabled = false
		return save(current)
	case "modules-remove":
		current, err := load()
		if err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(home, "modules", current.ModuleID)); err != nil {
			return err
		}
		return os.Remove(statePath)
	default:
		return fmt.Errorf("unknown fake host command %q", args[0])
	}
	return nil
}

func describeModule(binary string) (descriptor, error) {
	var descriptor descriptor
	cmd := exec.Command(binary, "module", "describe", "--json")
	cmd.Env = []string{}
	out, err := cmd.Output()
	if err != nil {
		return descriptor, fmt.Errorf("describe module: %w", err)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return descriptor, fmt.Errorf("decode module envelope: %w", err)
	}
	if !envelope.OK {
		return descriptor, fmt.Errorf("module describe returned ok=false")
	}
	if err := json.Unmarshal(envelope.Result, &descriptor); err != nil {
		return descriptor, fmt.Errorf("decode module descriptor: %w", err)
	}
	return descriptor, nil
}

func safeRelativePath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" || rel != strings.TrimSpace(rel) ||
		strings.Contains(rel, `\`) || strings.Contains(rel, ":") {
		return "", fmt.Errorf("path %q is not portable and relative", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if !filepath.IsLocal(clean) || clean == "." {
		return "", fmt.Errorf("path %q escapes the module root", rel)
	}
	return clean, nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
