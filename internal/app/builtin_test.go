package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	tests := []struct {
		name     string
		checks   map[string]error
		wantCode int
		wantBody string
	}{
		{"no checks", nil, http.StatusOK, "ok\n"},
		{"all pass", map[string]error{"database": nil}, http.StatusOK, "ok\n"},
		{"one fails", map[string]error{"database": errors.New("dial tcp 10.0.0.5:5432: refused")}, http.StatusServiceUnavailable, "database: unavailable\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod := Module{Name: "m", Provide: func(_ context.Context, a *App) error {
				for name, err := range tt.checks {
					a.AddCheck(name, func(context.Context) error { return err })
				}
				return nil
			}}
			s, err := Build(context.Background(), testConfig(t), quietLog(), []Module{mod})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
			if rec.Code != tt.wantCode || rec.Body.String() != tt.wantBody {
				t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
			}
			// The error text can reveal internal addresses, so it stays in the log.
			if strings.Contains(rec.Body.String(), "10.0.0.5") {
				t.Fatal("health response leaks the check's error")
			}
		})
	}
}

func TestRootWithoutWebApp(t *testing.T) {
	s, err := Build(context.Background(), testConfig(t), quietLog(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "WEB_DIST_DIR") {
		t.Fatalf("GET / = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func TestWebApp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "<!doctype html><title>PathMaxing</title>")
	writeFile(t, filepath.Join(dir, "assets", "index-abc123.js"), "console.log(1)")
	writeFile(t, filepath.Join(dir, "favicon.svg"), "<svg/>")

	cfg := testConfig(t)
	cfg.WebDistDir = dir
	s, err := Build(context.Background(), cfg, quietLog(), nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path      string
		wantCode  int
		wantBody  string
		wantCache string
		wantLoc   string
	}{
		{path: "/", wantCode: http.StatusFound, wantLoc: "/app/"},
		{path: "/app", wantCode: http.StatusFound, wantLoc: "/app/"},
		{path: "/app/", wantCode: http.StatusOK, wantBody: "<title>PathMaxing</title>", wantCache: "no-cache"},
		{path: "/app/links/42", wantCode: http.StatusOK, wantBody: "<title>PathMaxing</title>", wantCache: "no-cache"},
		{path: "/app/assets/index-abc123.js", wantCode: http.StatusOK, wantBody: "console.log(1)", wantCache: "public, max-age=31536000, immutable"},
		{path: "/app/assets/missing.js", wantCode: http.StatusNotFound},
		{path: "/app/favicon.svg", wantCode: http.StatusOK, wantBody: "<svg/>"},
		{path: "/app/assets", wantCode: http.StatusOK, wantBody: "<title>PathMaxing</title>"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
			if rec.Code != tt.wantCode {
				t.Fatalf("status %d, want %d (body %q)", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
			if tt.wantCache != "" && rec.Header().Get("Cache-Control") != tt.wantCache {
				t.Errorf("Cache-Control %q, want %q", rec.Header().Get("Cache-Control"), tt.wantCache)
			}
			if tt.wantLoc != "" && rec.Header().Get("Location") != tt.wantLoc {
				t.Errorf("Location %q, want %q", rec.Header().Get("Location"), tt.wantLoc)
			}
		})
	}
}

// The mux already cleans ".." out of paths, but the SPA handler must not rely
// on that: called directly, it still never serves a file outside its dir.
func TestWebAppStaysInsideItsDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "secret.txt"), "do not serve")
	writeFile(t, filepath.Join(root, "dist", "index.html"), "<title>PathMaxing</title>")
	spa, err := newSPAHandler(filepath.Join(root, "dist"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/app/../secret.txt", "/app/assets/../../secret.txt", "/app/..%2fsecret.txt"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.URL.Path = p // set directly: NewRequest would clean it
		spa.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "do not serve") {
			t.Errorf("%s served a file outside the web app", p)
		}
	}
}

func TestWebAppWithoutBuild(t *testing.T) {
	cfg := testConfig(t)
	cfg.WebDistDir = t.TempDir()
	_, err := Build(context.Background(), cfg, quietLog(), nil)
	if err == nil || !strings.Contains(err.Error(), "index.html") {
		t.Fatalf("Build error = %v, want a missing index.html error", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
