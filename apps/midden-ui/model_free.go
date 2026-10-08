package main

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/xibodev/llmgw-core/providers"
)

// freeOutcome is one free provider's result as the Models page shows it.
type freeOutcome struct {
	InstanceID string `json:"instanceId"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	Models     int    `json:"models"`
}

// freeResult is one provider line of `compa-kernel model auto-free`.
type freeResult struct {
	ID, Status, Class, Error string
}

// freeRunner runs the kernel's own free-model connection and returns what
// it printed; tests replace it.
var freeRunner = func(ctx context.Context, kernel string, env []string) ([]byte, error) {
	command := exec.CommandContext(ctx, kernel, "--no-color", "model", "auto-free")
	command.Env = env
	command.WaitDelay = 5 * time.Second
	output := &boundedOutput{max: 64 << 10}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	return output.Bytes(), err
}

// connectFree runs compa-kernel model auto-free, which checks the free
// providers that need no key, connects those that answer and makes the
// first model that answered the default when none is set. The kernel then
// reloads its settings.
func (a *App) connectFree(ctx context.Context) ([]freeOutcome, error) {
	if a.opts.Kernel == "" {
		return nil, modelFailure(http.StatusServiceUnavailable, "the assistant's kernel is not installed, so free models cannot be checked")
	}
	ctx, cancel := context.WithTimeout(ctx, freeTimeout)
	defer cancel()
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	release, err := a.admitModelChange(errModelBusy)
	if err != nil {
		return nil, err
	}
	defer release()
	if _, err := a.loadModelConfig(); err != nil {
		return nil, err
	}
	output, err := freeRunner(ctx, a.opts.Kernel, kernelCommandEnv(a.paths.Kernel))
	if err != nil {
		reason := lastLine(string(output))
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || reason == "" {
			reason = err.Error()
		}
		return nil, modelFailure(http.StatusBadGateway, "Compa could not check the free models: %s", clip(reason, 300))
	}
	results := parseFreeResults(string(output))
	for _, result := range results {
		if result.Status == "verified" {
			if err := a.reloadKernel(ctx); err != nil {
				return nil, err
			}
			break
		}
	}
	cfg, err := a.loadModelConfig()
	if err != nil {
		return nil, err
	}
	catalogs, err := loadCatalogs(a.paths.Kernel)
	if err != nil {
		return nil, err
	}
	roster := rosterByID()
	outcomes := make([]freeOutcome, 0, len(results))
	for _, result := range results {
		outcome := freeOutcome{InstanceID: result.ID, Status: freeStatus(result), Error: result.Error}
		instance := cfg.instance(result.ID)
		kind := ""
		if instance != nil {
			kind = instance.ProviderKind
		}
		outcome.Label = freeLabel(result.ID, kind, roster)
		if instance != nil && validCatalog(result.ID, catalogs[result.ID], instance) {
			outcome.Models = len(catalogs[result.ID].Models)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// freeLabel names a free service as the provider list does. Compa reports
// each service by its connection id, such as kilo-code, while the list is
// keyed by registry id, such as kilo_code.
func freeLabel(id, kind string, roster map[string]rosterItem) string {
	registry := ""
	for _, profile := range providers.AnonymousProviderProfiles() {
		if profile.ProviderID == id {
			registry = profile.RegistryID
		}
	}
	for _, key := range []string{kind, registry, id} {
		if item, ok := roster[key]; ok && item.Label != "" {
			return item.Label
		}
	}
	return id
}

// parseFreeResults reads the provider lines auto-free prints:
//
//   - <id>: <status>[ (<error class>)][ - <error>]
func parseFreeResults(output string) []freeResult {
	results := []freeResult{}
	for _, line := range strings.Split(output, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "- ")
		if !ok {
			continue
		}
		id, detail, ok := strings.Cut(rest, ": ")
		if !ok || !stableName.MatchString(id) {
			continue
		}
		result := freeResult{ID: id, Status: detail}
		if status, reason, ok := strings.Cut(detail, " - "); ok {
			result.Status, result.Error = status, strings.TrimSpace(reason)
		}
		if open := strings.Index(result.Status, " ("); open >= 0 && strings.HasSuffix(result.Status, ")") {
			result.Class = result.Status[open+2 : len(result.Status)-1]
			result.Status = result.Status[:open]
		}
		result.Status = strings.TrimSpace(result.Status)
		results = append(results, result)
	}
	return results
}

// freeStatus words Compa's outcome for the Models page.
func freeStatus(result freeResult) string {
	switch {
	case result.Status == "verified":
		return "answers_text"
	case result.Class == "rate_limited":
		return "busy"
	case result.Status == "connected":
		return "connected"
	}
	return "failed"
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
