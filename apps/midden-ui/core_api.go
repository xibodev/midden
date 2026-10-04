package main

import "net/http"

// serveCore is the human door to the deterministic core: routes under
// /api/core/ run the same core binary and validator as the agent's midden tool.
func (a *App) serveCore(w http.ResponseWriter, r *http.Request) {
	apiError(w, http.StatusNotFound, "route not found")
}
