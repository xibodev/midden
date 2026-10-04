package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xibodev/compa/pkg/config"
)

// errModelBusy refuses model changes while a turn runs.
var errModelBusy = errors.New("stop the active turn before changing model settings")

// errNoModelChange ends a change that found nothing to save.
var errNoModelChange = errors.New("no model change")

// modelConfigPath is the kernel configuration that stores model connections in
// Compa's own format, so the embedded kernel reads exactly what setup wrote.
func (a *App) modelConfigPath() string {
	return filepath.Join(a.opts.State, "kernel", "config.json")
}

// loadModelConfig reads the stored model configuration, or Compa's defaults
// when none has been saved yet. Saves replace files atomically, so readers
// need no lock; a.modelMu serializes changes.
func (a *App) loadModelConfig() (*config.Config, error) {
	if config.GetHome() != filepath.Join(a.opts.State, "kernel") {
		return nil, fmt.Errorf("kernel state binding changed; run one workspace per UI process")
	}
	path := a.modelConfigPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return config.DefaultConfig(), nil
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return nil, fmt.Errorf("read model configuration: %w", err)
	}
	return cfg, nil
}

// modelUndo collects the steps that reverse a change's effects outside
// config.json, such as stored keys, catalogs and check results.
type modelUndo []func()

func (u *modelUndo) add(step func()) { *u = append(*u, step) }

func (u modelUndo) run() {
	for i := len(u) - 1; i >= 0; i-- {
		u[i]()
	}
}

// updateModelConfig applies change to the stored model configuration and saves
// it under the model lock. A failed change saves nothing. Changes are refused
// while a turn runs; a saved change retires the kernel so the next turn uses
// the new connections.
func (a *App) updateModelConfig(change func(*config.Config) error) error {
	return a.changeModelConfig(func(cfg *config.Config, _ *modelUndo) error { return change(cfg) })
}

// changeModelConfig is updateModelConfig for changes with effects outside
// config.json: their undo steps run, still under the lock, when the change
// fails or cannot be saved. A change returning errNoModelChange saves nothing
// and keeps the kernel. Turns cannot start while a change runs.
func (a *App) changeModelConfig(change func(*config.Config, *modelUndo) error) error {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return errModelBusy
	}
	a.modelChanging = true
	a.mu.Unlock()
	saved := false
	defer func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.modelChanging = false
		if saved && a.runtime != nil {
			a.runtime.Close()
			a.runtime = nil
		}
	}()
	cfg, err := a.loadModelConfig()
	if err != nil {
		return err
	}
	var undo modelUndo
	err = change(cfg, &undo)
	if errors.Is(err, errNoModelChange) {
		return nil
	}
	if err == nil {
		if err = config.SaveConfig(a.modelConfigPath(), cfg); err != nil {
			err = fmt.Errorf("save model configuration: %w", err)
		}
	}
	if err != nil {
		undo.run()
		return err
	}
	saved = true
	return nil
}

// modelCheck is the stored result of one tool-call check.
type modelCheck struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	At      string `json:"at"`
}

// modelChecks holds check results by instance and model.
type modelChecks map[string]map[string]modelCheck

func (a *App) modelChecksPath() string {
	return filepath.Join(a.opts.State, "model-checks.json")
}

// loadModelChecks reads the stored check results. They are advisory, so an
// unreadable file counts as no results.
func (a *App) loadModelChecks() modelChecks {
	raw, err := readBounded(a.modelChecksPath(), 4<<20)
	if err != nil {
		return modelChecks{}
	}
	checks := modelChecks{}
	if err := json.Unmarshal(raw, &checks); err != nil || checks == nil {
		return modelChecks{}
	}
	return checks
}

// saveModelChecks replaces the stored check results. Callers hold a.modelMu.
func (a *App) saveModelChecks(checks modelChecks) error {
	return writeJSON(a.modelChecksPath(), checks)
}

// forgetModelChecks drops an instance's check results as part of a change.
func (a *App) forgetModelChecks(id string, undo *modelUndo) error {
	checks := a.loadModelChecks()
	previous, ok := checks[id]
	if !ok {
		return nil
	}
	delete(checks, id)
	if err := a.saveModelChecks(checks); err != nil {
		return fmt.Errorf("save model checks: %w", err)
	}
	undo.add(func() {
		checks := a.loadModelChecks()
		checks[id] = previous
		_ = a.saveModelChecks(checks)
	})
	return nil
}
