package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mekjr1/midden/internal/legacy"
)

func runLegacy(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "export" {
		return fmt.Errorf("usage: midden legacy export --from OLD_STATE --out NEW_DIRECTORY [--json]")
	}
	fs := flag.NewFlagSet("legacy export", flag.ContinueOnError)
	source := fs.String("from", "", "old Midden state directory containing index.db")
	destination := fs.String("out", "", "new directory outside the old store; parent must exist")
	asJSON := fs.Bool("json", false, "structured report")
	if err := fs.Parse(reorderArgs(fs, args[1:])); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("legacy export accepts only explicit --from/--out flags")
	}
	report, err := legacy.Export(*source, *destination)
	if err != nil {
		return err
	}
	return emitMaterial(out, *asJSON, report)
}
