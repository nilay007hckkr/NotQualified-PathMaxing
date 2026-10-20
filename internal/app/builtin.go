package app

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// mountBuiltins adds the routes that belong to the server itself rather
// than to a part: the health check, the web app and the root redirect.
func mountBuiltins(a *App, mux *http.ServeMux) error {
	mux.HandleFunc("GET /healthz", healthHandler(a))

	if a.Config.WebDistDir == "" {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "PathMaxing is running. The web app is not served here (WEB_DIST_DIR is not set).")
		})
		return nil
	}
	spa, err := newSPAHandler(a.Config.WebDistDir)
	if err != nil {
		return err
	}
	mux.Handle("GET /app/", spa)
	toApp := http.RedirectHandler("/app/", http.StatusFound)
	mux.Handle("GET /app", toApp)
	mux.Handle("GET /{$}", toApp)
	return nil
}

// healthHandler runs every registered check. The response names a failing
// check but not its error, which can reveal host names or other internals;
// the error goes to the log instead.
func healthHandler(a *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		var failed []string
		for _, c := range a.healthChecks() {
			if err := c.fn(ctx); err != nil {
				a.Log.Warn("health check failed", "check", c.name, "err", err)
				failed = append(failed, c.name+": unavailable")
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if len(failed) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, strings.Join(failed, "\n"))
			return
		}
		fmt.Fprintln(w, "ok")
	}
}

// newSPAHandler serves the built web app in dir under /app/.
//
// Client-side routes such as /app/links/42 have no file, so they get
// index.html and the React router takes over. A missing file under
// /app/assets/ is a real 404: answering a missing script with HTML would
// only hide the error.
func newSPAHandler(dir string) (http.Handler, error) {
	fsys := os.DirFS(dir)
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil, fmt.Errorf("WEB_DIST_DIR %q has no index.html (run npm run build in web/): %w", dir, err)
	}
	files := http.FileServerFS(fsys)

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		// index.html names the current asset files, so it must be revalidated.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, "index.html")
	}

	return http.StripPrefix("/app", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r)
			return
		}
		info, err := fs.Stat(fsys, name)
		switch {
		case err == nil && !info.IsDir():
			if strings.HasPrefix(name, "assets/") {
				// Vite puts a content hash in every asset name, so a cached copy
				// can never go stale.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
		case strings.HasPrefix(name, "assets/"):
			http.NotFound(w, r)
		default:
			serveIndex(w, r)
		}
	})), nil
}
