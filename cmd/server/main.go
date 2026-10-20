// Command server runs PathMaxing as one binary: the redirect service, the
// management API and the background workers.
//
// Each part of the codebase plugs in from its own module_<part>.go file in
// this package, by calling app.Register from init(). This file never changes
// when a part is added, which is what lets every commit build on its own.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"pathmaxing/internal/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pathmaxing:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := app.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := app.NewLogger(cfg, os.Stdout)
	if cfg.SecretGenerated {
		log.Warn("SECRET_KEY is not set; using a random key, so unlock cookies and visitor hashes reset on every restart")
	}

	// SIGTERM is what Render (and docker stop) send before killing the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, cfg, log, app.Modules())
}
