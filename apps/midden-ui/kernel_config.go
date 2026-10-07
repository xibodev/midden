package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Provider instance states, adapters and settings, as Compa's config.json
// records them.
const (
	instanceEnabled  = "enabled"
	instanceDisabled = "disabled"

	adapterOpenAI    = "openai-compatible"
	adapterAnthropic = "anthropic-compatible"
	adapterNative    = "native"
	adapterExtension = "extension"

	settingDisplayName            = "display_name"
	settingExtensionProvider      = "extension_provider"
	settingExtensionCredential    = "extension_credential"
	settingExtensionCredentialKey = "extension_credential_key"
	settingExtensionSurfaces      = "extension_surfaces"
)

// stableName matches Compa's provider instance ids and route names.
var stableName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)

// errSettingsChanged ends a change whose file another writer replaced
// meanwhile, so neither change is lost.
var errSettingsChanged = errors.New("the model settings changed while this change ran; try again")

// providerInstance is one entry of provider_instances: a connection to a
// provider that models run on.
type providerInstance struct {
	ID                string            `json:"id"`
	ProviderKind      string            `json:"provider_kind"`
	Adapter           string            `json:"adapter"`
	Protocol          string            `json:"protocol"`
	Endpoint          string            `json:"endpoint,omitempty"`
	AuthConnectionRef string            `json:"auth_connection_ref,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	Settings          map[string]any    `json:"settings,omitempty"`
	// Runtime holds request settings Midden never changes; it is kept as
	// read.
	Runtime json.RawMessage `json:"runtime,omitempty"`
	State   string          `json:"state"`
}

func (p *providerInstance) clone() *providerInstance {
	copy := *p
	if p.Headers != nil {
		copy.Headers = make(map[string]string, len(p.Headers))
		for name, value := range p.Headers {
			copy.Headers[name] = value
		}
	}
	if p.Settings != nil {
		copy.Settings = make(map[string]any, len(p.Settings))
		for name, value := range p.Settings {
			copy.Settings[name] = value
		}
	}
	copy.Runtime = append(json.RawMessage(nil), p.Runtime...)
	return &copy
}

func (p *providerInstance) setting(name string) string {
	value, _ := p.Settings[name].(string)
	return strings.TrimSpace(value)
}

// extensionProvider is the extension service's id of the provider an
// extension instance reaches, or "" for any other instance.
func (p *providerInstance) extensionProvider() string {
	if p == nil || !strings.EqualFold(strings.TrimSpace(p.Adapter), adapterExtension) {
		return ""
	}
	return p.setting(settingExtensionProvider)
}

// extensionSurfaces lists the surfaces an extension instance's provider
// serves: the extension_surfaces setting, or its protocol.
func (p *providerInstance) extensionSurfaces() []string {
	if p.extensionProvider() == "" {
		return nil
	}
	if surfaces, ok := stringList(p.Settings[settingExtensionSurfaces]); ok && len(surfaces) > 0 {
		return surfaces
	}
	if protocol := strings.ToLower(strings.TrimSpace(p.Protocol)); protocol != "" {
		return []string{protocol}
	}
	return nil
}

// stringList reads a list of strings, lower-cased and without repeats, as
// held in memory or decoded from JSON.
func stringList(raw any) ([]string, bool) {
	var values []string
	switch list := raw.(type) {
	case []string:
		values = list
	case []any:
		for _, item := range list {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, text)
		}
	default:
		return nil, false
	}
	out := []string{}
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out, true
}

// modelRoute is one entry of model_routes: ordered failover over exact
// targets.
type modelRoute struct {
	Name    string   `json:"name"`
	Targets []string `json:"targets"`
}

// extensionDaemon is the extension service that provider instances reach
// providers through.
type extensionDaemon struct {
	URL string `json:"url"`
}

// kernelConfig is the kernel's config.json: the model settings Midden
// changes, decoded, and every other setting exactly as the file holds it.
type kernelConfig struct {
	Instances    []*providerInstance
	Routes       []*modelRoute
	ActiveModels []string
	Extension    *extensionDaemon

	agents   map[string]any             // the agents object, for its model selections
	other    map[string]json.RawMessage // every other top-level setting
	original []byte                     // the file as read; nil when there was none
}

func kernelConfigPath(home string) string { return filepath.Join(home, "config.json") }

// decodeJSON decodes raw into value, keeping numbers exact.
func decodeJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(value)
}

func parseKernelConfig(data []byte) (*kernelConfig, error) {
	cfg := &kernelConfig{agents: map[string]any{}, other: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}
	var top map[string]json.RawMessage
	if err := decodeJSON(data, &top); err != nil || top == nil {
		return nil, fmt.Errorf("config.json is not a JSON object: %v", err)
	}
	for key, raw := range top {
		var err error
		switch key {
		case "provider_instances":
			err = decodeJSON(raw, &cfg.Instances)
		case "model_routes":
			err = decodeJSON(raw, &cfg.Routes)
		case "active_models":
			err = decodeJSON(raw, &cfg.ActiveModels)
		case "extension":
			err = decodeJSON(raw, &cfg.Extension)
		case "agents":
			err = decodeJSON(raw, &cfg.agents)
		default:
			cfg.other[key] = raw
		}
		if err != nil {
			return nil, fmt.Errorf("config.json %s: %w", key, err)
		}
	}
	cfg.Instances = slices.DeleteFunc(cfg.Instances, func(p *providerInstance) bool { return p == nil })
	cfg.Routes = slices.DeleteFunc(cfg.Routes, func(r *modelRoute) bool { return r == nil })
	if cfg.agents == nil {
		cfg.agents = map[string]any{}
	}
	return cfg, nil
}

// encode renders config.json: the settings Midden keeps as read, and its
// own as they are now.
func (c *kernelConfig) encode() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(c.other)+5)
	for key, raw := range c.other {
		out[key] = raw
	}
	set := func(key string, value any, present bool) error {
		if !present {
			delete(out, key)
			return nil
		}
		raw, err := json.Marshal(value)
		if err == nil {
			out[key] = raw
		}
		return err
	}
	for _, err := range []error{
		set("provider_instances", c.Instances, len(c.Instances) > 0),
		set("model_routes", c.Routes, len(c.Routes) > 0),
		set("active_models", c.ActiveModels, len(c.ActiveModels) > 0),
		set("extension", c.Extension, c.Extension != nil),
		set("agents", c.agents, len(c.agents) > 0),
	} {
		if err != nil {
			return nil, err
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// loadKernelConfig reads config.json in home; a missing file reads as no
// settings, which the kernel completes with its defaults.
func loadKernelConfig(home string) (*kernelConfig, error) {
	data, err := os.ReadFile(kernelConfigPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return parseKernelConfig(nil)
	}
	if err != nil {
		return nil, fmt.Errorf("read the kernel settings: %w", err)
	}
	cfg, err := parseKernelConfig(data)
	if err != nil {
		return nil, err
	}
	cfg.original = data
	return cfg, nil
}

// saveKernelConfig replaces config.json with cfg, unless another writer
// replaced it since cfg was read.
func saveKernelConfig(home string, cfg *kernelConfig) error {
	data, err := cfg.encode()
	if err != nil {
		return err
	}
	path := kernelConfigPath(home)
	return withFileLock(path, func() error {
		current, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !bytes.Equal(current, cfg.original) {
			return errSettingsChanged
		}
		if err := replaceFile(path, data); err != nil {
			return err
		}
		cfg.original = data
		return nil
	})
}

// objectAt returns the object at path under root, creating the missing ones
// when create is set; nil when a value on the way is not an object.
func objectAt(root map[string]any, create bool, path ...string) map[string]any {
	current := root
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			if _, exists := current[key]; exists || !create {
				return nil
			}
			next = map[string]any{}
			current[key] = next
		}
		current = next
	}
	return current
}

// DefaultModel is agents.defaults.model_name: an exact target or a route.
func (c *kernelConfig) DefaultModel() string {
	value, _ := objectAt(c.agents, false, "defaults")["model_name"].(string)
	return strings.TrimSpace(value)
}

func (c *kernelConfig) SetDefaultModel(selection string) {
	if defaults := objectAt(c.agents, true, "defaults"); defaults != nil {
		defaults["model_name"] = selection
	}
}

// eachSelection replaces every model selection, the default, image and
// light models and each agent's model, with what change returns for it.
func (c *kernelConfig) eachSelection(change func(string) string) {
	update := func(object map[string]any, key string) {
		if value, ok := object[key].(string); ok {
			object[key] = change(value)
		}
	}
	if defaults := objectAt(c.agents, false, "defaults"); defaults != nil {
		update(defaults, "model_name")
		update(defaults, "image_model")
		if routing := objectAt(defaults, false, "routing"); routing != nil {
			update(routing, "light_model")
		}
	}
	if list, ok := c.agents["list"].([]any); ok {
		for _, item := range list {
			if agent, ok := item.(map[string]any); ok {
				update(agent, "model")
			}
		}
	}
}

func (c *kernelConfig) instance(id string) *providerInstance {
	for _, instance := range c.Instances {
		if instance.ID == id {
			return instance
		}
	}
	return nil
}

func (c *kernelConfig) routeIndex(name string) int {
	return slices.IndexFunc(c.Routes, func(route *modelRoute) bool { return route.Name == name })
}

// rawObject decodes the top-level setting key as an object of raw values;
// absent or null reads as empty.
func (c *kernelConfig) rawObject(key string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if raw, ok := c.other[key]; ok && string(bytes.TrimSpace(raw)) != "null" {
		if err := decodeJSON(raw, &out); err != nil || out == nil {
			return nil, fmt.Errorf("config.json %s is not an object", key)
		}
	}
	return out, nil
}

func (c *kernelConfig) setRawObject(key string, value map[string]json.RawMessage) error {
	raw, err := json.Marshal(value)
	if err == nil {
		c.other[key] = raw
	}
	return err
}

// exactTarget is "instance-id/model-id"; the model id may hold slashes.
type exactTarget struct{ Instance, Model string }

func (t exactTarget) String() string { return t.Instance + "/" + t.Model }

func parseExactTarget(raw string) (exactTarget, error) {
	instance, model, found := strings.Cut(strings.TrimSpace(raw), "/")
	if !found || !stableName.MatchString(instance) {
		return exactTarget{}, errors.New("target must be instance-id/model-id")
	}
	if model == "" || strings.TrimSpace(model) != model || strings.ContainsAny(model, "\t\n\r ") || strings.Contains(model, "//") {
		return exactTarget{}, errors.New("target model-id is invalid")
	}
	return exactTarget{Instance: instance, Model: model}, nil
}
