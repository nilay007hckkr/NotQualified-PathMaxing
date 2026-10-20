package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"
)

// Server is a fully wired application: every module's Provide, Wire and
// Routes hooks have run, and it is ready to start its workers and serve.
type Server struct {
	app     *App
	modules []Module
	handler http.Handler
}

// Run builds the server from modules, listens on cfg.Addr and serves until
// ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, cfg Config, log *slog.Logger, modules []Module) error {
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	s, err := Build(ctx, cfg, log, modules)
	if err != nil {
		ln.Close()
		return err
	}
	return s.Run(ctx, ln)
}

// Build runs the Provide, Wire and Routes phases. If a hook fails, Build
// calls the Stop hooks of every module it reached (so a database pool opened
// in Provide is closed) and returns the error.
func Build(ctx context.Context, cfg Config, log *slog.Logger, modules []Module) (*Server, error) {
	modules = slices.Clone(modules)
	sortModules(modules)
	a := newApp(cfg, log)
	s := &Server{app: a, modules: modules}

	phases := []struct {
		name string
		run  func(m Module) error
	}{
		{"provide", func(m Module) error {
			if m.Provide == nil {
				return nil
			}
			return m.Provide(ctx, a)
		}},
		{"wire", func(m Module) error {
			if m.Wire == nil {
				return nil
			}
			return m.Wire(ctx, a)
		}},
	}
	for _, p := range phases {
		for i, m := range modules {
			a.setModule(m.Name)
			if err := p.run(m); err != nil {
				reached := len(modules)
				if p.name == "provide" {
					reached = i + 1
				}
				return nil, s.abort(fmt.Errorf("module %s: %s: %w", m.Name, p.name, err), reached)
			}
		}
	}

	mux := http.NewServeMux()
	a.setModule("app")
	if err := mountBuiltins(a, mux); err != nil {
		return nil, s.abort(err, len(modules))
	}
	for _, m := range modules {
		if m.Routes == nil {
			continue
		}
		a.setModule(m.Name)
		if err := m.Routes(a, mux); err != nil {
			return nil, s.abort(fmt.Errorf("module %s: routes: %w", m.Name, err), len(modules))
		}
	}
	s.handler = recoverPanics(log, mux)
	return s, nil
}

// Handler returns the server's HTTP handler, for tests.
func (s *Server) Handler() http.Handler { return s.handler }

// Run starts the workers, serves on ln until ctx is cancelled (or a worker
// fails), and then shuts down in this order:
//
//  1. HTTP stops accepting connections and finishes in-flight requests.
//  2. Workers are cancelled and waited for, so they drain what those last
//     requests queued (for example click events).
//  3. Stop hooks run in reverse order, so the database closes last.
func (s *Server) Run(ctx context.Context, ln net.Listener) error {
	a := s.app
	// The workers outlive ctx on purpose: they must keep running until HTTP
	// has stopped, so their context is cancelled by shutdown, not by ctx.
	workerCtx, cancelWorkers := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWorkers()
	a.workerCtx = workerCtx

	for _, m := range s.modules {
		if m.Start == nil {
			continue
		}
		a.setModule(m.Name)
		if err := m.Start(ctx, a); err != nil {
			ln.Close()
			cancelWorkers()
			a.workers.Wait()
			return s.abort(fmt.Errorf("module %s: start: %w", m.Name, err), len(s.modules))
		}
	}

	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(a.Log.Handler(), slog.LevelWarn),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	a.Log.Info("listening", "addr", ln.Addr().String(), "base_url", a.Config.BaseURL)

	var runErr error
	select {
	case <-ctx.Done():
		a.Log.Info("shutting down")
	case err := <-serveErr:
		runErr = fmt.Errorf("http server: %w", err)
	case err := <-a.workerErr:
		runErr = err
		a.Log.Error("a worker failed; shutting down", "err", err)
	}
	return errors.Join(runErr, s.shutdown(srv, cancelWorkers))
}

func (s *Server) shutdown(srv *http.Server, cancelWorkers context.CancelFunc) error {
	a := s.app
	timeout := a.Config.ShutdownTimeout
	var errs []error

	a.markStopping()
	httpCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := srv.Shutdown(httpCtx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		srv.Close()
	}

	cancelWorkers()
	done := make(chan struct{})
	go func() {
		a.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		errs = append(errs, fmt.Errorf("workers still running after %s", timeout))
	}

	stopCtx, cancelStop := context.WithTimeout(context.Background(), timeout)
	defer cancelStop()
	errs = append(errs, s.stopModules(stopCtx, len(s.modules))...)
	if len(errs) == 0 {
		a.Log.Info("stopped cleanly")
	}
	return errors.Join(errs...)
}

// abort stops the first n modules after a failed startup and returns err
// joined with any Stop errors.
func (s *Server) abort(err error, n int) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.app.Config.ShutdownTimeout)
	defer cancel()
	return errors.Join(append([]error{err}, s.stopModules(ctx, n)...)...)
}

func (s *Server) stopModules(ctx context.Context, n int) []error {
	var errs []error
	for i := n - 1; i >= 0; i-- {
		m := s.modules[i]
		if m.Stop == nil {
			continue
		}
		if err := m.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("module %s: stop: %w", m.Name, err))
		}
	}
	return errs
}

// recoverPanics turns a panicking handler into a 500 for that request, logs
// it, and keeps the server alive for everyone else.
func recoverPanics(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("handler panicked", "method", r.Method, "path", r.URL.Path, "panic", v)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
