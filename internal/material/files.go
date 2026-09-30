package material

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func readLimited(name string, limit int64) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimitedFile(file, limit)
}

func readLimitedFile(file *os.File, limit int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if limit < 0 || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("file is not regular or exceeds the %d byte limit", limit)
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit || int64(len(raw)) != info.Size() {
		return nil, fmt.Errorf("file changed or grew beyond the read limit")
	}
	return raw, nil
}

// readCachedView bounds allocation and verifies source-window identity.
func readCachedView(state, id string) (cachedView, error) {
	var cached cachedView
	if !filepath.IsAbs(state) || !viewIDPattern.MatchString(id) {
		return cached, fmt.Errorf("invalid state root or view id")
	}
	raw, err := readLimited(filepath.Join(state, "views", id+".json"), MaxCollectionBytes)
	if err != nil {
		return cached, fmt.Errorf("view is unavailable in this state directory: %w", err)
	}
	if err = json.Unmarshal(raw, &cached); err != nil {
		return cached, err
	}
	if cached.View.Schema != ViewSchema || cached.View.ID != id {
		return cached, fmt.Errorf("unsupported or mismatched source view")
	}
	for i := range cached.View.Records {
		if cached.View.Records[i].Reference.ViewID != id {
			return cached, fmt.Errorf("cached record has a mismatched source view")
		}
	}
	if viewIdentity(cached.View) != id {
		return cached, fmt.Errorf("cached source view failed content identity verification")
	}

	return cached, nil
}

func sameJSON(a, b any) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

func portableRelative(name string) bool {
	return name != "" && len(name) <= 4096 && !strings.ContainsAny(name, "\\:\x00") &&
		path.Clean(name) == name && filepath.IsLocal(filepath.FromSlash(name)) && name != "."
}

// All collection metadata and content are opened through the same root handle.
// Portable collections contain regular files, never symlink dependencies.
func openCollectionFile(root *os.Root, relative string) (*os.File, error) {
	if !portableRelative(relative) {
		return nil, fmt.Errorf("invalid collection-relative path")
	}
	var info os.FileInfo
	var err error
	parts := strings.Split(relative, "/")
	for i := range parts {
		info, err = root.Lstat(filepath.FromSlash(strings.Join(parts[:i+1], "/")))
		if err != nil {
			return nil, err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil, fmt.Errorf("collection symlinks are not supported")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, fmt.Errorf("collection path parent is not a directory")
		}
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("collection content is not a regular file")
	}
	file, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return nil, fmt.Errorf("collection file changed while opening")
	}
	return file, nil
}

func readCollectionFile(root *os.Root, relative string, limit int64) ([]byte, error) {
	file, err := openCollectionFile(root, relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLimitedFile(file, limit)
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (b *limitedBuffer) Len() int      { return b.buffer.Len() }
func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *limitedBuffer) Write(raw []byte) (int, error) {
	if int64(len(raw)) > b.limit-int64(b.Len()) {
		return 0, fmt.Errorf("output exceeds the %d byte limit", b.limit)
	}
	return b.buffer.Write(raw)
}

type collectionOutput struct {
	path    string
	name    string
	parent  *os.Root
	root    *os.Root
	created []string
}

func claimCollectionDirectory(name string) (*collectionOutput, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("collection output directory is required")
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	parent, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	leaf := filepath.Base(absolute)
	// Claim the final name atomically. Renaming a staging directory could
	// replace an empty directory created by somebody else after a preflight.
	if err = parent.Mkdir(leaf, 0700); err != nil {
		parent.Close()
		return nil, fmt.Errorf("output already exists or cannot be claimed; choose a new collection directory: %w", err)
	}
	root, err := parent.OpenRoot(leaf)
	if err != nil {
		removeErr := parent.Remove(leaf)
		parent.Close()
		return nil, errors.Join(err, removeErr)
	}
	return &collectionOutput{path: absolute, name: leaf, parent: parent, root: root}, nil
}

func (out *collectionOutput) write(name string, raw []byte) error {
	file, err := out.root.OpenFile(filepath.FromSlash(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	out.created = append(out.created, name)
	_, writeErr := file.Write(raw)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func (out *collectionOutput) finish(cause error) error {
	if cause != nil {
		for i := len(out.created) - 1; i >= 0; i-- {
			if err := out.root.Remove(filepath.FromSlash(out.created[i])); err != nil {
				cause = errors.Join(cause, fmt.Errorf("clean incomplete collection: %w", err))
			}
		}
	}
	cause = errors.Join(cause, out.root.Close())
	if cause != nil {
		cause = errors.Join(cause, out.parent.Remove(out.name))
	}
	return errors.Join(cause, out.parent.Close())
}

func writeNewFile(name string, raw []byte) error {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	defer root.Close()
	leaf := filepath.Base(absolute)
	file, err := root.OpenFile(leaf, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	err = errors.Join(writeErr, file.Close())
	if err != nil {
		err = errors.Join(err, root.Remove(leaf))
	}
	return err
}
