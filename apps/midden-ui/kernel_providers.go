package main

// Model connections run on llmgw-core providers, built the way compa-kernel
// builds them, so a connection Midden checks is the one the kernel uses.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/xibodev/llm-provider-auth/tokenstore"
	core "github.com/xibodev/llmgw-core"
	"github.com/xibodev/llmgw-core/extension"
	"github.com/xibodev/llmgw-core/providers"
	"github.com/xibodev/llmgw-core/translation"
)

// errSignInRequired reports a provider whose sign-in has not happened.
var errSignInRequired = errors.New("sign in to this provider first")

// rosterItem is a provider the Models page offers to connect.
type rosterItem struct {
	ID, Label, Adapter, Protocol, DefaultEndpoint string
	AuthMethods                                   []string
	RequiresAPIKey, RequiresBaseURL, Keyless      bool
}

// rosterDuplicates leaves out a registry entry that offers the same provider
// as another: Google's Gemini API is listed natively and through its
// OpenAI-compatible endpoint, which the kernel streams and calls tools on.
var rosterDuplicates = map[string]string{"ai_studio": "gemini"}

// providerRoster lists the registry's providers the kernel runs and a person
// can connect with an API key or nothing, sorted by id.
func providerRoster() []rosterItem {
	registry := providers.ProviderRegistry()
	servable := map[string]bool{}
	for _, entry := range registry {
		servable[entry.ID] = rosterServable(entry)
	}
	roster := []rosterItem{}
	for _, entry := range registry {
		if !servable[entry.ID] || servable[rosterDuplicates[entry.ID]] {
			continue
		}
		adapter, _ := registryAdapter(entry)
		protocol := entry.Protocol
		if protocol == "" {
			protocol = entry.RuntimeType
		}
		roster = append(roster, rosterItem{ID: entry.ID, Label: entry.Label, Adapter: adapter, Protocol: protocol,
			DefaultEndpoint: entry.DefaultBaseURL, AuthMethods: append([]string{}, entry.AuthMethods...),
			RequiresAPIKey: entry.RequiresAPIKey, RequiresBaseURL: entry.RequiresBaseURL, Keyless: entry.AnonymousAutomation})
	}
	sort.Slice(roster, func(i, j int) bool { return roster[i].ID < roster[j].ID })
	return roster
}

func rosterByID() map[string]rosterItem {
	out := map[string]rosterItem{}
	for _, item := range providerRoster() {
		out[item.ID] = item
	}
	return out
}

func rosterServable(entry providers.RegistryEntry) bool {
	if entry.ClientOnly || strings.TrimSpace(entry.AuthAdapter) != "" || !strings.EqualFold(entry.Availability, "available") {
		return false
	}
	if !slices.ContainsFunc(entry.AuthMethods, func(method string) bool { return method == "api_key" || method == "none" }) {
		return false
	}
	_, ok := registryAdapter(entry)
	return ok
}

// registryAdapter is the adapter an instance of entry records: the
// compatible adapter of its wire, or native for another runtime the kernel
// runs.
func registryAdapter(entry providers.RegistryEntry) (string, bool) {
	switch entry.RuntimeType {
	case "openai_compatible":
		return adapterOpenAI, true
	case "anthropic":
		return adapterAnthropic, true
	}
	if _, ok := coreRuntimes[entry.RuntimeType]; ok {
		return adapterNative, true
	}
	return "", false
}

// coreBuild is what a runtime builds a provider from.
type coreBuild struct {
	instance   *providerInstance
	registryID string
	endpoint   string
	client     *http.Client
}

