package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mekjr1/midden/internal/module"
)

// PackageModule materializes the executable and every descriptor-declared
// overlay and skill into an empty module root suitable for host registration.
func PackageModule(binary, outDir string) (string, error) {
	if !filepath.IsAbs(binary) {
		return "", fmt.Errorf("module binary %q is not absolute", binary)
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		return "", fmt.Errorf("module binary %q is unavailable", binary)
	}
	if !filepath.IsAbs(outDir) {
		return "", fmt.Errorf("package directory %q is not absolute", outDir)
	}
	if entries, err := os.ReadDir(outDir); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("package directory is not empty: %s", outDir)
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect package directory: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("create package directory: %w", err)
	}

	dest := filepath.Join(outDir, packagedBinaryName())
	if err := copyPackagedFile(binary, dest, 0o755); err != nil {
		return "", fmt.Errorf("package module binary: %w", err)
	}

	descriptor := module.Describe()
	writeContent := func(kind, id, rel, digest string) error {
		clean, err := packagedRelativePath(rel)
		if err != nil {
			return fmt.Errorf("%s %q: %w", kind, id, err)
		}
		raw, ok := module.OverlayContent(rel)
		if !ok {
			return fmt.Errorf("%s %q declares content that is not embedded: %s", kind, id, rel)
		}
		if actual := module.DigestSHA256(raw); actual != digest {
			return fmt.Errorf("%s %q digest mismatch: declared %s, embedded %s", kind, id, digest, actual)
		}
		path := filepath.Join(outDir, clean)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create %s directory: %w", kind, err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return fmt.Errorf("write %s %q: %w", kind, id, err)
		}
		written, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("verify %s %q: %w", kind, id, err)
		}
		if actual := module.DigestSHA256(written); actual != digest {
			return fmt.Errorf("verify %s %q: wrote digest %s, want %s", kind, id, actual, digest)
		}
		return nil
	}
	for _, overlay := range descriptor.AgentOverlays {
		if err := writeContent("overlay", overlay.ID, overlay.Path, overlay.Digest); err != nil {
			return "", err
		}
	}
	for _, skill := range descriptor.Skills {
		if err := writeContent("skill", skill.ID, skill.Path, skill.Digest); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func packagedBinaryName() string {
	if runtime.GOOS == "windows" {
		return "midden.exe"
	}
	return "midden"
}

func packagedRelativePath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" || rel != strings.TrimSpace(rel) {
		return "", fmt.Errorf("declared path is empty or contains surrounding whitespace")
	}
	if strings.Contains(rel, `\`) || strings.Contains(rel, ":") {
		return "", fmt.Errorf("declared path %q is not a portable module-relative path", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if !filepath.IsLocal(clean) || clean == "." {
		return "", fmt.Errorf("declared path %q escapes the module root", rel)
	}
	return clean, nil
}

func copyPackagedFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}
