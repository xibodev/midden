package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var version = "0.3.0-dev"

func applicationDataRoot(platform, home, local, xdg string) (string, error) {
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("a valid user home directory is required")
	}
	var root string
	switch platform {
	case "windows":
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		root = filepath.Join(local, "Midden")
	case "darwin":
		root = filepath.Join(home, "Library", "Application Support", "Midden")
	default:
		if xdg == "" {
			xdg = filepath.Join(home, ".local", "share")
		}
		root = filepath.Join(xdg, "midden")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("application data directory must be absolute")
	}
	return root, nil
}

func resolveLaunchPaths(opts Options, executable, dataRoot string) (Options, bool, error) {
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return opts, false, fmt.Errorf("resolve installed executable: %w", err)
	}
	createWorkspace := opts.Workspace == ""
	if createWorkspace {
		if !filepath.IsAbs(dataRoot) {
			return opts, false, fmt.Errorf("default application data directory must be absolute")
		}
		opts.Workspace = filepath.Join(dataRoot, "workspace")
		if opts.State == "" {
			opts.State = filepath.Join(dataRoot, "host-state")
		}
	} else if opts.State == "" {
		return opts, false, fmt.Errorf("--workspace must be used together with --state")
	}
	if opts.Core == "" {
		name := "midden"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		opts.Core = filepath.Join(filepath.Dir(executable), name)
	}
	if opts.Bundle == "" {
		opts.Bundle = filepath.Join(filepath.Dir(executable), "bundles")
	}
	for _, path := range []*string{&opts.Workspace, &opts.State, &opts.Core, &opts.Bundle} {
		*path, err = filepath.Abs(*path)
		if err != nil {
			return opts, false, err
		}
	}
	return opts, createWorkspace, nil
}

func openBrowser(address string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", address)
	case "darwin":
		command = exec.CommandContext(ctx, "open", address)
	default:
		command = exec.CommandContext(ctx, "xdg-open", address)
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("open %s manually; browser launch failed: %w", address, err)
	}
	return nil
}
