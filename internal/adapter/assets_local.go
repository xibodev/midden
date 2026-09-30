package adapter

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type assetLocation struct {
	root     string
	relative string
	modified time.Time
	rootInfo os.FileInfo
}

func assetLocalReference(reference string) (string, string, string) {
	if strings.ContainsRune(reference, 0) || len(reference) > 4096 {
		return "", "unavailable", "Invalid or overlong local asset reference."
	}
	if strings.HasPrefix(reference, `\\`) || strings.HasPrefix(reference, "//") {
		return "", "external_reference", "Unsupported remote or device file reference; no network access is performed."
	}
	if filepath.IsAbs(reference) {
		return reference, "", ""
	}
	uri, err := url.Parse(reference)
	if err != nil {
		return "", "unavailable", "Invalid asset URI."
	}
	switch strings.ToLower(uri.Scheme) {
	case "":
		return reference, "", ""
	case "http", "https":
		return "", "external_reference", "External reference only; network fetching is disabled."
	case "file", "localfile":
		if uri.Host != "" && !strings.EqualFold(uri.Host, "localhost") {
			return "", "external_reference", "Unsupported remote file URI; no network access is performed."
		}
		if uri.RawQuery != "" || uri.Fragment != "" || uri.User != nil {
			return "", "unavailable", "Unsupported file URI query, fragment or credentials."
		}
		local := uri.Path
		if local == "" {
			local = uri.Opaque
		}
		if runtime.GOOS == "windows" && len(local) >= 3 && local[0] == '/' && local[2] == ':' {
			local = local[1:]
		}
		local = filepath.FromSlash(local)
		if local == "" || strings.HasPrefix(local, `\\`) || strings.HasPrefix(local, "//") {
			return "", "external_reference", "Unsupported remote or empty file URI."
		}
		return local, "", ""
	default:
		return "", "unavailable", "Unsupported asset URI scheme; no content was fetched."
	}
}

func localAssetPath(root, name string) (string, bool) {
	if !filepath.IsAbs(name) {
		if !filepath.IsLocal(name) {
			return "", false
		}
		return name, true
	}
	relative, err := filepath.Rel(root, name)
	return relative, err == nil && filepath.IsLocal(relative)
}

func locateAsset(name string, roots []string) (assetLocation, int64, string, string) {
	reason := "Recorded local asset is missing."
	status := "unavailable"
	eligible := false
	for _, original := range roots {
		if !filepath.IsAbs(original) {
			continue
		}
		relative, ok := localAssetPath(original, name)
		if !ok {
			continue
		}
		eligible = true
		canonical, err := filepath.EvalSymlinks(original)
		if err != nil {
			if !os.IsNotExist(err) {
				reason = "Recorded local asset root cannot be resolved."
			}
			continue
		}
		root, err := os.OpenRoot(canonical)
		if err != nil {
			reason = "Recorded local asset root cannot be opened."
			continue
		}
		rootInfo, rootErr := root.Stat(".")
		info, statErr := root.Stat(relative)
		root.Close()
		if rootErr != nil {
			reason = "Recorded local asset root cannot be inspected."
			continue
		}
		if statErr != nil {
			if !os.IsNotExist(statErr) {
				return assetLocation{}, 0, "external_reference", "Local reference cannot be safely resolved inside a permitted root."
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return assetLocation{}, 0, "unavailable", "Recorded local asset is not a regular file."
		}
		if info.Size() > MaxAssetBytes {
			return assetLocation{}, info.Size(), "unavailable", "Asset omitted: local content exceeds 16 MiB."
		}
		return assetLocation{root: canonical, relative: relative, modified: info.ModTime(), rootInfo: rootInfo}, info.Size(), "available", ""
	}
	if !eligible {
		return assetLocation{}, 0, "external_reference", "Local reference is outside the source session, store and recorded workspace."
	}
	return assetLocation{}, 0, status, reason
}

// WriteTo copies at most 16 MiB. Local references are reopened through os.Root,
// so a symlink swap after listing cannot redirect the read outside its root.
func (a RecordedAsset) WriteTo(writer io.Writer) (int64, error) {
	switch a.Status {
	case "embedded":
		if int64(len(a.data)) != a.Bytes || a.Bytes > MaxAssetBytes {
			return 0, fmt.Errorf("invalid embedded asset size")
		}
		return io.Copy(writer, bytes.NewReader(a.data))
	case "available":
		root, err := os.OpenRoot(a.location.root)
		if err != nil {
			return 0, fmt.Errorf("local asset root is no longer available")
		}
		defer root.Close()
		rootInfo, err := root.Stat(".")
		if err != nil || a.location.rootInfo == nil || !os.SameFile(a.location.rootInfo, rootInfo) {
			return 0, fmt.Errorf("local asset root changed after listing")
		}
		// Stat before Open also refuses devices and named pipes without
		// opening them; Stat after Open checks the actual file handle.
		info, err := root.Stat(a.location.relative)
		if err != nil || !assetMatches(info, a) {
			return 0, fmt.Errorf("local asset changed or is no longer confined to its recorded root")
		}
		file, err := root.Open(a.location.relative)
		if err != nil {
			return 0, fmt.Errorf("local asset cannot be safely opened")
		}
		defer file.Close()
		info, err = file.Stat()
		if err != nil || !assetMatches(info, a) {
			return 0, fmt.Errorf("local asset changed before copying")
		}
		n, err := io.Copy(writer, io.LimitReader(file, a.Bytes))
		if err != nil {
			return n, fmt.Errorf("copy local asset: %w", err)
		}
		var extra [1]byte
		extraN, extraErr := file.Read(extra[:])
		if extraErr != io.EOF || extraN != 0 || n != a.Bytes {
			return n, fmt.Errorf("local asset changed while copying")
		}
		info, err = file.Stat()
		if err != nil || !assetMatches(info, a) {
			return n, fmt.Errorf("local asset changed while copying")
		}
		return n, nil
	default:
		return 0, fmt.Errorf("asset is not available for copying")
	}
}

func assetMatches(info os.FileInfo, asset RecordedAsset) bool {
	return info != nil && info.Mode().IsRegular() && info.Size() == asset.Bytes &&
		info.Size() <= MaxAssetBytes && info.ModTime().Equal(asset.location.modified)
}
