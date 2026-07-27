// Package render formats results for a terminal.
package render

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

// ANSI colours, disabled when NO_COLOR is set or output is redirected.
var colourEnabled = detectColour()

func detectColour() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

const (
	reset   = "\033[0m"
	dim     = "\033[2m"
	bold    = "\033[1m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
)

func c(code, s string) string {
	if !colourEnabled {
		return s
	}
	return code + s + reset
}

// Dim renders secondary text.
func Dim(s string) string { return c(dim, s) }

// Bold renders emphasised text.
func Bold(s string) string { return c(bold, s) }

// ToolColour gives each tool a stable colour so a mixed list stays scannable.
func ToolColour(t core.Tool) string {
	switch t {
	case core.ToolCopilot:
		return c(cyan, string(t))
	case core.ToolClaude:
		return c(magenta, string(t))
	case core.ToolOpencode:
		return c(green, string(t))
	}
	return string(t)
}

// RiskColour renders a risk level with severity colouring.
func RiskColour(r core.Risk) string {
	switch r {
	case core.RiskCritical:
		return c(red, r.String())
	case core.RiskWarn:
		return c(yellow, r.String())
	case core.RiskWatch:
		return c(blue, r.String())
	}
	return c(dim, r.String())
}

// Bytes formats a byte count in binary units.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit && exp < 4; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// Age renders a duration as a compact relative time.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// Flags renders the badges that change what a user should do with a session.
func Flags(s core.Session) string {
	var out []string

	if s.Live != nil {
		status := s.Live.Status
		if status == "" {
			status = "open"
		}
		out = append(out, c(yellow, fmt.Sprintf("[LIVE pid %d %s]", s.Live.PID, status)))
	}
	if r := s.Risk(); r != core.RiskNone {
		out = append(out, c(red, fmt.Sprintf("[%s %s]", r, Bytes(s.Bytes))))
	}
	if d := s.SpanDays(); d >= 2 {
		out = append(out, c(dim, fmt.Sprintf("[%.0fd span]", d)))
	}
	if !s.DirExists() {
		out = append(out, c(red, "[dir missing]"))
	}
	if s.Noise {
		out = append(out, c(dim, "[auto]"))
	}
	if len(out) == 0 {
		return ""
	}
	return "  " + strings.Join(out, " ")
}

// Size renders the best available size signal for a session.
func Size(s core.Session) string {
	if s.Bytes > 0 {
		return Bytes(s.Bytes)
	}
	if s.Turns > 0 {
		return fmt.Sprintf("%d turns", s.Turns)
	}
	return ""
}

// Rule prints a horizontal separator.
func Rule(n int) string { return Dim(strings.Repeat("-", n)) }
