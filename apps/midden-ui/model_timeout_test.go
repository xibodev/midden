package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestModelChecksRemainCancellable(t *testing.T) {
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
	started := time.Now()
	if result, err := app.checkModel(ctx, "fixture", "fixture"); err != nil || result.Status != "failed" {
		t.Fatal("a cancelled check reported success", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("the check outlived its request by %s", elapsed)
	}
}
