package main

import (
	"net/http"
	"strings"
)

// serveModelExtras serves the model routes for local model servers and
// extension services, and reports false for any other path:
//
//	POST   /api/models/local
//	PUT    /api/models/extension
//	DELETE /api/models/extension
//	POST   /api/models/extension/{instanceId}/token
//	POST   /api/models/extension/{instanceId}/signin
//	POST   /api/models/extension/signin/{flowId}/poll
//	POST   /api/models/extension/signin/{flowId}/complete
func (a *App) serveModelExtras(w http.ResponseWriter, r *http.Request) bool {
	const extensionRoot = "/api/models/extension"
	path := r.URL.Path
	switch {
	case path == "/api/models/local":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return true
		}
		a.serveLocalModelServers(w, r)
		return true
	case path == extensionRoot:
		switch r.Method {
		case http.MethodPut:
			a.serveExtensionConnect(w, r)
		case http.MethodDelete:
			a.serveExtensionDisconnect(w, r)
		default:
			methodNotAllowed(w, "PUT, DELETE")
		}
		return true
	case strings.HasPrefix(path, extensionRoot+"/"):
		parts := strings.Split(strings.TrimPrefix(path, extensionRoot+"/"), "/")
		var serve func(http.ResponseWriter, *http.Request, string)
		id := parts[0]
		switch {
		case len(parts) == 3 && parts[0] == "signin" && parts[2] == "poll":
			serve, id = a.serveExtensionSignInPoll, parts[1]
		case len(parts) == 3 && parts[0] == "signin" && parts[2] == "complete":
			serve, id = a.serveExtensionSignInComplete, parts[1]
		case len(parts) == 2 && parts[1] == "token":
			serve = a.serveExtensionToken
		case len(parts) == 2 && parts[1] == "signin":
			serve = a.serveExtensionSignInStart
		}
		if serve == nil || id == "" {
			return false
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return true
		}
		serve(w, r, id)
		return true
	}
	return false
}
