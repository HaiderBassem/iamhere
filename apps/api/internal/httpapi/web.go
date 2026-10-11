package httpapi

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// cspFor builds the Content-Security-Policy for the web app. The app loads no
// third-party code; the only external origin it talks to is the identity
// provider (token and discovery requests).
func cspFor(issuer string) string {
	connect := "'self'"
	if u, err := url.Parse(issuer); err == nil && u.Scheme != "" && u.Host != "" {
		connect += " " + u.Scheme + "://" + u.Host
	}
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; media-src 'self' blob:; connect-src " + connect + "; " +
		"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

// webApp serves the single-page app from dir: real files as themselves, and
// every other non-API path as index.html so client-side routes survive reload.
func webApp(dir, csp string) http.HandlerFunc {
	root, err := filepath.Abs(dir)
	if err != nil {
		root = dir
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/v1" {
			writeJSON(w, 404, errBody{"not_found", "Not found."})
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(self), geolocation=(self), microphone=()")
		// revalidate on every load (ETag / Last-Modified) instead of no-store
		h.Set("Cache-Control", "no-cache")

		// filepath.Join on a rooted, cleaned path cannot escape root
		name := filepath.Join(root, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if st, err := os.Stat(name); err == nil && !st.IsDir() {
			http.ServeFile(w, r, name)
			return
		}
		http.ServeFile(w, r, filepath.Join(root, "index.html"))
	}
}