// coreRuntimes maps a registry runtime type to the llmgw-core provider that
// serves it.
var coreRuntimes = map[string]func(coreBuild) (core.Provider, error){
	"openai_compatible": func(b coreBuild) (core.Provider, error) {
		return providers.NewOpenAICompatible(providers.OpenAICompatibleConfig{BaseURL: b.endpoint, RegistryID: b.registryID,
			ForwardAllFields: true, Client: b.client, CatalogClient: b.client})
	},
	"anthropic": func(b coreBuild) (core.Provider, error) {
		// llmgw-core appends /v1/messages to the API root.
		return providers.NewAnthropic(providers.AnthropicConfig{BaseURL: strings.TrimSuffix(strings.TrimRight(b.endpoint, "/"), "/v1"),
			Client: b.client, CatalogClient: b.client})
	},
	"ai_studio": func(b coreBuild) (core.Provider, error) {
		return providers.NewGoogle(providers.GoogleConfig{Deployment: providers.GoogleAIStudio, BaseURL: b.endpoint, Client: b.client})
	},
	"vertex_ai": func(b coreBuild) (core.Provider, error) {
		return providers.NewGoogle(providers.GoogleConfig{Deployment: providers.GoogleVertexAI, BaseURL: b.endpoint, Client: b.client,
			Project: b.instance.setting("project"), Location: b.instance.setting("location")})
	},
	"azure_openai": func(b coreBuild) (core.Provider, error) {
		return providers.NewAzureOpenAI(providers.AzureOpenAIConfig{BaseURL: b.endpoint, Client: b.client, CatalogClient: b.client})
	},
	"bedrock": func(b coreBuild) (core.Provider, error) {
		// A Bedrock endpoint is a URL or, without a scheme, a region.
		region, base := "", b.endpoint
		if base != "" && !strings.Contains(base, "://") {
			region, base = base, ""
		}
		return providers.NewBedrock(region, base, providers.OpenAICompatibleConfig{RegistryID: b.registryID, ForwardAllFields: true,
			Client: b.client, CatalogClient: b.client})
	},
	"elevenlabs": func(b coreBuild) (core.Provider, error) {
		return providers.NewElevenLabs(providers.ElevenLabsConfig{BaseURL: b.endpoint, Client: b.client})
	},
	"mimo": func(b coreBuild) (core.Provider, error) {
		return providers.NewMiMo(providers.MiMoConfig{BaseURL: b.endpoint, Client: b.client})
	},
	"ollama": func(b coreBuild) (core.Provider, error) {
		// Ollama's native API sits at the server's root, not its /v1.
		return providers.NewOllama(providers.OllamaConfig{BaseURL: strings.TrimSuffix(strings.TrimRight(b.endpoint, "/"), "/v1"),
			Client: b.client, CatalogClient: b.client})
	},
}

// adapterRuntimes is the runtime of an instance whose kind the registry does
// not know.
var adapterRuntimes = map[string]string{adapterOpenAI: "openai_compatible", adapterAnthropic: "anthropic"}

// instanceRuntimeType is the runtime that serves instance, or false when the
// kernel runs none for it.
func instanceRuntimeType(instance *providerInstance) (string, bool) {
	adapter := strings.ToLower(strings.TrimSpace(instance.Adapter))
	if adapter == adapterExtension {
		return "extension", true
	}
	if entry, ok := providers.RegistryProvider(strings.TrimSpace(instance.ProviderKind)); ok {
		if _, served := coreRuntimes[entry.RuntimeType]; served {
			return entry.RuntimeType, true
		}
	}
	runtime, ok := adapterRuntimes[adapter]
	return runtime, ok
}

