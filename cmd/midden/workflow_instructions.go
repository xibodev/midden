package main

import (
	"strings"

	"github.com/mekjr1/midden/internal/module"
)

func workflowInstructions() string {
	raw, ok := module.OverlayContent("skills/editorial-production/SKILL.md")
	if !ok {
		panic("embedded editorial workflow skill is missing")
	}
	body := string(raw)
	if parts := strings.SplitN(body, "---", 3); len(parts) == 3 {
		body = parts[2]
	}
	return "Midden workflow connection: apply the following product-owned process. This is guidance, not source-session content.\n\n" + strings.TrimSpace(body)
}
