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
	app := newTestApp(t)
	storeTestModel(t, app, service.URL, "")
	probeDone := make(chan modelCheck, 1)
	go func() {
		result, err := app.checkModel(context.Background(), "fixture", "fixture")
		if err != nil {
			t.Error(err)
		}
		probeDone <- result
	}()
	provider, err := protectedInstanceProvider(fixtureInstance(service.URL), "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	reply, err := provider.Chat(ctx, []providers.Message{{Role: "user", Content: "Synthetic cold-start check."}}, nil, "fixture", nil)
	if err != nil || reply == nil || reply.Content != "cold model ready" {
		t.Fatal("normal inference incorrectly inherited the short setup-probe timeout", err)
	}
	if result := <-probeDone; result.Status != "failed" {
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
	app := newTestApp(t)
	storeTestModel(t, app, service.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if result, err := app.checkModel(ctx, "fixture", "fixture"); err != nil || result.Status != "failed" {
		t.Fatal("cancelled setup probe reported success", err)
	}
	provider, err := protectedInstanceProvider(fixtureInstance(service.URL), "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	runtimeCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if _, err := provider.Chat(runtimeCtx, []providers.Message{{Role: "user", Content: "Wait."}}, nil, "fixture", nil); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatal("runtime did not respect cancellation", err)
	}
}
