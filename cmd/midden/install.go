package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mekjr1/midden/internal/install"
	"github.com/mekjr1/midden/internal/render"
)

// cmdInstall detects agentic CLI hosts and wires Midden into the chosen ones.
//
// Midden is headless. It is used through an agentic CLI that already exists, or
// as a module of a host that does, so getting it wired in is the actual first
// run. This command exists to make that one step rather than a documentation
// exercise.
func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	all := fs.Bool("all", false, "wire every detected target without asking")
	force := fs.Bool("force", false, "overwrite an existing Midden skills install")
	dryRun := fs.Bool("dry-run", false, "show what would happen and change nothing")
	uninstall := fs.Bool("uninstall", false, "remove Midden's skills from detected targets")
	only := fs.String("only", "", "act on one target by id (claude-code, copilot-cli, opencode, facet-studio)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	targets := install.Detect()
	detected := 0
	for _, t := range targets {
		if t.Detected {
			detected++
		}
	}

	fmt.Printf("\n  %s\n", render.Bold("MIDDEN INSTALL"))
	fmt.Printf("  %s\n\n", render.Rule(60))

	if detected == 0 {
		fmt.Printf("  %s\n\n", render.Dim("No agentic CLI was detected on this machine."))
		fmt.Printf("  Midden is headless: it runs inside an agentic CLI, or as a module of\n")
		fmt.Printf("  a host that provides one. Install one of these first:\n\n")
		for _, t := range targets {
			fmt.Printf("    %s\n", t.Name)
		}
		fmt.Println()
		return nil
	}

	fmt.Printf("  %s\n", render.Dim("detected"))
	for _, t := range targets {
		if !t.Detected {
			continue
		}
		fmt.Printf("    %-22s %-8s %s\n", t.Name, t.Kind, render.Dim(t.BinaryPath))
		if t.Note != "" {
			fmt.Printf("    %-22s %s\n", "", render.Dim("note: "+t.Note))
		}
	}
	fmt.Println()

	// A skills install is only useful if the binary it names can be found.
	// Saying so before writing anything is cheaper than letting every command
	// in every installed skill fail later.
	if w := install.PathWarning(); w != "" && !*uninstall {
		fmt.Printf("  %s %s\n\n", render.Bold("warning:"), w)
	}

	if !*all && !*dryRun && *only == "" {
		fmt.Printf("  %s\n", render.Dim("nothing was changed"))
		fmt.Printf("  Run with %s to wire every detected target, %s to preview,\n",
			render.Bold("--all"), render.Bold("--dry-run"))
		fmt.Printf("  or %s to pick one.\n\n", render.Bold("--only <id>"))
		return nil
	}

	var acted bool
	for _, t := range targets {
		if !t.Detected {
			continue
		}
		if *only != "" && !strings.EqualFold(*only, t.ID) {
			continue
		}
		acted = true

		if *dryRun {
			reportPlan(t, *uninstall)
			continue
		}
		if err := applyTarget(t, *force, *uninstall); err != nil {
			// One target failing must not abort the others: a user with four
			// CLIs should not lose three installs to one broken directory.
			fmt.Printf("    %s %s: %v\n", render.Bold("failed"), t.Name, err)
			continue
		}
	}

	if !acted {
		if *only != "" {
			return fmt.Errorf("no detected target matches %q", *only)
		}
		fmt.Printf("  %s\n", render.Dim("no detected target to act on"))
	}
	fmt.Println()
	return nil
}

func reportPlan(t install.Target, uninstall bool) {
	verb := "would install"
	if uninstall {
		verb = "would remove"
	}
	switch t.Kind {
	case install.KindSkills:
		fmt.Printf("    %s  %s -> %s\n", verb, t.Name, render.Dim(t.SkillsDir))
	case install.KindModule:
		if uninstall {
			fmt.Printf("    %s  %s (run its own remove command)\n", verb, t.Name)
			return
		}
		self, err := install.SelfPath()
		if err != nil {
			self = "<this binary>"
		}
		fmt.Printf("    %s  %s -> %s\n", verb, t.Name,
			render.Dim(fmt.Sprintf("%s modules-add %s", t.BinaryPath, self)))
	}
}

func applyTarget(t install.Target, force, uninstall bool) error {
	switch t.Kind {
	case install.KindSkills:
		if uninstall {
			res, err := install.UninstallSkills(t)
			if err != nil {
				return err
			}
			fmt.Printf("    %s  %s (%d removed)\n", render.Bold("removed"), t.Name, len(res.Written))
			return nil
		}
		res, err := install.InstallSkills(t, force)
		if err != nil {
			return err
		}
		if len(res.Written) == 0 && len(res.Skipped) > 0 {
			fmt.Printf("    %s  %s (already installed; --force to update)\n",
				render.Dim("skipped"), t.Name)
			return nil
		}
		// Read back rather than trusting the write.
		if err := install.VerifySkillsInstall(t); err != nil {
			return err
		}
		fmt.Printf("    %s  %s (%d skills, verified)\n", render.Bold("installed"), t.Name, len(res.Written))
		return nil

	case install.KindModule:
		if uninstall {
			fmt.Printf("    %s  %s: remove Midden with the host's own command\n",
				render.Dim("skipped"), t.Name)
			return nil
		}
		res, err := install.RegisterWithHost(t)
		if err != nil {
			if res != nil && res.Output != "" {
				fmt.Fprintln(os.Stderr, render.Dim("    "+res.Output))
			}
			return err
		}
		fmt.Printf("    %s  %s\n", render.Bold("registered"), t.Name)
		if res.Output != "" {
			fmt.Printf("      %s\n", render.Dim(firstLine(res.Output)))
		}
		return nil
	}
	return fmt.Errorf("unknown target kind")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