// newCoreProvider builds the provider instance runs on, serving Chat
// Completions natively or through translation.
func newCoreProvider(home string, instance *providerInstance) (core.Provider, error) {
	if err := supportedAdapter(instance); err != nil {
		return nil, err
	}
	runtime, ok := instanceRuntimeType(instance)
	if !ok {
		return nil, fmt.Errorf("provider instance %q: Compa runs no provider for kind %q with adapter %q", instance.ID, instance.ProviderKind, instance.Adapter)
	}
	var (
		provider core.Provider
		err      error
	)
	if runtime == "extension" {
		provider, err = newExtensionProvider(home, instance)
	} else {
		var client *http.Client
		if client, err = instanceHTTPClient(instance, false); err == nil {
			registryID := ""
			if entry, ok := providers.RegistryProvider(strings.TrimSpace(instance.ProviderKind)); ok {
				registryID = entry.ID
			}
			provider, err = coreRuntimes[runtime](coreBuild{instance: instance, registryID: registryID,
				endpoint: strings.TrimSpace(instance.Endpoint), client: client})
		}
	}
	if err != nil {
		return nil, fmt.Errorf("provider instance %q: %w", instance.ID, err)
	}
	if core.ServesNatively(provider, "", core.ModelSurfaceChatCompletions) {
		return provider, nil
	}
	return translation.Adapter{Provider: provider}, nil
}

