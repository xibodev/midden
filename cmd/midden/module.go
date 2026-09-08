package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mekjr1/midden/internal/module"
)

// cmdModule implements the detached-module protocol surface:
//
//	midden module describe --json
//	midden module invoke <capability> --input <request.json>
//
// This command is spoken by a module host, not by a person. Its contract is
// strict: stdout carries exactly one JSON envelope and nothing else, and every
// diagnostic goes to stderr where it is advisory only.
func cmdModule(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: midden module <describe|invoke> [flags]")
	}

	switch args[0] {
	case "describe":
		return cmdModuleDescribe(args[1:])
	case "invoke":
		return cmdModuleInvoke(args[1:])
	default:
		return fmt.Errorf("unknown module subcommand %q (want describe or invoke)", args[0])
	}
}

func cmdModuleDescribe(args []string) error {
	fs := flag.NewFlagSet("module describe", flag.ExitOnError)
	// --json is accepted and ignored: this surface is always JSON. The flag
	// exists because the host's documented invocation includes it.
	fs.Bool("json", true, "emit JSON (always true for this surface)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	d := module.Describe()
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}

	// Describe is local and genuinely free — explicit zero costs, not unknown.
	env, err := module.NewResultEnvelope(module.OpDescribe, "", json.RawMessage(raw), module.LocalFree())
	if err != nil {
		return err
	}
	return emitEnvelope(env)
}

func cmdModuleInvoke(args []string) error {
	fs := flag.NewFlagSet("module invoke", flag.ExitOnError)
	input := fs.String("input", "", "path to the request JSON document")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: midden module invoke <capability> --input <request.json>")
	}
	capability := fs.Arg(0)

	req := module.Request{Capability: capability}
	if *input != "" {
		raw, err := os.ReadFile(*input)
		if err != nil {
			// A request we cannot read is still a protocol response, not a
			// crash: the host gets a structured envelope it can route on.
			return emitEnvelope(module.NewErrorEnvelope(module.OpInvoke, "", module.Error{
				Code:      module.ErrInvalidRequest,
				Message:   "request document could not be read: " + err.Error(),
				Retryable: false,
			}, module.LocalFree()))
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return emitEnvelope(module.NewErrorEnvelope(module.OpInvoke, "", module.Error{
				Code:      module.ErrInvalidRequest,
				Message:   "request document is not valid JSON: " + err.Error(),
				Retryable: false,
			}, module.LocalFree()))
		}
		// The capability on the command line is authoritative: it is what the
		// host asked to run.
		req.Capability = capability
	}
	req.Normalize()

	return emitEnvelope(module.Invoke(req))
}

// emitEnvelope writes exactly one JSON document to stdout.
//
// Compact and newline-terminated: the host reads one bounded document, and
// indentation would only inflate it against MaxOutputBytes.
func emitEnvelope(env module.Envelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}
