package main

import (
	"crypto/subtle"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Every request needs the key made at each launch. The browser gets it once,
// in the address the App opens, and keeps it in a cookie only this origin's
// pages send. The address is also written to a file only the person can read,
// so the Start entry can reopen an App that is already running.
const (
	launchCookie = "midden_key"
	launchFile   = "launch-address"
)

const launchPage = `<!doctype html>
<html lang="en"><meta charset="utf-8"><title>Midden</title>
<body style="font-family:system-ui,sans-serif;max-width:36rem;margin:4rem auto;line-height:1.5">
<h1>Open Midden from its Start entry</h1>
<p>This page needs the key Midden makes each time it starts. Open Midden again from the Start menu, the Applications folder or your app launcher, or run <code>midden-ui</code>, and it opens with a fresh key.</p>
</body></html>
`

func (a *App) launchAddress(host string) string { return "http://" + host + "/?key=" + a.key }

// admitted reports whether r carries the launch key. A first visit with the
// key in its address gets the cookie and goes on to the page without it.
func (a *App) admitted(w http.ResponseWriter, r *http.Request) bool {
	if cookie, err := r.Cookie(launchCookie); err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(a.key)) == 1 {
		return true
	}
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		if given := r.URL.Query().Get("key"); given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(a.key)) == 1 {
			http.SetCookie(w, &http.Cookie{Name: launchCookie, Value: a.key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return false
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, launchPage)
		return false
	}
	apiError(w, http.StatusUnauthorized, "This Midden page is from an earlier launch. Open Midden again from its Start entry, or run midden-ui.")
	return false
}

func launchFilePath(paths appPaths) string { return filepath.Join(paths.App, launchFile) }

// writeLaunchAddress records the address that opens this App.
func writeLaunchAddress(paths appPaths, address string) error {
	return replaceFile(launchFilePath(paths), []byte(address+"\n"))
}

// removeLaunchAddress forgets the address, unless another App wrote its own.
func removeLaunchAddress(paths appPaths, address string) {
	if current, err := os.ReadFile(launchFilePath(paths)); err == nil && strings.TrimSpace(string(current)) == address {
		_ = os.Remove(launchFilePath(paths))
	}
}

// runningLaunchAddress is the address of an App already running for this
// data, if one answers to it.
func runningLaunchAddress(paths appPaths) (string, bool) {
	raw, err := readBounded(launchFilePath(paths), 4096)
	if err != nil {
		return "", false
	}
	address := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(address, "http://127.0.0.1:") && !strings.HasPrefix(address, "http://[::1]:") {
		return "", false
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(address)
	if err != nil {
		return "", false
	}
	response.Body.Close()
	for _, cookie := range response.Cookies() {
		if response.StatusCode == http.StatusSeeOther && cookie.Name == launchCookie {
			return address, true
		}
	}
	return "", false
}