// instanceHTTPClient sends an instance's requests with its headers, proxy and
// request timeout, which bounds the wait for an answer's headers rather than
// a whole stream. direct skips the proxy, for a service on this computer.
func instanceHTTPClient(instance *providerInstance, direct bool) (*http.Client, error) {
	var runtime struct {
		Proxy          string `json:"proxy"`
		RequestTimeout int    `json:"request_timeout"`
	}
	if len(instance.Runtime) > 0 {
		if err := json.Unmarshal(instance.Runtime, &runtime); err != nil {
			return nil, fmt.Errorf("provider instance %q has unreadable runtime settings", instance.ID)
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 5 * time.Minute
	if runtime.RequestTimeout > 0 {
		transport.ResponseHeaderTimeout = time.Duration(runtime.RequestTimeout) * time.Second
	}
	transport.Proxy = http.ProxyFromEnvironment
	if direct {
		transport.Proxy = nil
	} else if proxy := strings.TrimSpace(runtime.Proxy); proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil || parsed.Host == "" {
			return nil, fmt.Errorf("provider instance %q has an invalid proxy URL", instance.ID)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	var roundTripper http.RoundTripper = transport
	if len(instance.Headers) > 0 {
		roundTripper = headerTransport{headers: instance.Headers, base: transport}
	}
	return &http.Client{Transport: roundTripper, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// headerTransport adds an instance's headers; a header the provider set,
// such as its authentication, is never replaced.
type headerTransport struct {
	headers map[string]string
	base    *http.Transport
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	out := request.Clone(request.Context())
	for name, value := range t.headers {
		if strings.TrimSpace(name) != "" && out.Header.Get(name) == "" {
			out.Header.Set(name, value)
		}
	}
	return t.base.RoundTrip(out)
}

func (t headerTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

// instanceCredential is the credential each request of instance carries.
// secret is what its auth_connection_ref resolved to: an API key or an
// extension provider's token. A signed-in extension provider's credential is
// read, and refreshed when due, through the shared token store.
func instanceCredential(ctx context.Context, home string, instance *providerInstance, secret string) (*core.Credential, error) {
	secret = strings.TrimSpace(secret)
	if instance.extensionProvider() == "" {
		if secret == "" {
			return nil, nil
		}
		return &core.Credential{APIKey: secret, TokenType: core.TokenTypeAPIKey}, nil
	}
	switch kind := strings.ToLower(instance.setting(settingExtensionCredential)); kind {
	case "none":
		return nil, nil
	case "token":
		if secret == "" {
			return nil, fmt.Errorf("provider instance %q needs its token", instance.ID)
		}
		return &core.Credential{Token: secret}, nil
	case "oauth":
		key := instance.setting(settingExtensionCredentialKey)
		if key == "" {
			return nil, errSignInRequired
		}
		daemonSecret, err := extensionDaemonSecret(home)
		if err != nil {
			return nil, err
		}
		client, err := newExtensionClient(instance.Endpoint, daemonSecret)
		if err != nil {
			return nil, err
		}
		coordinator, err := tokenstore.NewCoordinator(authTokenStore{kernelAuth(home)}, client.RefreshFunc(instance.extensionProvider()))
		if err != nil {
			return nil, err
		}
		record, err := coordinator.Token(ctx, key)
		if errors.Is(err, tokenstore.ErrNotFound) {
			return nil, errSignInRequired
		}
		if err != nil {
			return nil, err
		}
		return core.CredentialFromRecord(key, record), nil
	default:
		return nil, fmt.Errorf("provider instance %q has unknown credential kind %q", instance.ID, kind)
	}
}

// listInstanceModels returns the models instance reaches with secret.
func listInstanceModels(ctx context.Context, home string, instance *providerInstance, secret string) ([]catalogModel, error) {
	provider, err := newCoreProvider(home, instance)
	if err != nil {
		return nil, err
	}
	credential, err := instanceCredential(ctx, home, instance, secret)
	if err != nil {
		return nil, err
	}
	models, err := provider.ListModels(ctx, credential)
	if err != nil {
		return nil, fmt.Errorf("catalog request failed: %w", err)
	}
	return catalogModelsFromCore(models), nil
}

// catalogModelsFromCore keeps what the kernel reads of a catalog: each
// model's id, names, the surfaces it serves and the inputs it takes.
func catalogModelsFromCore(models []core.ModelInfo) []catalogModel {
	catalog := make([]catalogModel, 0, len(models))
	for _, model := range models {
		if strings.TrimSpace(model.ID) == "" {
			continue
		}
		owner := model.OwnedBy
		if owner == "" {
			owner = model.Vendor
		}
		inputs := modelInputModalities(model)
		catalog = append(catalog, catalogModel{ID: model.ID, OwnedBy: owner, DisplayName: model.DisplayName,
			Surfaces: modelSurfaces(model), InputModalities: inputs, AudioInput: slices.Contains(inputs, "audio")})
	}
	return catalog
}

// apiSurfaces maps the endpoint paths a catalog row lists to surfaces.
var apiSurfaces = map[string]core.ModelSurface{
	"/chat/completions": core.ModelSurfaceChatCompletions, "/responses": core.ModelSurfaceResponses,
	"/messages": core.ModelSurfaceMessages, "/embeddings": core.ModelSurfaceEmbeddings,
	"/audio/transcriptions": core.ModelSurfaceAudioTranscriptions, "/audio/speech": core.ModelSurfaceAudioSpeech,
	"/images/generations": core.ModelSurfaceImages, "/videos/generations": core.ModelSurfaceVideos, "/videos": core.ModelSurfaceVideos,
}

// modelSurfaces is the sorted surfaces a catalog row reports from its
// supported APIs and typed capabilities; none when it reports neither. A
// chat model's audio in and out are its chat's, not the transcription and
// speech endpoints.
func modelSurfaces(model core.ModelInfo) []string {
	set := map[core.ModelSurface]bool{}
	for _, api := range model.SupportedAPIs {
		path := strings.TrimPrefix("/"+strings.Trim(strings.ToLower(strings.TrimSpace(api)), "/"), "/v1")
		if surface, ok := apiSurfaces[path]; ok {
			set[surface] = true
		} else if named := core.ModelSurface(strings.Trim(path, "/")); core.KnownModelSurface(named) {
			set[named] = true
		}
	}
	if capabilities := model.Capabilities; capabilities != nil {
		add := func(support core.Support, surface core.ModelSurface) {
			if support == core.SupportSupported {
				set[surface] = true
			}
		}
		add(capabilities.Surfaces.ChatCompletions, core.ModelSurfaceChatCompletions)
		add(capabilities.Surfaces.Responses, core.ModelSurfaceResponses)
		add(capabilities.Surfaces.Messages, core.ModelSurfaceMessages)
		add(capabilities.Operations.Embeddings, core.ModelSurfaceEmbeddings)
		add(capabilities.Operations.Image, core.ModelSurfaceImages)
		add(capabilities.Operations.Video, core.ModelSurfaceVideos)
		listed := make([]core.ModelSurface, 0, len(set))
		for surface := range set {
			listed = append(listed, surface)
		}
		if capabilities.Operations.Chat != core.SupportSupported && !translation.ServesChat(listed...) {
			add(capabilities.Operations.AudioIn, core.ModelSurfaceAudioTranscriptions)
			add(capabilities.Operations.AudioOut, core.ModelSurfaceAudioSpeech)
		}
	}
	if len(set) == 0 {
		return nil
	}
	surfaces := make([]string, 0, len(set))
	for surface := range set {
		surfaces = append(surfaces, string(surface))
	}
	slices.Sort(surfaces)
	return surfaces
}

// modelInputModalities is the sorted inputs a catalog row declares, among
// audio, image and text; nothing is guessed from the model's id.
func modelInputModalities(model core.ModelInfo) []string {
	capabilities := model.Capabilities
	if capabilities == nil {
		capabilities = core.InferCapabilities(model, time.Time{}, time.Time{})
	}
	set := map[string]bool{}
	add := func(modality string) {
		switch modality = strings.ToLower(strings.TrimSpace(modality)); modality {
		case "audio", "image", "text":
			set[modality] = true
		}
	}
	if capabilities.Inputs.Text == core.SupportSupported {
		add("text")
	}
	if capabilities.Inputs.Image == core.SupportSupported {
		add("image")
	}
	if capabilities.Operations.AudioIn == core.SupportSupported {
		add("audio")
	}
	legacy := model.LegacyCapabilities
	for _, key := range []string{"input_audio", "audio_input"} {
		if enabled, _ := legacy[key].(bool); enabled {
			add("audio")
		}
	}
	collect := func(raw any) {
		if list, ok := stringList(raw); ok {
			for _, item := range list {
				add(item)
			}
		}
	}
	collect(legacy["input_modalities"])
	collect(legacy["inputs"])
	for _, key := range []string{"modalities", "architecture"} {
		if nested, ok := legacy[key].(map[string]any); ok {
			collect(nested["input"])
			collect(nested["input_modalities"])
		}
	}
	if len(set) == 0 {
		return nil
	}
	modalities := make([]string, 0, len(set))
	for modality := range set {
		modalities = append(modalities, modality)
	}
	slices.Sort(modalities)
	return modalities
}

// servesChat reports whether a model with surfaces can be chosen for chat; a
// model whose catalog reports no surfaces stays choosable.
func servesChat(surfaces []string) bool {
	if len(surfaces) == 0 {
		return true
	}
	native := make([]core.ModelSurface, 0, len(surfaces))
	for _, surface := range surfaces {
		native = append(native, core.ModelSurface(surface))
	}
	return translation.ServesChat(native...)
}

// Extension services.

// checkExtensionEndpoint refuses a service Midden must not send its secret
// to: anything but http(s), plain http beyond this computer, and a remote
// service without a secret.
func checkExtensionEndpoint(endpoint, secret string) error {
	base, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return errors.New("needs the extension service's http(s) URL as its endpoint")
	}
	if loopbackHost(base.Hostname()) {
		return nil
	}
	if base.Scheme != "https" {
		return fmt.Errorf("the extension service on %s needs an https URL; plain http is only for this computer", base.Hostname())
	}
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("the extension service on %s needs a shared secret", base.Hostname())
	}
	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// extensionControlClient reaches a service's control routes: never through a
// proxy and never following redirects.
func extensionControlClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: extension.DefaultTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func newExtensionClient(endpoint, secret string) (*extension.Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("extension service URL is required")
	}
	if err := checkExtensionEndpoint(endpoint, secret); err != nil {
		return nil, err
	}
	return extension.NewClient(extension.Config{BaseURL: endpoint, Secret: secret, HTTPClient: extensionControlClient()})
}

// extensionDaemonSecret is the stored secret of the extension service.
func extensionDaemonSecret(home string) (string, error) {
	credential, err := kernelAuth(home).get(extensionDaemonKey)
	if err != nil {
		return "", fmt.Errorf("read the extension service secret: %w", err)
	}
	if credential == nil {
		return "", nil
	}
	return credential.AccessToken, nil
}

func newExtensionProvider(home string, instance *providerInstance) (core.Provider, error) {
	secret, err := extensionDaemonSecret(home)
	if err != nil {
		return nil, err
	}
	if err := checkExtensionEndpoint(instance.Endpoint, secret); err != nil {
		return nil, err
	}
	client, err := instanceHTTPClient(instance, true)
	if err != nil {
		return nil, err
	}
	service, err := extension.NewClient(extension.Config{BaseURL: strings.TrimSpace(instance.Endpoint), Secret: secret, HTTPClient: client})
	if err != nil {
		return nil, err
	}
	surfaces := []core.ModelSurface{}
	for _, surface := range instance.extensionSurfaces() {
		surfaces = append(surfaces, core.ModelSurface(surface))
	}
	return extension.NewProvider(service, extension.ProviderInfo{ID: instance.extensionProvider(), Surfaces: surfaces}), nil
}

// extensionSurfaces are the surfaces llmgw-core defines among those a service
// provider lists, lower-cased, in the service's order.
func extensionSurfaces(info extension.ProviderInfo) []string {
	surfaces := []string{}
	for _, surface := range info.Surfaces {
		name := core.ModelSurface(strings.ToLower(strings.TrimSpace(string(surface))))
		if core.KnownModelSurface(name) && !slices.Contains(surfaces, string(name)) {
			surfaces = append(surfaces, string(name))
		}
	}
	return surfaces
}

// extensionSurface is a service provider's primary surface, its instance's
// protocol: Chat Completions, then Messages, then the first it lists.
func extensionSurface(info extension.ProviderInfo) string {
	surfaces := extensionSurfaces(info)
	for _, preferred := range []string{string(core.ModelSurfaceChatCompletions), string(core.ModelSurfaceMessages)} {
		if slices.Contains(surfaces, preferred) {
			return preferred
		}
	}
	if len(surfaces) > 0 {
		return surfaces[0]
	}
	return ""
}

// Settings the kernel validates on load: a change Midden makes keeps them
// valid, so the kernel never refuses its own configuration.

func supportedAdapter(instance *providerInstance) error {
	switch strings.ToLower(strings.TrimSpace(instance.Adapter)) {
	case adapterOpenAI, adapterAnthropic, adapterNative:
		return nil
	case adapterExtension:
		if instance.extensionProvider() == "" {
			return fmt.Errorf("provider instance %q has no %s setting", instance.ID, settingExtensionProvider)
		}
		protocol := strings.ToLower(strings.TrimSpace(instance.Protocol))
		if !core.KnownModelSurface(core.ModelSurface(protocol)) {
			return fmt.Errorf("provider instance %q uses surface %q, which llmgw-core does not define", instance.ID, instance.Protocol)
		}
		if raw, ok := instance.Settings[settingExtensionSurfaces]; ok {
			surfaces, valid := stringList(raw)
			if !valid || len(surfaces) == 0 {
				return fmt.Errorf("provider instance %q: %s must list llmgw-core surfaces", instance.ID, settingExtensionSurfaces)
			}
			for _, surface := range surfaces {
				if !core.KnownModelSurface(core.ModelSurface(surface)) {
					return fmt.Errorf("provider instance %q: %s lists surface %q, which llmgw-core does not define", instance.ID, settingExtensionSurfaces, surface)
				}
			}
			if !slices.Contains(surfaces, protocol) {
				return fmt.Errorf("provider instance %q: %s does not list its protocol %q", instance.ID, settingExtensionSurfaces, protocol)
			}
		}
		return nil
	}
	return fmt.Errorf("provider instance %q uses adapter %q, which Compa does not include", instance.ID, instance.Adapter)
}

func validateInstance(instance *providerInstance) error {
	switch {
	case !stableName.MatchString(instance.ID):
		return errors.New("id must be a stable lowercase identifier using letters, numbers, '.', '_', or '-'")
	case strings.TrimSpace(instance.ProviderKind) == "":
		return errors.New("provider_kind is required")
	case strings.TrimSpace(instance.Adapter) == "":
		return errors.New("adapter is required")
	case strings.TrimSpace(instance.Protocol) == "":
		return errors.New("protocol is required")
	case instance.State != instanceEnabled && instance.State != instanceDisabled:
		return fmt.Errorf("state must be %q or %q", instanceEnabled, instanceDisabled)
	}
	for name := range instance.Headers {
		if strings.TrimSpace(name) == "" {
			return errors.New("header name must not be empty")
		}
	}
	return supportedAdapter(instance)
}

func validSelectionSyntax(selection string) error {
	selection = strings.TrimSpace(selection)
	switch {
	case selection == "":
		return nil
	case strings.Contains(selection, "/"):
		if _, err := parseExactTarget(selection); err != nil {
			return fmt.Errorf("selection %q: %w", selection, err)
		}
	case !stableName.MatchString(selection):
		return fmt.Errorf("selection %q must be an exact target instance-id/model-id or a model route name", selection)
	}
	return nil
}

// validateModelSettings checks the model settings as the kernel does when it
// loads them.
func validateModelSettings(cfg *kernelConfig) error {
	instances := map[string]*providerInstance{}
	for i, instance := range cfg.Instances {
		if err := validateInstance(instance); err != nil {
			return fmt.Errorf("provider_instances[%d]: %w", i, err)
		}
		if instances[instance.ID] != nil {
			return fmt.Errorf("provider_instances[%d]: duplicate id %q", i, instance.ID)
		}
		instances[instance.ID] = instance
	}
	routes := map[string]bool{}
	for i, route := range cfg.Routes {
		if err := validateRoute(route, instances); err != nil {
			return fmt.Errorf("model_routes[%d]: %w", i, err)
		}
		if routes[route.Name] {
			return fmt.Errorf("model_routes[%d]: duplicate name %q", i, route.Name)
		}
		routes[route.Name] = true
	}
	var invalid error
	cfg.eachSelection(func(selection string) string {
		if err := validSelectionSyntax(selection); err != nil && invalid == nil {
			invalid = err
		}
		return selection
	})
	return invalid
}

func validateRoute(route *modelRoute, instances map[string]*providerInstance) error {
	if !stableName.MatchString(route.Name) {
		return errors.New("name must be a stable lowercase identifier using letters, numbers, '.', '_', or '-'")
	}
	if len(route.Targets) == 0 {
		return errors.New("targets must contain at least one exact target")
	}
	seen := map[string]bool{}
	for i, raw := range route.Targets {
		target, err := parseExactTarget(raw)
		if err != nil {
			return fmt.Errorf("targets[%d]: %w", i, err)
		}
		instance := instances[target.Instance]
		switch {
		case instance == nil:
			return fmt.Errorf("targets[%d]: provider instance %q not found", i, target.Instance)
		case instance.State == instanceDisabled:
			return fmt.Errorf("targets[%d]: provider instance %q is disabled", i, target.Instance)
		case seen[target.String()]:
			return fmt.Errorf("targets[%d]: duplicate target %q", i, target.String())
		}
		seen[target.String()] = true
	}
	return nil
}

// checkSelection reports whether selection, an exact target or a route,
// resolves as the kernel resolves it: every target's instance exists and is
// enabled, and its saved model list holds the model. Empty selects nothing
// and is valid.
func checkSelection(cfg *kernelConfig, catalogs map[string]*catalogEntry, selection string) error {
	selection = strings.TrimSpace(selection)
	if selection == "" {
		return nil
	}
	if err := validSelectionSyntax(selection); err != nil {
		return err
	}
	targets := []string{selection}
	if _, err := parseExactTarget(selection); err != nil {
		index := cfg.routeIndex(selection)
		if index < 0 {
			return fmt.Errorf("target %q is invalid and route was not found", selection)
		}
		targets = cfg.Routes[index].Targets
	}
	for i, raw := range targets {
		target, err := parseExactTarget(raw)
		if err != nil {
			return fmt.Errorf("target[%d]: %w", i, err)
		}
		instance := cfg.instance(target.Instance)
		switch {
		case instance == nil:
			return fmt.Errorf("target[%d]: provider instance %q not found", i, target.Instance)
		case instance.State != instanceEnabled:
			return fmt.Errorf("target[%d]: provider instance %q is disabled", i, target.Instance)
		case !validCatalog(target.Instance, catalogs[target.Instance], instance):
			return fmt.Errorf("target[%d]: catalog for provider instance %q not found", i, target.Instance)
		case !slices.ContainsFunc(catalogs[target.Instance].Models, func(model catalogModel) bool { return strings.TrimSpace(model.ID) == target.Model }):
			return fmt.Errorf("target[%d]: model %q not found in provider instance %q catalog", i, target.Model, target.Instance)
		}
	}
	return nil
}

// dropTargets removes every exact target keep rejects: from the chat
// shortlist, from routes (a route left empty goes), and from the model
// selections, which are cleared when they name a dropped target or a removed
// route.
func dropTargets(cfg *kernelConfig, keep func(exactTarget) bool) {
	dropped := func(raw string) bool {
		target, err := parseExactTarget(raw)
		return err == nil && !keep(target)
	}
	cfg.ActiveModels = slices.DeleteFunc(cfg.ActiveModels, dropped)
	removed := map[string]bool{}
	cfg.Routes = slices.DeleteFunc(cfg.Routes, func(route *modelRoute) bool {
		route.Targets = slices.DeleteFunc(route.Targets, dropped)
		if len(route.Targets) == 0 {
			removed[route.Name] = true
			return true
		}
		return false
	})
	cfg.eachSelection(func(selection string) string {
		value := strings.TrimSpace(selection)
		if _, err := parseExactTarget(value); err == nil && dropped(value) || removed[value] {
			return ""
		}
		return selection
	})
}

// adoptDefaultModel makes target the default model when none is set.
func adoptDefaultModel(cfg *kernelConfig, target string) bool {
	if target = strings.TrimSpace(target); target == "" || cfg.DefaultModel() != "" {
		return false
	}
	cfg.SetDefaultModel(target)
	return true
}

// probeToolCall asks model for an inert tool call; nothing is executed.
func probeToolCall(ctx context.Context, provider core.Provider, credential *core.Credential, model string) error {
	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []any{map[string]any{"role": "user",
			"content": "This is a connection check with no user files. Call midden_connection_check with ok=true. Do not answer with prose."}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "midden_connection_check", "description": "An inert tool-capability probe. No command or file operation is executed.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
				"required": []string{"ok"}, "additionalProperties": false}}}},
	})
	if err != nil {
		return err
	}
	response, err := provider.Invoke(ctx, core.Request{Surface: core.ModelSurfaceChatCompletions, Model: model, Body: body,
		ContentType: core.ContentTypeJSON, Credential: credential})
	if err != nil {
		return err
	}
	var answer struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response.Body, &answer); err != nil {
		return fmt.Errorf("the service answered with something other than a chat completion: %w", err)
	}
	for _, choice := range answer.Choices {
		for _, call := range choice.Message.ToolCalls {
			var arguments map[string]any
			if call.Function.Name != "midden_connection_check" {
				continue
			}
			if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
				return fmt.Errorf("the model returned malformed probe arguments: %w", err)
			}
			if arguments["ok"] == true {
				return nil
			}
		}
	}
	return errors.New("the service responded but did not return the required tool call; this model is not verified for tool use")
}

// redact replaces every nonempty secret in text.
func redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret = strings.TrimSpace(secret); secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}
