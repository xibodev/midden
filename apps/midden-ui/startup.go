package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var version = "0.4.0-dev"

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

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// resolveLaunchPaths completes opts from the installed layout beside
// executable: midden, the skills folder, and in app/ the kernel and the App's
// own programs. dataRoot is the data folder when none is given.
func resolveLaunchPaths(opts Options, executable, dataRoot string) (Options, error) {
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return opts, fmt.Errorf("resolve installed executable: %w", err)
	}
	install := filepath.Dir(executable)
	if opts.Data == "" {
		if !filepath.IsAbs(dataRoot) {
			return opts, fmt.Errorf("default application data directory must be absolute")
		}
		opts.Data = dataRoot
	}
	if opts.Core == "" {
		opts.Core = filepath.Join(install, executableName("midden"))
	}
	if opts.Skills == "" {
		opts.Skills = filepath.Join(install, "skills")
	}
	if opts.Kernel == "" {
		if candidate := filepath.Join(install, "app", executableName("compa-kernel")); isFile(candidate) {
			opts.Kernel = candidate
		}
	} else if !isFile(opts.Kernel) {
		return opts, fmt.Errorf("compa-kernel not found at %s", opts.Kernel)
	}
	if tools := filepath.Join(install, "app", "tools"); isDir(tools) {
		opts.Tools = tools
	}
	opts.Install = install
	for _, path := range []*string{&opts.Data, &opts.Core, &opts.Skills, &opts.Kernel, &opts.Tools, &opts.Install} {
		if *path == "" {
			continue
		}
		if *path, err = filepath.Abs(*path); err != nil {
			return opts, err
		}
	}
	if resolved, err := filepath.EvalSymlinks(opts.Skills); err == nil {
		opts.Skills = resolved
	}
	return opts, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// errAlreadyRunning reports another midden-ui on the same data folder.
var errAlreadyRunning = errors.New("Midden is already running for this data folder")

// lockData holds the data folder for this process; a second midden-ui finds
// it held and opens the running App instead.
func lockData(root string) (func(), error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, "midden-ui.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	held, err := tryLockFile(file)
	if err != nil || !held {
		file.Close()
		if err == nil {
			err = errAlreadyRunning
		}
		return nil, err
	}
	return func() { _ = unlockFile(file); _ = file.Close() }, nil
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
