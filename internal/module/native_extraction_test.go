package module

import (
	"context"
	"github.com/mekjr1/midden/internal/reclaim"
	"testing"
	"time"
)

func TestExtractionUsesNativeDriverWithoutExternalBinary(t *testing.T) {
	called := false
	out, sid, err := runExtraction(reclaim.Slice{}, ModelGrant{Timeout: time.Second, NativeDriver: func(ctx context.Context, prompt string) (string, error) { called = true; return "[]", ctx.Err() }})
	if err != nil || !called || sid != "" || out != "[]" {
		t.Fatalf("called=%v out=%q sid=%q err=%v", called, out, sid, err)
	}
}
