package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xibodev/compa/pkg/modelservice"
	"github.com/xibodev/compa/pkg/providers"
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
	secret := ""
	if err == nil {
		secret, err = resolveModelCredential(model, input.APIKey)
	}
	a.mu.Unlock()
	if err != nil {
		return modelCatalog{}, modelSetupError(err, input.Provider, input.APIKey)
	}
	catalogInput := modelservice.CatalogSyncInputFromInstance(modelInstance(model))
	catalogInput.Secret = secret
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	models, err := modelservice.SyncCatalog(ctx, catalogInput)
	if err != nil {
		return modelCatalog{}, modelSetupError(err, model.Provider, secret, catalogInput.Secret)
	}
	result := modelCatalog{Models: []availableModel{}, Note: "Catalog discovery does not verify inference or tool support. Select a model and use Check model."}
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
	secret := input.APIKey
	if err == nil && model.Model == "" {
		err = fmt.Errorf("select a model before testing the connection")
	}
	if err == nil {
		secret, err = resolveModelCredential(model, input.APIKey)
		if err == nil {
			provider, err = protectedInstanceProvider(modelInstance(model), model.Model, secret)
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
	if provider == "github-copilot" || provider == "openai-codex" {
		return errors.New(retiredNativeProviderMessage)
	}
	return errors.New(message)
}
