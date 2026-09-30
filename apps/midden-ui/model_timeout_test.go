package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xibodev/compa/pkg/providers"
)

func TestRuntimeAllowsColdProviderHeadersBeyondSetupDeadline(t *testing.T) {
	probeCancelled := make(chan struct{})
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Tools []any }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		select {
		case <-time.After(26 * time.Second):
		case <-r.Context().Done():
			if len(body.Tools) != 0 {
				close(probeCancelled)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"cold model ready"},"finish_reason":"stop"}]}`))
	}))
	defer service.Close()
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	probeDone := make(chan error, 1)
	go func() {
		probeDone <- app.CheckModel(context.Background(), ModelInput{Provider: "openai", Model: "cold-fixture", Endpoint: service.URL})
	}()
	model := Model{Provider: "openai", Model: "cold-fixture", Endpoint: service.URL}
	provider, err := protectedInstanceProvider(modelInstance(model), model.Model, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	reply, err := provider.Chat(ctx, []providers.Message{{Role: "user", Content: "Synthetic cold-start check."}}, nil, model.Model, nil)
	if err != nil || reply == nil || reply.Content != "cold model ready" {
		t.Fatal("normal inference incorrectly inherited the short setup-probe timeout", err)
	}
	if err := <-probeDone; err == nil {
		t.Fatal("setup probe did not retain its bounded deadline")
	}
	select {
	case <-probeCancelled:
	default:
		t.Fatal("setup probe waited for headers beyond its deadline")
	}
}

func TestModelSetupAndRuntimeRequestsRemainCancellable(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		<-r.Context().Done()
	}))
	defer service.Close()
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	input := ModelInput{Provider: "openai", Model: "waiting-fixture", Endpoint: service.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := app.CheckModel(ctx, input); err == nil {
		t.Fatal("cancelled setup probe reported success")
	}
	provider, err := protectedInstanceProvider(modelInstance(Model{Provider: input.Provider, Model: input.Model, Endpoint: input.Endpoint}), input.Model, "")
	if err != nil {
		t.Fatal(err)
	}
	runtimeCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if _, err := provider.Chat(runtimeCtx, []providers.Message{{Role: "user", Content: "Wait."}}, nil, input.Model, nil); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatal("runtime did not respect cancellation", err)
	}
}
