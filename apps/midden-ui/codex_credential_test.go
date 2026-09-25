package main

import (
	"testing"
	"time"

	"github.com/xibodev/facet-studio/pkg/auth"
)

func TestNativeCodexAcceptsCanonicalOpenAIOAuthCredential(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := auth.SetCredential("synthetic-codex", &auth.AuthCredential{
		Provider: "openai", AuthMethod: "oauth", AccessToken: "synthetic-token",
		AccountID: "synthetic-account", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveModelCredential(Model{Provider: "openai-codex", CredentialRef: "synthetic-codex"}, "")
	if err != nil || resolved == nil || resolved.Provider != "openai" || resolved.Kind != "oauth" {
		t.Fatal("canonical native Codex OAuth reference was rejected", err)
	}
	if _, err := resolveModelCredential(Model{Provider: "anthropic", CredentialRef: "synthetic-codex"}, ""); err == nil {
		t.Fatal("cross-provider credential was accepted")
	}
	if err := auth.SetCredential("synthetic-api-key", &auth.AuthCredential{Provider: "openai", AuthMethod: "token", AccessToken: "synthetic-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveModelCredential(Model{Provider: "openai-codex", CredentialRef: "synthetic-api-key"}, ""); err == nil {
		t.Fatal("native Codex accepted an API key instead of its OAuth account credential")
	}
}
