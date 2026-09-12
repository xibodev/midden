package exec

import (
	"context"
	"strings"
	"testing"
)

func TestNativeDriver_Run(t *testing.T) {
	r := &Runner{
		NativeDriver: func(ctx context.Context, prompt string) (string, error) {
			return "processed: " + prompt, nil
		},
	}

	res, err := r.Run(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Backend != "native" {
		t.Errorf("expected backend 'native', got %q", res.Backend)
	}
	if res.Output != "processed: hello world" {
		t.Errorf("unexpected output: %q", res.Output)
	}
}

func TestNativeDriver_Conversation(t *testing.T) {
	r := &Runner{
		NativeDriver: func(ctx context.Context, prompt string) (string, error) {
			if strings.Contains(prompt, "prime") {
				return "ready", nil
			}
			return "answered: " + prompt, nil
		},
	}

	conv := r.NewConversation()
	primeRes, err := conv.Prime(context.Background(), "prime context")
	if err != nil {
		t.Fatalf("Prime error: %v", err)
	}
	if primeRes.Output != "ready" {
		t.Errorf("unexpected prime output: %q", primeRes.Output)
	}

	askRes, err := conv.Ask(context.Background(), "question 1")
	if err != nil {
		t.Fatalf("Ask error: %v", err)
	}
	if askRes.Output != "answered: question 1" {
		t.Errorf("unexpected ask output: %q", askRes.Output)
	}
	if conv.Turns() != 2 {
		t.Errorf("expected 2 turns, got %d", conv.Turns())
	}
}
