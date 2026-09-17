package create

import (
	"fmt"
	"strings"
)

// ValidateSlideSource checks the presentation contract before a model result
// becomes a successful draft. It does not substitute for editorial review.
func ValidateSlideSource(body string) error {
	slides, words, bullets := 0, 0, 0
	notes, fence := false, false
	check := func() error {
		if words > 100 {
			return fmt.Errorf("slide %d has %d words; split it into concise slides", slides, words)
		}
		if bullets > 4 {
			return fmt.Errorf("slide %d has %d bullets; maximum is four", slides, bullets)
		}
		return nil
	}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			fence = !fence
			continue
		}
		if fence {
			return fmt.Errorf("slides must not be enclosed in a code fence")
		}
		if line == "::: notes" {
			notes = true
			continue
		}
		if notes {
			if line == ":::" {
				notes = false
			}
			continue
		}
		if strings.HasPrefix(line, "--- #") {
			return fmt.Errorf("slide separator and heading must be on separate lines")
		}
		if strings.HasPrefix(line, "# ") {
			if err := check(); err != nil {
				return err
			}
			slides++
			words = 0
			bullets = 0
		}
		if slides > 0 && line != "---" {
			words += len(strings.Fields(line))
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			bullets++
		}
	}
	if notes {
		return fmt.Errorf("unclosed speaker notes block")
	}
	if slides < 8 || slides > 12 {
		return fmt.Errorf("expected 8–12 slide headings, found %d", slides)
	}
	return check()
}
