package api

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// panelIndex is the entry page of the built panel (apps/panel/dist).
const panelIndex = "index.html"

// CheckPanel reports whether dir holds a built panel, so a wrong
// TOLKOVO_PANEL_DIR fails at startup and not on the first visit.
func CheckPanel(dir fs.FS) error {
	st, err := fs.Stat(dir, panelIndex)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return errors.New(panelIndex + " is a directory")
	}
	return nil
}

// panelHandler serves the built panel on the API's host, so the session
// cookie (__Host-, bound to the host) works without a separate web server
// (docs/02 §16.2). It answers routes nothing else matched: files of the
// build as they are, any other page path with index.html for the client
// router. /api and paths that look like files stay 404 problems, so a
// mistyped API call or a missing asset never returns HTML.
func panelHandler(dir fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || p == "/api" || strings.HasPrefix(p, "/api/") {
			notFound(w, r)
			return
		}
		name := strings.TrimPrefix(p, "/")
		if st, err := fs.Stat(dir, name); err == nil && !st.IsDir() {
			panelHeaders(w)
			if strings.HasPrefix(name, "assets/") {
				// Vite puts a content hash in every name under assets/.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, dir, name) //nolint:gosec // G703: name is cleaned and fs.FS rejects paths outside dir
			return
		}
		if path.Ext(name) != "" {
			notFound(w, r)
			return
		}
		panelHeaders(w)
		// A new release must reach open tabs: index.html names the new assets.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, dir, panelIndex)
	}
}

// panelHeaders complete the CSP that the build puts into index.html
// (apps/panel/vite.config.ts): a <meta> cannot forbid framing.
func panelHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
}
