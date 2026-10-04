package main

import "net/http"

// serveModels exposes model connections managed through Compa's runtime format
// under STATE/kernel: providers, free models, local servers, extension services,
// routes and the default selection.
func (a *App) serveModels(w http.ResponseWriter, r *http.Request) {
	apiError(w, http.StatusNotFound, "route not found")
}
