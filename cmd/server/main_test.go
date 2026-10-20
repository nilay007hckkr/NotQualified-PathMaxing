package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"pathmaxing/internal/app"
)

// TestServerBoots starts every module registered in this binary, with no
// database configured, and checks /healthz before shutting down. Each part
// adds a module_<part>.go file to this package, so at every commit this test
// runs the whole registry: a module that can't start without a database, or
// that breaks startup or shutdown, fails here.
func TestServerBoots(t *testing.T) {
	env := map[string]string{
		"APP_ENV":  "development",
		"BASE_URL": "http://127.0.0.1",
	}
	cfg, err := app.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := app.Build(ctx, cfg, log, app.Modules())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("GET /healthz = %d %q, want 200 \"ok\\n\"", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("server did not shut down within 30s")
	}
}
