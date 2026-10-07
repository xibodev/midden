package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// catalogModel is one model of a saved catalog, in Compa's format.
type catalogModel struct {
	ID          string `json:"id"`
	OwnedBy     string `json:"owned_by,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	// Surfaces are the llmgw-core surfaces the model serves; empty when the
	// catalog reports none.
	Surfaces        []string `json:"surfaces,omitempty"`
	InputModalities []string `json:"input_modalities,omitempty"`
	AudioInput      bool     `json:"audio_input,omitempty"`
}

// catalogEntry is the saved model list of one provider instance. The kernel
// resolves a model only when its instance's entry lists it.
type catalogEntry struct {
	ID         string         `json:"id"`
	InstanceID string         `json:"instance_id,omitempty"`
	Provider   string         `json:"provider"`
	APIBase    string         `json:"api_base"`
	Models     []catalogModel `json:"models"`
	FetchedAt  string         `json:"fetched_at"`
}

func catalogsPath(home string) string { return filepath.Join(home, "model_catalogs.json") }

func readCatalogEntries(home string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(catalogsPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read model lists: %w", err)
	}
	var file struct {
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("the model lists file %s is not valid JSON: %w", catalogsPath(home), err)
	}
	if file.Entries == nil {
		file.Entries = map[string]json.RawMessage{}
	}
	return file.Entries, nil
}

// loadCatalogs returns the saved model lists by instance id.
func loadCatalogs(home string) (map[string]*catalogEntry, error) {
	raw, err := readCatalogEntries(home)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]*catalogEntry, len(raw))
	for key, value := range raw {
		if string(bytes.TrimSpace(value)) == "null" {
			continue
		}
		var entry catalogEntry
		if err := json.Unmarshal(value, &entry); err != nil {
			return nil, fmt.Errorf("model list %q: %w", key, err)
		}
		entries[key] = &entry
	}
	return entries, nil
}

// updateCatalogs changes the saved model lists under the lock; lists change
// leaves alone are kept as read.
func updateCatalogs(home string, change func(map[string]json.RawMessage) error) error {
	path := catalogsPath(home)
	return withFileLock(path, func() error {
		entries, err := readCatalogEntries(home)
		if err != nil {
			return err
		}
		if err := change(entries); err != nil {
			return err
		}
		data, err := json.MarshalIndent(map[string]any{"entries": entries}, "", "  ")
		if err != nil {
			return err
		}
		return replaceFile(path, data)
	})
}

// saveInstanceCatalog saves models as the model list of instance.
func saveInstanceCatalog(home string, instance *providerInstance, models []catalogModel) error {
	raw, err := json.Marshal(catalogEntry{ID: instance.ID, InstanceID: instance.ID, Provider: instance.ProviderKind,
		APIBase: strings.TrimRight(strings.TrimSpace(instance.Endpoint), "/"), Models: models,
		FetchedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return updateCatalogs(home, func(entries map[string]json.RawMessage) error {
		entries[instance.ID] = raw
		return nil
	})
}

func deleteInstanceCatalog(home, id string) error {
	return updateCatalogs(home, func(entries map[string]json.RawMessage) error {
		delete(entries, id)
		return nil
	})
}

// validCatalog reports whether entry, saved under key, is the model list of
// instance: saved for that instance and from its kind of provider. A list
// left behind by a removed instance is not.
func validCatalog(key string, entry *catalogEntry, instance *providerInstance) bool {
	return entry != nil && instance != nil && key == instance.ID && entry.ID == instance.ID &&
		entry.InstanceID == instance.ID && entry.Provider == instance.ProviderKind
}
