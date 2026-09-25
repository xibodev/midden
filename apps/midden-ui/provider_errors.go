package main

import (
	"context"
	"strings"

	"github.com/xibodev/facet-studio/pkg/providers"
)

type protectedProvider struct {
	providers.LLMProvider
	secret string
}

type protectedError struct {
	cause error
	text  string
}

func (e protectedError) Error() string { return e.text }
func (e protectedError) Unwrap() error { return e.cause }

func (p protectedProvider) protect(err error) error {
	if err == nil || p.secret == "" || !strings.Contains(err.Error(), p.secret) {
		return err
	}
	return protectedError{cause: err, text: strings.ReplaceAll(err.Error(), p.secret, "[redacted]")}
}

func (p protectedProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	response, err := p.LLMProvider.Chat(ctx, messages, tools, model, options)
	return response, p.protect(err)
}

func (p protectedProvider) ChatStream(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]any, onChunk func(string)) (*providers.LLMResponse, error) {
	if streaming, ok := p.LLMProvider.(providers.StreamingProvider); ok {
		response, err := streaming.ChatStream(ctx, messages, tools, model, options, onChunk)
		return response, p.protect(err)
	}
	response, err := p.Chat(ctx, messages, tools, model, options)
	if err == nil && response != nil {
		onChunk(response.Content)
	}
	return response, err
}

func (p protectedProvider) ChatStreamEvents(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]any, onChunk func(providers.StreamChunk)) (*providers.LLMResponse, error) {
	if streaming, ok := p.LLMProvider.(providers.StreamingEventProvider); ok {
		response, err := streaming.ChatStreamEvents(ctx, messages, tools, model, options, onChunk)
		return response, p.protect(err)
	}
	return p.ChatStream(ctx, messages, tools, model, options, func(text string) {
		onChunk(providers.StreamChunk{Content: text})
	})
}

func (p protectedProvider) SupportsThinking() bool {
	capable, ok := p.LLMProvider.(providers.ThinkingCapable)
	return ok && capable.SupportsThinking()
}

func (p protectedProvider) SupportsNativeSearch() bool {
	capable, ok := p.LLMProvider.(providers.NativeSearchCapable)
	return ok && capable.SupportsNativeSearch()
}

func (p protectedProvider) Close() {
	if stateful, ok := p.LLMProvider.(providers.StatefulProvider); ok {
		stateful.Close()
	}
}
