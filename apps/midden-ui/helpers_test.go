package main

import (
	"net/http"
	"path/filepath"
	"testing"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	return Options{Data: filepath.Join(root, "data"), Core: "synthetic-core",
		CoreEnv: map[string]string{"MIDDEN_HOME": filepath.Join(root, "core-state")}}
}

// keyed adds the App's launch key, as the browser sends it.
func keyed(app *App, request *http.Request) *http.Request {
	request.AddCookie(&http.Cookie{Name: launchCookie, Value: app.key})
	return request
}
