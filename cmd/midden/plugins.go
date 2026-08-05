package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/mekjr1/midden/internal/plugins"
	"github.com/mekjr1/midden/internal/render"
)

// cmdPlugins exposes configured integrations without executing them. A plugin
// is first a capability declaration and availability probe; action wiring
// comes only after the probe can prove the target is real.
func cmdPlugins(args []string) error {
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	if action != "list" && action != "probe" && action != "verify" {
		return fmt.Errorf("unknown plugins command %q; use list, probe, or verify", action)
	}

	fs := flag.NewFlagSet("plugins "+action, flag.ExitOnError)
	dir := fs.String("dir", filepath.Join(indexDir(), "plugins"), "plugin manifest directory")
	asJSON := fs.Bool("json", false, "machine-readable output")
	allowNetwork := fs.Bool("allow-network", false, "allow HTTP probes outside loopback")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected plugins arguments: %s", strings.Join(fs.Args(), " "))
	}

	loaded, err := plugins.LoadDir(*dir)
	if err != nil {
		return fmt.Errorf("read plugin directory: %w", err)
	}
	if *asJSON {
		return emitJSON(pluginViews(loaded, action, *allowNetwork))
	}

	fmt.Printf("\n  %s  %s\n", render.Bold("INTEGRATIONS"), render.Dim(*dir))
	if len(loaded) == 0 {
		fmt.Printf("\n  %s\n\n", render.Dim("No advanced manifests found. Run `midden ui` and open Integrations to set up built-in tools."))
		return nil
	}

	fmt.Printf("\n  %-16s %-12s %-7s %-12s %s\n", "name", "kind", "cost", "status", "detail")
	fmt.Printf("  %s\n", render.Rule(78))
	for _, view := range pluginViews(loaded, action, *allowNetwork) {
		status := view.Status
		switch status {
		case plugins.Available:
			status = render.Bold(status)
		case plugins.Disabled:
			status = render.Dim(status)
		case plugins.NotChecked:
			status = render.Dim("not checked")
		default:
			status = render.Warn(status)
		}
		fmt.Printf("  %-16s %-12s %-7s %-12s %s\n",
			coreTruncate(view.Name, 16), safeTerminal(view.Kind), safeTerminal(view.Cost), status, safeTerminal(view.Detail))
		for _, op := range view.Operations {
			if op.Status == "ok" {
				fmt.Printf("    %s %-6s %s\n", render.Dim("ok"), safeTerminal(op.Method), safeTerminal(op.Path))
			} else {
				fmt.Printf("    %s %-6s %s  %s\n", render.Warn("missing"), safeTerminal(op.Method), safeTerminal(op.Path), render.Dim(safeTerminal(op.Detail)))
			}
		}
	}
	fmt.Printf("\n  %s\n\n", render.Dim("Advanced manifests are listed passively. Run `midden plugins probe` (or `verify`) to check one."))
	return nil
}

type pluginView struct {
	Name       string              `json:"name"`
	Kind       string              `json:"kind"`
	Cost       string              `json:"cost"`
	Enabled    bool                `json:"enabled"`
	Status     string              `json:"status"`
	Detail     string              `json:"detail"`
	Operations []plugins.Operation `json:"operations,omitempty"`
}

func pluginViews(loaded []plugins.Loaded, action string, allowNetwork bool) []pluginView {
	views := make([]pluginView, 0, len(loaded))
	for _, loaded := range loaded {
		m := loaded.Manifest
		view := pluginView{
			Name:    pluginName(m),
			Kind:    m.Kind,
			Cost:    m.Cost,
			Enabled: m.IsEnabled(),
		}
		if loaded.Error != nil {
			view.Status, view.Detail = plugins.Unavailable, "unparseable: "+loaded.Error.Error()
		} else if errs := plugins.Validate(m); len(errs) > 0 {
			view.Status, view.Detail = plugins.Unavailable, "invalid: "+strings.Join(errs, "; ")
		} else if !m.IsEnabled() {
			view.Status, view.Detail = plugins.Disabled, "turned off in manifest"
		} else if action == "verify" && m.Kind == "service" {
			result := plugins.VerifyServiceWithOptions(context.Background(), m, nil, plugins.ProbeOptions{AllowNetwork: allowNetwork})
			view.Status, view.Detail, view.Operations = result.Result.Status, result.Result.Detail, result.Operations
		} else if action == "probe" {
			result := plugins.ProbeManifestWithOptions(context.Background(), m, nil, plugins.ProbeOptions{AllowNetwork: allowNetwork})
			view.Status, view.Detail = result.Status, result.Detail
		} else {
			view.Status, view.Detail = plugins.NotChecked, "not probed"
		}
		views = append(views, view)
	}
	return views
}

func pluginName(m plugins.Manifest) string {
	if m.Name != "" {
		return m.Name
	}
	return strings.TrimSuffix(filepath.Base(m.File), filepath.Ext(m.File))
}

func indexDir() string {
	if v := os.Getenv("MIDDEN_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".midden"
	}
	return filepath.Join(home, ".midden")
}

func coreTruncate(s string, n int) string {
	s = safeTerminal(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 2 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// safeTerminal makes manifest-controlled output printable. A YAML string can
// contain ESC, newline, OSC, or other control characters; writing it verbatim
// turns `midden plugins list` into a terminal-control channel rather than a
// diagnostic command.
func safeTerminal(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			if r <= 0xff {
				fmt.Fprintf(&b, `\x%02X`, r)
			} else {
				fmt.Fprintf(&b, `\u%04X`, r)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
