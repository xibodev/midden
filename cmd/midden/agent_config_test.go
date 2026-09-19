package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentMCPConfigBindsOnlyTheRequestedState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "new-state")
	var out bytes.Buffer
	if err := runAgent([]string{"mcp-config", "--home", home}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	var config struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(out.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	server, ok := config.Servers["midden"]
	if !ok || !filepath.IsAbs(server.Command) || strings.Join(server.Args, "|") != "mcp|--workflow|--home|"+home {
		t.Fatalf("wrong MCP binding: %s", out.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("configuration discovery mutated state")
	}
}
