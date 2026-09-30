package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckDestination rejects writes into source stores, resolved aliases, and
// SQLite companion files. It neither creates nor reserves paths, enforces
// overwrite policy, nor prohibits a recorded workspace as a whole.
func CheckDestination(out string, roots Roots) error {
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("output destination is required")
	}
	target, err := resolveDestinationPath(out)
	if err != nil {
		return err
	}
	for _, configured := range AllWithRoots(roots) {
		var protected []string
		switch source := configured.(type) {
		case *Copilot:
			protected = []string{source.Root}
		case *Claude:
			protected = []string{source.Root}
		case *Opencode:
			resolved, err := resolveDestinationPath(source.DB)
			if err != nil {
				return err
			}
			for _, database := range []string{source.DB, resolved} {
				for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
					protected = append(protected, database+suffix)
				}
			}
		default:
			return fmt.Errorf("unsupported source destination boundary")
		}
		for _, name := range protected {
			store, err := resolveDestinationPath(name)
			if err != nil {
				return err
			}
			if relative, err := filepath.Rel(store, target); err == nil && (relative == "." || filepath.IsLocal(relative)) {
				return fmt.Errorf("output destination must be outside the read-only source stores and database files")
			}
		}
	}
	return nil
}

func resolveDestinationPath(name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	missing := []string{}
	for {
		_, err = os.Lstat(absolute)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", fmt.Errorf("output has no existing filesystem root")
		}
		missing = append(missing, filepath.Base(absolute))
		absolute = parent
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		absolute = filepath.Join(absolute, missing[i])
	}
	return absolute, nil
}
