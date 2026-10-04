package main

import "net/http"

// serveModelExtras serves the extension and local-server model routes; this
// build has none.
func (a *App) serveModelExtras(w http.ResponseWriter, r *http.Request) bool { return false }
