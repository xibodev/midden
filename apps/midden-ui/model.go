package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/xibodev/compa/pkg/auth"
	"github.com/xibodev/compa/pkg/config"
	"github.com/xibodev/compa/pkg/modelservice"
	"github.com/xibodev/compa/pkg/providers"
)

const retiredNativeProviderMessage = "This connection uses a retired native provider. Reconnect using an API-key or local compatible provider. Copilot/Codex requires a separately verified extension provider, which this host does not include."

type ModelInput struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Endpoint      string `json:"endpoint"`
	APIKey        string `json:"apiKey"`
	CredentialRef string `json:"credentialRef"`
}

func modelProviderError(provider string) error {
	switch strings.ReplaceAll(strings.TrimSpace(provider), "_", "-") {
	case "openai", "anthropic":
		return nil
	case "github-copilot", "openai-codex":
		return errors.New(retiredNativeProviderMessage)
	default:
		return fmt.Errorf("choose an OpenAI-compatible or Anthropic-compatible provider")
	}
}

func (a *App) SetModel(input ModelInput) error {
	if strings.TrimSpace(input.Model) == "" {
		return fmt.Errorf("an explicit model id is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	model, err := a.modelCandidateLocked(input)
	if err != nil {
		return err
	}
	newKey := ""
	if key := strings.TrimSpace(input.APIKey); key != "" {
		newKey = "midden-ui-" + model.Provider + "-" + randomID()
		if err := auth.SetCredential(newKey, &auth.AuthCredential{Provider: model.Provider, AuthMethod: modelservice.APIKeyAuthMethod, AccessToken: key}); err != nil {
			return fmt.Errorf("save credential: %w", err)
		}
		model.CredentialRef = newKey
	}
	if err := writeJSON(filepath.Join(a.opts.State, "model.json"), model); err != nil {
		if newKey != "" {
			if cleanupErr := auth.DeleteCredential(newKey); cleanupErr != nil {
				return errors.Join(err, fmt.Errorf("remove uncommitted credential: %w", cleanupErr))
			}
		}
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
	if err := modelProviderError(model.Provider); err != nil {
		return Model{}, err
	}
	if len(model.Model) > 200 {
		return Model{}, fmt.Errorf("model id is too long")
	}
	if model.Endpoint != "" {
		u, err := url.Parse(model.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Model{}, fmt.Errorf("endpoint must be an HTTP(S) URL without credentials, query or fragment")
		}
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
	if model.Provider == "anthropic" {
		return "https://api.anthropic.com/v1"
	}
	return "https://api.openai.com/v1"
}

func modelInstance(model Model) *config.ProviderInstanceConfig {
	endpoint := modelEndpoint(model)
	digest := sha256.Sum256([]byte(model.Provider + "\x00" + endpoint))
	instance := &config.ProviderInstanceConfig{
		ID:           "midden-" + model.Provider + "-" + hex.EncodeToString(digest[:6]),
		ProviderKind: "openai-compatible", Adapter: config.ProviderAdapterOpenAICompatible,
		Protocol: "openai", Endpoint: endpoint, State: config.ProviderInstanceStateEnabled,
	}
	if model.Provider == "anthropic" {
		instance.ProviderKind = "anthropic"
		instance.Adapter = config.ProviderAdapterAnthropicCompatible
		instance.Protocol = "anthropic-messages"
	} else if endpoint == "https://api.openai.com/v1" {
		instance.ProviderKind = "openai"
	}
	if model.CredentialRef != "" {
		instance.AuthConnectionRef = "credential:" + strings.TrimPrefix(model.CredentialRef, "credential:")
	}
	return instance
}

func resolveModelCredential(model Model, override string) (string, error) {
	if err := modelProviderError(model.Provider); err != nil {
		return "", err
	}
	if key := strings.TrimSpace(override); key != "" {
		return key, nil
	}
	if model.CredentialRef == "" {
		return "", nil
	}
	key := strings.TrimPrefix(model.CredentialRef, "credential:")
	if key == "" || strings.ContainsAny(key, ":/\\") {
		return "", fmt.Errorf("credential reference must be a local key or credential:<key>")
	}
	credential, err := auth.GetCredential(key)
	if err != nil {
		return "", err
	}
	if credential == nil || strings.TrimSpace(credential.AccessToken) == "" {
		return "", fmt.Errorf("selected credential is unavailable; reconnect this provider")
	}
	if credential.Provider != model.Provider || (credential.AuthMethod != "token" && credential.AuthMethod != modelservice.APIKeyAuthMethod) {
		return "", fmt.Errorf("selected credential is not an API key for this provider; reconnect it")
	}
	if credential.IsExpired() {
		return "", fmt.Errorf("selected credential is expired; reconnect this provider")
	}
	return strings.TrimSpace(credential.AccessToken), nil
}

func protectedInstanceProvider(instance *config.ProviderInstanceConfig, modelID, secret string) (providers.LLMProvider, error) {
	provider, err := providers.CreateProviderFromInstance(instance, modelID, secret)
	if err != nil {
		return nil, err
	}
	return protectedProvider{LLMProvider: provider, secret: secret}, nil
}

func (a *App) configureKernelModel(cfg *config.Config) (*modelservice.Resolver, error) {
	if config.GetHome() != filepath.Join(a.opts.State, "kernel") {
		return nil, fmt.Errorf("kernel state binding changed")
	}
	model := a.model
	if err := modelProviderError(model.Provider); err != nil {
		return nil, err
	}
	instance := modelInstance(model)
	cfg.ProviderInstances = []*config.ProviderInstanceConfig{instance}
	cfg.Agents.Defaults.ModelName = instance.ID + "/" + model.Model
	// This catalog records the operator's explicit selection, not verified upstream availability.
	if err := modelservice.SaveProviderInstanceCatalog(instance, []modelservice.CatalogModel{{ID: model.Model}}); err != nil {
		return nil, fmt.Errorf("save selected model catalog: %w", err)
	}
	resolver := modelservice.NewResolver(
		modelservice.WithCredentialResolver(func(ref string) (string, error) {
			if ref != instance.AuthConnectionRef {
				return "", fmt.Errorf("credential reference does not belong to the selected connection")
			}
			return resolveModelCredential(model, "")
		}),
		modelservice.WithProviderFactory(protectedInstanceProvider),
	)
	if err := resolver.Check(cfg, cfg.Agents.Defaults.ModelName); err != nil {
		return nil, err
	}
	return resolver, nil
}

func providerRoster() any {
	var supported []modelservice.ProviderRosterItem
	for _, item := range modelservice.ListRoster(&config.Config{}) {
		if item.ID == "openai" || item.ID == "anthropic" {
			supported = append(supported, item)
		}
	}
	return supported
}
