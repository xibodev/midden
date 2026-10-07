package main

import (
	"fmt"
)

const chooseModelMessage = "Choose a model in Models before starting a conversation."

// modelStatus is the stored default selection as the kernel will see it.
type modelStatus struct {
	Configured   bool   `json:"configured"`
	DefaultModel string `json:"defaultModel"`
	Summary      string `json:"summary"`
	SetupError   string `json:"setupError"`
}

// selectionStatus reports whether cfg's default selection resolves against
// its connections and the saved model lists in home.
func selectionStatus(home string, cfg *kernelConfig) modelStatus {
	selection := cfg.DefaultModel()
	status := modelStatus{DefaultModel: selection}
	if selection == "" {
		return status
	}
	status.Summary = selectionSummary(cfg, selection)
	catalogs, err := loadCatalogs(home)
	if err == nil {
		err = checkSelection(cfg, catalogs, selection)
	}
	if err != nil {
		status.SetupError = "The default model is unavailable: " + err.Error()
		return status
	}
	status.Configured = true
	return status
}

func selectionSummary(cfg *kernelConfig, selection string) string {
	if _, err := parseExactTarget(selection); err == nil {
		return selection
	}
	if index := cfg.routeIndex(selection); index >= 0 {
		return "Route " + selection + " · " + countModels(len(cfg.Routes[index].Targets))
	}
	return selection
}

func countModels(n int) string {
	if n == 1 {
		return "1 model"
	}
	return fmt.Sprintf("%d models", n)
}

// storedModelStatus reads the stored model configuration.
func (a *App) storedModelStatus() modelStatus {
	cfg, err := a.loadModelConfig()
	if err != nil {
		return modelStatus{SetupError: err.Error()}
	}
	return selectionStatus(a.paths.Kernel, cfg)
}
