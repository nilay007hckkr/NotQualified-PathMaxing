// Package app is the composition root's toolkit. Each part of PathMaxing adds
// a Module from an init() function in cmd/server/module_<part>.go; app runs
// every module's hooks in phases, keeps a typed container of the services
// modules hand to each other, and owns the server lifecycle.
//
// The phases are what let a part be committed without editing anyone else's
// code: in-memory defaults are provided first and replaced by later modules,
// wrappers such as the link cache are added once every base implementation
// exists, and routes only ever see the final services.
package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"sync"
)

// Module is one part's contribution to the server. Every hook is optional.
//
// Hooks run phase by phase across all modules, each phase in Priority order
// (then by Name): every Provide, then every Wire, then every Routes, then
// every Start. During shutdown the Stop hooks run in reverse order.
type Module struct {
	// Name identifies the module in logs and errors. It must be unique.
	Name string
	// Priority orders the module within each phase, lowest first. A later
	// module's Provide replaces an earlier one's, which is how the real
	// implementations replace the in-memory defaults.
	Priority int

	// Provide registers implementations with Provide.
	Provide func(ctx context.Context, a *App) error
	// Wire decorates services that other modules provided, for example by
	// wrapping the link store in a cache. Every Provide has run by now.
	Wire func(ctx context.Context, a *App) error
	// Routes mounts HTTP handlers. Services are final by now.
	Routes func(a *App, mux *http.ServeMux) error
	// Start launches background workers with a.Go. It must not block.
	Start func(ctx context.Context, a *App) error
	// Stop releases resources after HTTP has stopped and the workers have
	// returned. It must be safe to call even if the other hooks never ran.
	Stop func(ctx context.Context) error
}

// The registry is the only package-level mutable state in PathMaxing.
// Modules add themselves from init(), before main runs, so it is written
// once at startup and only read afterwards.
var (
	registryMu sync.Mutex
	registry   []Module
)

// Register adds a module to the server. Call it from an init() function.
func Register(m Module) {
	if m.Name == "" {
		panic("app: Register called with an unnamed module")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, r := range registry {
		if r.Name == m.Name {
			panic(fmt.Sprintf("app: module %q registered twice", m.Name))
		}
	}
	registry = append(registry, m)
}

// Modules returns every registered module in hook order.
func Modules() []Module {
	registryMu.Lock()
	defer registryMu.Unlock()
	ms := slices.Clone(registry)
	sortModules(ms)
	return ms
}

func sortModules(ms []Module) {
	slices.SortStableFunc(ms, func(a, b Module) int {
		return cmp.Or(cmp.Compare(a.Priority, b.Priority), cmp.Compare(a.Name, b.Name))
	})
}

// App is what the hooks receive: configuration, the logger, the service
// container, background workers and health checks.
type App struct {
	Config Config
	Log    *slog.Logger

	mu       sync.RWMutex
	services map[reflect.Type]service
	checks   []healthCheck
	module   string // the module whose hook is running, for logs

	workerCtx context.Context
	workers   sync.WaitGroup
	workerErr chan error
	stopping  chan struct{}
	stopOnce  sync.Once
}

type service struct {
	value  any
	module string
}

type healthCheck struct {
	name string
	fn   func(ctx context.Context) error
}

func newApp(cfg Config, log *slog.Logger) *App {
	return &App{
		Config:    cfg,
		Log:       log,
		services:  make(map[reflect.Type]service),
		workerErr: make(chan error, 1),
		stopping:  make(chan struct{}),
	}
}

func (a *App) setModule(name string) {
	a.mu.Lock()
	a.module = name
	a.mu.Unlock()
}

// Provide registers v as the implementation of T, replacing any earlier one.
// T is usually an interface from internal/domain, so a module can replace a
// default without knowing which module provided it.
func Provide[T any](a *App, v T) {
	t := reflect.TypeFor[T]()
	a.mu.Lock()
	defer a.mu.Unlock()
	if prev, ok := a.services[t]; ok {
		a.Log.Info("service replaced", "service", t.String(), "by", a.module, "previous", prev.module)
	} else {
		a.Log.Debug("service provided", "service", t.String(), "by", a.module)
	}
	a.services[t] = service{value: v, module: a.module}
}

// Get returns the implementation of T, or an error naming the missing
// service. Resolve services in hooks rather than per request, so a missing
// one stops the server at startup instead of failing a visitor later.
func Get[T any](a *App) (T, error) {
	v, ok := Lookup[T](a)
	if !ok {
		return v, fmt.Errorf("app: no %s has been provided", reflect.TypeFor[T]())
	}
	return v, nil
}

// Lookup returns the implementation of T and whether one was provided, for
// services that are optional.
func Lookup[T any](a *App) (T, bool) {
	a.mu.RLock()
	s, ok := a.services[reflect.TypeFor[T]()]
	a.mu.RUnlock()
	if !ok {
		var zero T
		return zero, false
	}
	return s.value.(T), true
}

// Go runs fn as a background worker. Its context is cancelled only after the
// HTTP server has stopped, so no request can add work (such as a click event)
// once the worker has started draining. A worker that fails while the server
// is running shuts the server down. Call Go from a Start hook.
func (a *App) Go(name string, fn func(ctx context.Context) error) {
	if a.workerCtx == nil {
		panic("app: Go called outside a Start hook")
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		err := fn(a.workerCtx)
		switch {
		case err == nil || errors.Is(err, context.Canceled):
		case a.workerCtx.Err() == nil:
			select {
			case a.workerErr <- fmt.Errorf("worker %s: %w", name, err):
			default: // another worker already triggered the shutdown
			}
		default:
			a.Log.Error("worker stopped with an error", "worker", name, "err", err)
		}
	}()
}

// AddCheck registers a check that GET /healthz runs on every request.
func (a *App) AddCheck(name string, fn func(ctx context.Context) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checks = append(a.checks, healthCheck{name: name, fn: fn})
}

func (a *App) healthChecks() []healthCheck {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return slices.Clone(a.checks)
}

// Stopping is closed when shutdown begins. Long-lived responses such as
// Server-Sent Events must return when it closes, because
// http.Server.Shutdown waits for open requests rather than cancelling them.
func (a *App) Stopping() <-chan struct{} {
	return a.stopping
}

func (a *App) markStopping() {
	a.stopOnce.Do(func() { close(a.stopping) })
}
