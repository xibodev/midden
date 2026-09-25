package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/xibodev/facet-studio/pkg/auth"
	"github.com/xibodev/facet-studio/pkg/config"
	"github.com/xibodev/facet-studio/pkg/modelservice"
	"github.com/xibodev/facet-studio/pkg/providers"
)

type ModelInput struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Endpoint      string `json:"endpoint"`
	APIKey        string `json:"apiKey"`
	CredentialRef string `json:"credentialRef"`
}

func (a *App) SetModel(input ModelInput) error {
	if strings.TrimSpace(input.Model) == "" {
		return fmt.Errorf("an explicit model id is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.applyModelLocked(input); err != nil {
		return err
	}
	a.authGeneration++
	a.login = nil
	return nil
}

func (a *App) applyModelLocked(input ModelInput) error {
	model, err := a.modelCandidateLocked(input)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input.APIKey) != "" {
		key := "midden-ui-" + model.Provider
		if err := auth.SetCredential(key, &auth.AuthCredential{Provider: model.Provider, AuthMethod: "token", AccessToken: strings.TrimSpace(input.APIKey)}); err != nil {
			return fmt.Errorf("save credential: %w", err)
		}
		model.CredentialRef = key
	}
	if err := writeJSON(filepath.Join(a.opts.State, "model.json"), model); err != nil {
		return err
	}
	if a.runtime != nil {
		a.runtime.Close()
		a.runtime = nil
	}
	a.model = model
	return nil
}

func (a *App) modelCandidateLocked(input ModelInput) (Model, error) {
	model := Model{Provider: strings.ReplaceAll(strings.TrimSpace(input.Provider), "_", "-"), Model: strings.TrimSpace(input.Model), Endpoint: strings.TrimSpace(input.Endpoint), CredentialRef: strings.TrimSpace(input.CredentialRef)}
	switch model.Provider {
	case "openai", "anthropic", "github-copilot", "openai-codex":
	default:
		return Model{}, fmt.Errorf("provider must be openai, anthropic, github-copilot or openai-codex")
	}
	if len(model.Model) > 200 {
		return Model{}, fmt.Errorf("model id is too long")
	}
	if model.Endpoint != "" {
		u, err := url.Parse(model.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return Model{}, fmt.Errorf("endpoint must be an HTTP(S) URL without credentials")
		}
	}
	if (model.Provider == "github-copilot" || model.Provider == "openai-codex") && model.Endpoint != "" {
		return Model{}, fmt.Errorf("native Copilot and Codex use authenticated endpoints; custom endpoints are not accepted")
	}
	if model.Provider == "openai-codex" && strings.TrimSpace(input.APIKey) != "" {
		return Model{}, fmt.Errorf("native Codex requires an OpenAI OAuth credential reference, not an API key")
	}
	if config.GetHome() != filepath.Join(a.opts.State, "kernel") {
		return Model{}, fmt.Errorf("kernel state binding changed; run one workspace per UI process")
	}
	if a.active != nil {
		return Model{}, fmt.Errorf("stop the active turn before changing or checking model settings")
	}
	if strings.TrimSpace(input.APIKey) == "" && model.CredentialRef == "" &&
		model.Provider == a.model.Provider && modelEndpoint(model) == modelEndpoint(a.model) {
		model.CredentialRef = a.model.CredentialRef
	}
	return model, nil
}

func modelEndpoint(model Model) string {
	if model.Endpoint != "" {
		return strings.TrimRight(model.Endpoint, "/")
	}
	switch model.Provider {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	default:
		return ""
	}
}

func (a *App) providerConfig() (providers.LLMProvider, *config.ModelConfig, error) {
	if config.GetHome() != filepath.Join(a.opts.State, "kernel") {
		return nil, nil, fmt.Errorf("kernel state binding changed")
	}
	return providerForModel(a.model, "")
}

func providerForModel(m Model, override string) (providers.LLMProvider, *config.ModelConfig, error) {
	cfg := &config.ModelConfig{ModelName: "midden-selected", Provider: m.Provider, Model: m.Model, APIBase: m.Endpoint, Streaming: config.ModelStreamingConfig{Enabled: true}}
	connection, err := resolveModelCredential(m, override)
	if err != nil {
		return nil, nil, err
	}
	if connection != nil {
		cfg.SetAPIKey(connection.AccessToken)
	}
	if m.Provider == "github-copilot" {
		return providers.NewGitHubCopilotHTTPProvider(cfg.APIKey(), "", m.Model), cfg, nil
	}
	if m.Provider == "openai-codex" {
		if connection == nil {
			return nil, nil, fmt.Errorf("Codex requires an explicit credential reference in the isolated kernel auth store")
		}
		provider, err := providers.CreateProviderFromInstance(&config.ProviderInstanceConfig{ID: "midden-selected", Adapter: "openai-codex-native", Protocol: "openai", Endpoint: m.Endpoint}, m.Model, connection)
		cfg.Provider = "openai"
		return provider, cfg, err
	}
	provider, _, err := providers.CreateProviderFromConfig(cfg)
	return provider, cfg, err
}

func resolveModelCredential(m Model, override string) (*providers.ResolvedAuthConnection, error) {
	if key := strings.TrimSpace(override); key != "" {
		return &providers.ResolvedAuthConnection{Provider: m.Provider, Kind: "token", AccessToken: key}, nil
	}
	if m.CredentialRef != "" {
		var resolve func() (*providers.ResolvedAuthConnection, error)
		resolve = func() (*providers.ResolvedAuthConnection, error) {
			credential, err := auth.GetCredentialWithRefresh(m.CredentialRef)
			if err != nil {
				return nil, err
			}
			if credential == nil {
				return nil, fmt.Errorf("selected credential is unavailable")
			}
			provider := strings.ReplaceAll(strings.TrimSpace(credential.Provider), "_", "-")
			if m.Provider == "openai-codex" {
				if provider != "openai" || credential.AuthMethod != "oauth" {
					return nil, fmt.Errorf("native Codex requires an OpenAI OAuth account credential")
				}
			} else if provider != m.Provider {
				return nil, fmt.Errorf("selected credential belongs to another provider")
			}
			return &providers.ResolvedAuthConnection{Provider: credential.Provider, Kind: credential.AuthMethod, AccessToken: credential.AccessToken, RefreshToken: credential.RefreshToken, AccountID: credential.AccountID, ProjectID: credential.ProjectID, ExpiresAt: credential.ExpiresAt, Refresh: resolve}, nil
		}
		return resolve()
	}
	return nil, nil
}

func providerRoster() any { return modelservice.ListRoster(&config.Config{}) }
