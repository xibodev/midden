package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xibodev/compa/pkg/config"
	"github.com/xibodev/compa/pkg/modelservice"
	"github.com/xibodev/compa/pkg/providers"
)

const chooseModelMessage = "Choose a model in Models before starting a conversation."

// kernelModelResolver resolves selections as the kernel does: saved catalogs,
// keys from the auth store, and providers whose errors never repeat a key.
func kernelModelResolver() *modelservice.Resolver {
	return modelservice.NewResolver(modelservice.WithProviderFactory(protectedInstanceProvider))
}

func protectedInstanceProvider(instance *config.ProviderInstanceConfig, modelID, secret string) (providers.LLMProvider, error) {
	provider, err := providers.CreateProviderFromInstance(instance, modelID, secret)
	if err != nil {
		return nil, err
	}
	return protectedProvider{LLMProvider: provider, secret: secret}, nil
}

// modelStatus is the stored default selection as the kernel will see it.
type modelStatus struct {
	Configured   bool   `json:"configured"`
	DefaultModel string `json:"defaultModel"`
	Summary      string `json:"summary"`
	SetupError   string `json:"setupError"`
}

// selectionStatus reports whether cfg's default selection resolves against
// its connections and the saved catalogs.
func selectionStatus(cfg *config.Config) modelStatus {
	selection := strings.TrimSpace(cfg.Agents.Defaults.ModelName)
	status := modelStatus{DefaultModel: selection}
	if selection == "" {
		return status
	}
	status.Summary = selectionSummary(cfg, selection)
	if err := kernelModelResolver().Check(cfg, selection); err != nil {
		status.SetupError = "The default model is unavailable: " + err.Error()
		return status
	}
	status.Configured = true
	return status
}

func selectionSummary(cfg *config.Config, selection string) string {
	if _, err := config.ParseExactModelTarget(selection); err == nil {
		return selection
	}
	for _, route := range cfg.ModelRoutes {
		if route != nil && route.Name == selection {
			return "Route " + route.Name + " · " + countModels(len(route.Targets))
		}
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
	return selectionStatus(cfg)
}

// configureKernelModel copies the stored model connections into the kernel's
// configuration and checks its default selection.
func (a *App) configureKernelModel(cfg *config.Config) (*modelservice.Resolver, error) {
	stored, err := a.loadModelConfig()
	if err != nil {
		return nil, err
	}
	cfg.ProviderInstances = stored.ProviderInstances
	cfg.ModelRoutes = stored.ModelRoutes
	cfg.ActiveModels = stored.ActiveModels
	cfg.Agents.Defaults.ModelName = strings.TrimSpace(stored.Agents.Defaults.ModelName)
	cfg.Extension = stored.Extension
	if cfg.Agents.Defaults.ModelName == "" {
		return nil, errors.New(chooseModelMessage)
	}
	resolver := kernelModelResolver()
	if err := resolver.Check(cfg, cfg.Agents.Defaults.ModelName); err != nil {
		return nil, fmt.Errorf("the default model is unavailable: %w", err)
	}
	return resolver, nil
}
