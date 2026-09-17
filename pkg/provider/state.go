package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

const moduleStateDir = ".midden-module-state"

// ModuleStateRoot derives Midden-owned state beside, never inside, a Studio
// workspace. It does not inspect MIDDEN_HOME or the user profile.
func ModuleStateRoot(workspace string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", fmt.Errorf("workspace path is empty")
	}

	abs, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	canonical := filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}

	identity := filepath.ToSlash(canonical)
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	sum := sha256.Sum256([]byte(identity))
	key := hex.EncodeToString(sum[:8])

	return filepath.Join(filepath.Dir(canonical), moduleStateDir, key), nil
}
