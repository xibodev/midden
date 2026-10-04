package main

import (
	"fmt"
	"path/filepath"

	"github.com/xibodev/compa/pkg/config"
)

// modelConfigPath is the kernel configuration that stores model connections in
// Compa's own format, so the embedded kernel reads exactly what setup wrote.
func (a *App) modelConfigPath() string {
	return filepath.Join(a.opts.State, "kernel", "config.json")
}

// loadModelConfig reads the stored model configuration, or Compa's defaults
// when none has been saved yet. Callers must hold a.modelMu.
func (a *App) loadModelConfig() (*config.Config, error) {
	if config.GetHome() != filepath.Join(a.opts.State, "kernel") {
		return nil, fmt.Errorf("kernel state binding changed; run one workspace per UI process")
	}
	cfg, err := config.LoadConfig(a.modelConfigPath())
	if err != nil {
		return nil, fmt.Errorf("read model configuration: %w", err)
	}
	return cfg, nil
}

// updateModelConfig applies change to the stored model configuration and saves
// it under the model lock. A failed change saves nothing. Changes are refused
// while a turn runs; a saved change retires the kernel so the next turn uses
// the new connections.
func (a *App) updateModelConfig(change func(*config.Config) error) error {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	a.mu.Lock()
	busy := a.active != nil
	a.mu.Unlock()
	if busy {
		return fmt.Errorf("stop the active turn before changing model settings")
	}
	cfg, err := a.loadModelConfig()
	if err != nil {
		return err
	}
	if err := change(cfg); err != nil {
		return err
	}
	if err := config.SaveConfig(a.modelConfigPath(), cfg); err != nil {
		return fmt.Errorf("save model configuration: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runtime != nil {
		a.runtime.Close()
		a.runtime = nil
	}
	return nil
}
