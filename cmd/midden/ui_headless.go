//go:build headless

package main

import "fmt"

// cmdUI in a headless build.
//
// The web UI is excluded here deliberately: a build meant to run as a module
// behind facet-studio does not need to serve its own interface. The command
// still EXISTS and fails with a reason, rather than vanishing -- a missing
// subcommand looks like a broken install, while a refusal explains itself.
func cmdUI([]string) error {
	return fmt.Errorf("this build has no web UI (built with -tags headless); " +
		"use the module protocol via `midden module`, or install the standalone build")
}
