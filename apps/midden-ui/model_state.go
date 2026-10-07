package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
)

// errModelBusy refuses model changes while a turn runs.
var errModelBusy = errors.New("stop the active turn before changing model settings")

// errNoModelChange ends a change that found nothing to save.
var errNoModelChange = errors.New("no model change")

// modelUndo collects the steps that reverse a change's effects outside
// config.json, such as stored keys, model lists and check results.
type modelUndo []func()

func (u *modelUndo) add(step func()) { *u = append(*u, step) }

func (u modelUndo) run() {
	for i := len(u) - 1; i >= 0; i-- {
		u[i]()
	}
}

// loadModelConfig reads the kernel's settings. Saves replace the file whole,
// so a read needs no lock; a.modelMu serializes Midden's changes.
func (a *App) loadModelConfig() (*kernelConfig, error) {
	return loadKernelConfig(a.paths.Kernel)
}

// admitModelChange marks a model change running, so no turn starts meanwhile;
// release ends it. Callers hold a.modelMu.
func (a *App) admitModelChange(busy error) (release func(), err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active != nil {
		return nil, busy
	}
	a.modelChanging = true
	return func() { a.mu.Lock(); a.modelChanging = false; a.mu.Unlock() }, nil
}

// updateModelConfig is changeModelConfig for a change with no effects outside
// config.json.
func (a *App) updateModelConfig(ctx context.Context, change func(*kernelConfig) error) error {
	return a.changeModelConfig(ctx, func(cfg *kernelConfig, _ *modelUndo) error { return change(cfg) })
}

// changeModelConfig applies change to the kernel's settings and saves them;
// a failed change saves nothing and runs its undo steps. A change returning
// errNoModelChange saves nothing. A saved change reaches the running kernel
// through /reload. Turns cannot start while a change runs, and a change
// cannot start during a turn.
func (a *App) changeModelConfig(ctx context.Context, change func(*kernelConfig, *modelUndo) error) error {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	release, err := a.admitModelChange(errModelBusy)
	if err != nil {
		return err
	}
	defer release()
	cfg, err := a.loadModelConfig()
	if err != nil {
		return err
	}
	var undo modelUndo
	err = change(cfg, &undo)
	if errors.Is(err, errNoModelChange) {
		return nil
	}
	if err == nil {
		if invalid := validateModelSettings(cfg); invalid != nil {
			err = modelFailure(http.StatusBadRequest, "%v", invalid)
		}
	}
	if err == nil {
		if err = saveKernelConfig(a.paths.Kernel, cfg); err != nil && !errors.Is(err, errSettingsChanged) {
			err = fmt.Errorf("save model configuration: %w", err)
		}
	}
	if err != nil {
		undo.run()
		return err
	}
	return a.reloadKernel(ctx)
}

// reloadKernel makes the running kernel read its settings again.
func (a *App) reloadKernel(ctx context.Context) error {
	if a.kernel == nil {
		return nil
	}
	if err := a.kernel.reload(ctx); err != nil {
		return fmt.Errorf("the settings were saved, but the assistant could not load them: %w", err)
	}
	return nil
}

// modelCheck is the stored result of one tool-call check.
type modelCheck struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	At      string `json:"at"`
}

// modelChecks holds check results by instance and model.
type modelChecks map[string]map[string]modelCheck

func (a *App) modelChecksPath() string {
	return filepath.Join(a.paths.App, "model-checks.json")
}

// loadModelChecks reads the stored check results. They are advisory, so an
// unreadable file counts as no results.
func (a *App) loadModelChecks() modelChecks {
	raw, err := readBounded(a.modelChecksPath(), 4<<20)
	if err != nil {
		return modelChecks{}
	}
	checks := modelChecks{}
	if err := json.Unmarshal(raw, &checks); err != nil || checks == nil {
		return modelChecks{}
	}
	return checks
}

// saveModelChecks replaces the stored check results. Callers hold a.modelMu.
func (a *App) saveModelChecks(checks modelChecks) error {
	return writeJSON(a.modelChecksPath(), checks)
}

// forgetModelChecks drops an instance's check results as part of a change.
func (a *App) forgetModelChecks(id string, undo *modelUndo) error {
	checks := a.loadModelChecks()
	previous, ok := checks[id]
	if !ok {
		return nil
	}
	delete(checks, id)
	if err := a.saveModelChecks(checks); err != nil {
		return fmt.Errorf("save model checks: %w", err)
	}
	undo.add(func() {
		checks := a.loadModelChecks()
		checks[id] = previous
		_ = a.saveModelChecks(checks)
	})
	return nil
}

// storeCredential stores a key under name as part of a change.
func (a *App) storeCredential(name string, credential *authCredential, undo *modelUndo) error {
	store := kernelAuth(a.paths.Kernel)
	previous, err := store.get(name)
	if err != nil {
		return err
	}
	if err := store.set(name, credential); err != nil {
		return fmt.Errorf("store the key: %w", err)
	}
	undo.add(func() { _ = store.set(name, previous) })
	return nil
}

// dropCredential deletes a stored key as part of a change.
func (a *App) dropCredential(name string, undo *modelUndo) error {
	store := kernelAuth(a.paths.Kernel)
	previous, err := store.get(name)
	if err != nil || previous == nil {
		return err
	}
	if err := store.remove(name); err != nil {
		return fmt.Errorf("delete the stored key: %w", err)
	}
	undo.add(func() { _ = store.set(name, previous) })
	return nil
}

// keepCatalogs registers the restore of the saved model lists as they are.
func (a *App) keepCatalogs(undo *modelUndo) error {
	previous, err := readCatalogEntries(a.paths.Kernel)
	if err != nil {
		return err
	}
	undo.add(func() {
		_ = updateCatalogs(a.paths.Kernel, func(entries map[string]json.RawMessage) error {
			for key := range entries {
				delete(entries, key)
			}
			for key, value := range previous {
				entries[key] = value
			}
			return nil
		})
	})
	return nil
}

// saveCatalog saves an instance's model list as part of a change.
func (a *App) saveCatalog(instance *providerInstance, models []catalogModel, undo *modelUndo) error {
	if err := a.keepCatalogs(undo); err != nil {
		return err
	}
	if err := saveInstanceCatalog(a.paths.Kernel, instance, models); err != nil {
		return fmt.Errorf("save the model list: %w", err)
	}
	return nil
}
