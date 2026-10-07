package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// appPaths are the App's folders under its data root.
type appPaths struct {
	Data      string // the root, kept on update
	App       string // midden-ui's own state: conversations, model checks, launch address
	Kernel    string // the kernel's home, COMPA_HOME
	Workspace string // the kernel's workspace: AGENT.md, its history and memory
	Files     string // the person's files, in the workspace
}

func dataPaths(root string) appPaths {
	workspace := filepath.Join(root, "workspace")
	return appPaths{Data: root, App: filepath.Join(root, "app"), Kernel: filepath.Join(root, "kernel"),
		Workspace: workspace, Files: filepath.Join(workspace, "files")}
}

// layoutMarker records that the data root has this layout.
const (
	layoutMarker  = "layout"
	layoutVersion = "midden-app-data 2\n"
)

// prepareData creates the App's folders and, once, moves an earlier App's
// data into them. It returns a notice for the person, if any.
func prepareData(root string) (appPaths, string, error) {
	paths := dataPaths(root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return paths, "", err
	}
	marker := filepath.Join(paths.App, layoutMarker)
	notice := ""
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		if notice, err = migrateData(paths); err != nil {
			return paths, "", fmt.Errorf("move the earlier App's data: %w", err)
		}
	} else if err != nil {
		return paths, "", err
	}
	if err := finishFilesMove(paths); err != nil {
		return paths, "", fmt.Errorf("move the earlier App's files: %w", err)
	}
	for _, dir := range []string{paths.App, paths.Kernel, paths.Workspace, paths.Files} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return paths, "", err
		}
	}
	if err := replaceFile(marker, []byte(layoutVersion)); err != nil {
		return paths, "", err
	}
	return paths, notice, nil
}

// migrateData moves the data of the App before 0.4, which kept the person's
// files in workspace and its own state in host-state: the files go to
// workspace/files, conversations and model checks to app, and model
// connections to the kernel's home. host-state stays as it was.
//
// The files move in two renames: the earlier workspace is set aside as
// workspace.moving, which a record in app marks as done, and then becomes
// the files folder. A start that finds workspace.moving finishes the move,
// so a move cut short is never repeated or lost.
func migrateData(paths appPaths) (string, error) {
	old := filepath.Join(paths.Data, "host-state")
	if _, err := os.Stat(old); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err := os.MkdirAll(paths.App, 0o700); err != nil {
		return "", err
	}
	setAside := filepath.Join(paths.App, "files-set-aside")
	if _, err := os.Stat(setAside); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(movingFiles(paths)); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(paths.Workspace); err == nil {
				if err := os.Rename(paths.Workspace, movingFiles(paths)); err != nil {
					return "", err
				}
			}
		}
		if err := replaceFile(setAside, []byte("the earlier workspace was set aside as workspace.moving\n")); err != nil {
			return "", err
		}
	}
	if err := finishFilesMove(paths); err != nil {
		return "", err
	}
	for _, name := range []string{"sessions.json", "model-checks.json"} {
		if err := copyMissing(filepath.Join(old, name), filepath.Join(paths.App, name)); err != nil {
			return "", err
		}
	}
	if err := migrateModels(filepath.Join(old, "kernel"), paths.Kernel); err != nil {
		return "Midden could not carry over your model connections (" + err.Error() + "). Connect your models again in Models.", nil
	}
	return "", nil
}

func movingFiles(paths appPaths) string { return filepath.Join(paths.Data, "workspace.moving") }

// finishFilesMove makes the earlier workspace, set aside, the files folder.
func finishFilesMove(paths appPaths) error {
	moving := movingFiles(paths)
	if _, err := os.Stat(moving); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.MkdirAll(paths.Workspace, 0o700); err != nil {
		return err
	}
	if entries, err := os.ReadDir(paths.Files); err == nil && len(entries) == 0 {
		if err := os.Remove(paths.Files); err != nil {
			return err
		}
	}
	if err := os.Rename(moving, paths.Files); err != nil {
		return fmt.Errorf("%w; your earlier files are in %s", err, moving)
	}
	return nil
}

// migrateModels carries the earlier App's model connections into the
// kernel's home: its keys, its model lists, and the model settings of its
// config.json, which held other settings Compa no longer reads.
func migrateModels(oldHome, home string) error {
	if _, err := os.Stat(kernelConfigPath(home)); err == nil {
		return nil
	}
	previous, err := loadKernelConfig(oldHome)
	if err != nil || previous.original == nil {
		return err
	}
	cfg, _ := parseKernelConfig(nil)
	cfg.Instances, cfg.Routes, cfg.ActiveModels, cfg.Extension = previous.Instances, previous.Routes, previous.ActiveModels, previous.Extension
	if selection := previous.DefaultModel(); selection != "" {
		cfg.SetDefaultModel(selection)
	}
	if err := validateModelSettings(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"auth.json", "model_catalogs.json"} {
		if err := copyMissing(filepath.Join(oldHome, name), filepath.Join(home, name)); err != nil {
			return err
		}
	}
	return saveKernelConfig(home, cfg)
}

// copyMissing copies from to to, unless from is absent or to exists.
func copyMissing(from, to string) error {
	if _, err := os.Stat(to); err == nil {
		return nil
	}
	source, err := os.Open(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer source.Close()
	data, err := io.ReadAll(io.LimitReader(source, 64<<20))
	if err != nil {
		return err
	}
	return replaceFile(to, data)
}

// agentInstructions is the kernel's AGENT.md: the agent's name and
// description, and how it keeps the person's files. It is rewritten at each
// start, so an update takes effect at once.
const agentInstructions = `---
name: Midden
description: Midden's assistant. It investigates recorded AI sessions with the midden command and writes findings, articles, presentations and long-form pieces from them.
---
You are Midden, the assistant of the Midden App, powered by Compa.

The person's files are in the files folder of your workspace. Everything you make for them goes there, for example files/notes/summary.md, and so does anything they ask you to read or revise. Run midden from that folder (cd files), so what it writes lands there. The rest of the workspace is yours: AGENT.md, sessions, memory and state are not the person's files.

midden reads the person's recorded AI sessions from Copilot CLI, Claude Code and OpenCode, and never changes them. Use its --json output when you read results.

Midden's skills are installed for you. Before you start a request, read the skill that fits it and follow it.
`

// writeAgentInstructions writes the kernel's AGENT.md.
func writeAgentInstructions(workspace string) error {
	path := filepath.Join(workspace, "AGENT.md")
	if current, err := os.ReadFile(path); err == nil && string(current) == agentInstructions {
		return nil
	}
	return replaceFile(path, []byte(strings.ReplaceAll(agentInstructions, "\r\n", "\n")))
}
