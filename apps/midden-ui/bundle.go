package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/xibodev/facet-studio/pkg/skills"
)

type BundleInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func mountBundle(source, state string) (string, []BundleInfo, error) {
	root, err := filepath.Abs(source)
	if err != nil {
		return "", nil, err
	}
	if info, err := os.Stat(filepath.Join(root, "bundles")); err == nil && info.IsDir() {
		root = filepath.Join(root, "bundles")
	}
	mapping := map[string]string{"investigation": "midden-investigation", "article": "midden-article", "presentation": "midden-presentation", "long-form": "midden-long-form", "midden-shared": "midden-shared"}
	hash := sha256.New()
	expected := map[string][32]byte{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle symlinks are not supported")
		}
		if entry.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if len(parts) != 2 {
			return nil
		}
		folder, ok := mapping[parts[0]]
		if !ok {
			return nil
		}
		raw, err := readBounded(path, 2<<20)
		if err != nil {
			return err
		}
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		hash.Write(raw)
		hash.Write([]byte{0})
		expected[filepath.Join(folder, parts[1])] = sha256.Sum256(raw)
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	target := filepath.Join(state, "bundle", hex.EncodeToString(hash.Sum(nil)))
	if _, err = os.Stat(target); os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return "", nil, err
		}
		stage, err := os.MkdirTemp(filepath.Dir(target), ".mount-")
		if err != nil {
			return "", nil, err
		}
		for from, to := range mapping {
			if err = os.CopyFS(filepath.Join(stage, to), os.DirFS(filepath.Join(root, from))); err != nil {
				return "", nil, err
			}
		}
		if err = verifyMount(stage, expected); err != nil {
			return "", nil, err
		}
		if err = os.Rename(stage, target); err != nil {
			return "", nil, err
		}
	} else if err != nil {
		return "", nil, err
	}
	if err = verifyMount(target, expected); err != nil {
		return "", nil, err
	}
	loader := skills.NewSkillsLoader(filepath.Join(state, "empty-workspace"), "", target)
	list := loader.ListSkills()
	if len(list) != 4 {
		return "", nil, fmt.Errorf("bundle must expose all four Midden outcomes")
	}
	out := []BundleInfo{}
	for _, info := range list {
		body, ok := loader.LoadSkill(info.Name)
		if !ok || strings.TrimSpace(body) == "" {
			return "", nil, fmt.Errorf("bundle skill %s could not be loaded by name", info.Name)
		}
		out = append(out, BundleInfo{info.Name, info.Description})
	}
	return target, out, nil
}

func verifyMount(root string, expected map[string][32]byte) error {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("mounted bundle contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		want, ok := expected[rel]
		if !ok {
			return fmt.Errorf("mounted bundle contains an unexpected file")
		}
		raw, err := readBounded(path, 2<<20)
		if err != nil {
			return err
		}
		if sha256.Sum256(raw) != want {
			return fmt.Errorf("mounted bundle changed; inspect the host bundle cache before restarting")
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(expected) {
		return fmt.Errorf("mounted bundle is incomplete")
	}
	return nil
}
