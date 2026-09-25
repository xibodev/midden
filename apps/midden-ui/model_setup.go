package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/xibodev/facet-studio/pkg/config"
	"github.com/xibodev/facet-studio/pkg/modelservice"
	"github.com/xibodev/facet-studio/pkg/providers"
	copilotauth "github.com/xibodev/llm-provider-auth/copilot"
)

type availableModel struct {
	ID string `json:"id"`
}

type modelCatalog struct {
	Models []availableModel `json:"models"`
	Note   string           `json:"note"`
}

func (a *App) DiscoverModels(ctx context.Context, input ModelInput) (modelCatalog, error) {
	a.mu.Lock()
	model, err := a.modelCandidateLocked(input)
	var connection *providers.ResolvedAuthConnection
	if err == nil {
		connection, err = resolveModelCredential(model, input.APIKey)
	}
	a.mu.Unlock()
	if err != nil {
		return modelCatalog{}, modelSetupError(err, input.Provider, input.APIKey)
	}
	secret := ""
	if connection != nil {
		secret = connection.AccessToken
	}
	catalogInput := modelservice.ProviderCatalogSyncInput{
		Adapter: "openai-compatible", Endpoint: modelEndpoint(model), Secret: secret,
	}
	switch model.Provider {
	case "anthropic":
		catalogInput.Adapter = "anthropic-compatible"
	case "github-copilot":
		if secret == "" {
			secret, err = copilotauth.ResolveOAuthToken()
		}
		if err != nil {
			return modelCatalog{}, modelSetupError(err, model.Provider, secret)
		}
		session, err := copilotauth.GetSessionForOAuth(secret, false)
		if err != nil {
			return modelCatalog{}, modelSetupError(err, model.Provider, secret)
		}
		catalogInput.Endpoint = session.ChatBaseURL
		catalogInput.Secret = session.Token
		catalogInput.Headers = map[string]string{
			"Editor-Version":         copilotauth.EditorVersion,
			"Editor-Plugin-Version":  copilotauth.EditorPluginVersion,
			"Copilot-Integration-Id": "vscode-chat",
		}
	case "openai-codex":
		return modelCatalog{}, fmt.Errorf("native Codex does not expose a catalog through this host; enter an exact model id and use Test connection")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	models, err := modelservice.SyncCompatibleCatalog(ctx, catalogInput, &http.Client{Timeout: 20 * time.Second})
	if err != nil {
		return modelCatalog{}, modelSetupError(err, model.Provider, secret, catalogInput.Secret)
	}
	result := modelCatalog{Models: []availableModel{}, Note: "Catalog discovery does not verify inference or tool support. Select a model and use Test connection."}
	for _, model := range models {
		if id := strings.TrimSpace(model.ID); id != "" {
			result.Models = append(result.Models, availableModel{ID: id})
		}
	}
	if len(result.Models) == 0 {
		return modelCatalog{}, fmt.Errorf("the provider returned no model identifiers; check this account and endpoint")
	}
	return result, nil
}

func (a *App) CheckModel(ctx context.Context, input ModelInput) error {
	a.mu.Lock()
	model, err := a.modelCandidateLocked(input)
	var provider providers.LLMProvider
	var providerConfig *config.ModelConfig
	secret := input.APIKey
	if err == nil && model.Model == "" {
		err = fmt.Errorf("select a model before testing the connection")
	}
	if err == nil {
		provider, providerConfig, err = providerForModel(model, input.APIKey)
		if providerConfig != nil {
			secret = providerConfig.APIKey()
		}
	}
	a.mu.Unlock()
	if err != nil {
		return modelSetupError(err, input.Provider, input.APIKey)
	}
	if closer, ok := provider.(providers.StatefulProvider); ok {
		defer closer.Close()
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	response, err := provider.Chat(ctx,
		[]providers.Message{{Role: "user", Content: "This is a connection check with no user files. Call midden_connection_check with ok=true. Do not answer with prose."}},
		[]providers.ToolDefinition{{Type: "function", Function: providers.ToolFunctionDefinition{
			Name: "midden_connection_check", Description: "An inert tool-capability probe. No command or file operation is executed.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false},
		}}}, model.Model, map[string]any{"max_tokens": 128})
	if err != nil {
		return modelSetupError(err, model.Provider, input.APIKey, secret)
	}
	if response != nil {
		for _, call := range response.ToolCalls {
			name, arguments := call.Name, call.Arguments
			if call.Function != nil {
				name = call.Function.Name
				if arguments == nil {
					if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
						return fmt.Errorf("the model returned malformed probe arguments: %w", err)
					}
				}
			}
			if name == "midden_connection_check" && arguments["ok"] == true {
				return nil
			}
		}
	}
	return fmt.Errorf("the service responded but did not return the required tool call; this model/connection is not verified for Midden bundle execution")
}

func modelSetupError(err error, provider string, secrets ...string) error {
	message := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	if provider == "github-copilot" {
		var authErr *copilotauth.AuthError
		if errors.As(err, &authErr) || strings.Contains(strings.ToLower(message), "copilot") {
			return fmt.Errorf("GitHub Copilot connection unavailable. Use Settings > Sign in with GitHub, then select a model and Test connection. A GitHub CLI login is not a Copilot authorization; if sign-in is still rejected, check the account and organization access. Details: %s", message)
		}
	}
	return errors.New(message)
}
