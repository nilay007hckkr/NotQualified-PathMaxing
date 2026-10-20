package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	cfg.ShutdownTimeout = 5 * time.Second
	return cfg
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// events records hook calls from several goroutines.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) add(s string) {
	e.mu.Lock()
	e.list = append(e.list, s)
	e.mu.Unlock()
}

func (e *events) get() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.list)
}

// recorder returns a module whose hooks append "<hook> <name>" to ev.
func recorder(name string, priority int, ev *events) Module {
	return Module{
		Name:     name,
		Priority: priority,
		Provide:  func(context.Context, *App) error { ev.add("provide " + name); return nil },
		Wire:     func(context.Context, *App) error { ev.add("wire " + name); return nil },
		Routes:   func(*App, *http.ServeMux) error { ev.add("routes " + name); return nil },
		Start:    func(context.Context, *App) error { ev.add("start " + name); return nil },
		Stop:     func(context.Context) error { ev.add("stop " + name); return nil },
	}
}

// serve runs s on a local port and returns its base URL and a function that
// shuts it down and returns Run's error.
func serve(t *testing.T, s *Server) (string, func() error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, ln) }()
	stop := func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("server did not stop")
			return nil
		}
	}
	return "http://" + ln.Addr().String(), stop
}

func TestPhasesRunInPriorityOrder(t *testing.T) {
	ev := &events{}
	// Registered out of order on purpose; equal priorities sort by name.
	mods := []Module{recorder("c", 20, ev), recorder("a", 10, ev), recorder("b", 20, ev)}
	s, err := Build(context.Background(), testConfig(t), quietLog(), mods)
	if err != nil {
		t.Fatal(err)
	}
	_, stop := serve(t, s)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"provide a", "provide b", "provide c",
		"wire a", "wire b", "wire c",
		"routes a", "routes b", "routes c",
		"start a", "start b", "start c",
		"stop c", "stop b", "stop a",
	}
	if got := ev.get(); !slices.Equal(got, want) {
		t.Fatalf("hook order\n got %v\nwant %v", got, want)
	}
}

type greeter interface{ Greet() string }

type fixedGreeter string

func (g fixedGreeter) Greet() string { return string(g) }

func TestLaterProvideReplacesEarlier(t *testing.T) {
	var got string
	mods := []Module{
		{Name: "real", Priority: 40, Provide: func(_ context.Context, a *App) error {
			Provide[greeter](a, fixedGreeter("real"))
			return nil
		}},
		{Name: "default", Priority: 10, Provide: func(_ context.Context, a *App) error {
			Provide[greeter](a, fixedGreeter("default"))
			return nil
		}},
		{Name: "user", Priority: 50, Wire: func(_ context.Context, a *App) error {
			g, err := Get[greeter](a)
			if err != nil {
				return err
			}
			got = g.Greet()
			return nil
		}},
	}
	if _, err := Build(context.Background(), testConfig(t), quietLog(), mods); err != nil {
		t.Fatal(err)
	}
	if got != "real" {
		t.Fatalf("Get returned %q, want the later module's %q", got, "real")
	}
}

func TestGetMissingService(t *testing.T) {
	mods := []Module{{Name: "needy", Wire: func(_ context.Context, a *App) error {
		_, err := Get[greeter](a)
		return err
	}}}
	_, err := Build(context.Background(), testConfig(t), quietLog(), mods)
	if err == nil || !strings.Contains(err.Error(), "app.greeter") || !strings.Contains(err.Error(), "module needy: wire") {
		t.Fatalf("Build error = %v, want it to name the module, the phase and the missing service", err)
	}
	if _, ok := Lookup[greeter](newApp(testConfig(t), quietLog())); ok {
		t.Fatal("Lookup found a service nobody provided")
	}
}

func TestFailedStartupStopsWhatRan(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name     string
		fail     func(m *Module)
		wantStop []string
	}{
		{
			name:     "provide fails: only modules that were reached stop",
			fail:     func(m *Module) { m.Provide = func(context.Context, *App) error { return boom } },
			wantStop: []string{"stop b", "stop a"},
		},
		{
			name:     "wire fails: every module stops",
			fail:     func(m *Module) { m.Wire = func(context.Context, *App) error { return boom } },
			wantStop: []string{"stop c", "stop b", "stop a"},
		},
		{
			name:     "routes fails: every module stops",
			fail:     func(m *Module) { m.Routes = func(*App, *http.ServeMux) error { return boom } },
			wantStop: []string{"stop c", "stop b", "stop a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := &events{}
			b := recorder("b", 2, ev)
			tt.fail(&b)
			mods := []Module{recorder("a", 1, ev), b, recorder("c", 3, ev)}
			_, err := Build(context.Background(), testConfig(t), quietLog(), mods)
			if !errors.Is(err, boom) {
				t.Fatalf("Build error = %v, want boom", err)
			}
			var stops []string
			for _, e := range ev.get() {
				if strings.HasPrefix(e, "stop") {
					stops = append(stops, e)
				}
			}
			if !slices.Equal(stops, tt.wantStop) {
				t.Fatalf("stops = %v, want %v", stops, tt.wantStop)
			}
		})
	}
}

// TestShutdownStopsHTTPBeforeWorkers checks the order that keeps clicks from
// being lost: an in-flight request finishes while the worker keeps running,
// then the worker drains, then Stop runs.
func TestShutdownStopsHTTPBeforeWorkers(t *testing.T) {
	ev := &events{}
	inRequest := make(chan struct{})
	release := make(chan struct{})
	mod := Module{
		Name: "m",
		Routes: func(a *App, mux *http.ServeMux) error {
			mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
				close(inRequest)
				<-release
				ev.add("request finished")
			})
			return nil
		},
		Start: func(_ context.Context, a *App) error {
			a.Go("drainer", func(ctx context.Context) error {
				<-ctx.Done()
				ev.add("worker drained")
				return nil
			})
			return nil
		},
		Stop: func(context.Context) error { ev.add("stopped"); return nil },
	}
	s, err := Build(context.Background(), testConfig(t), quietLog(), []Module{mod})
	if err != nil {
		t.Fatal(err)
	}
	base, stop := serve(t, s)

	respErr := make(chan error, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err == nil {
			resp.Body.Close()
		}
		respErr <- err
	}()
	<-inRequest
	stopErr := make(chan error, 1)
	go func() { stopErr <- stop() }()

	// Shutdown has begun, but the request is still running, so the worker
	// must still be running too.
	time.Sleep(100 * time.Millisecond)
	if got := ev.get(); len(got) != 0 {
		t.Fatalf("events before the request finished: %v", got)
	}
	close(release)
	if err := <-respErr; err != nil {
		t.Fatalf("in-flight request failed during shutdown: %v", err)
	}
	if err := <-stopErr; err != nil {
		t.Fatal(err)
	}
	want := []string{"request finished", "worker drained", "stopped"}
	if got := ev.get(); !slices.Equal(got, want) {
		t.Fatalf("shutdown order %v, want %v", got, want)
	}
}

func TestFailingWorkerShutsServerDown(t *testing.T) {
	boom := errors.New("queue broke")
	mod := Module{Name: "m", Start: func(_ context.Context, a *App) error {
		a.Go("writer", func(context.Context) error { return boom })
		return nil
	}}
	s, err := Build(context.Background(), testConfig(t), quietLog(), []Module{mod})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), ln) }()
	select {
	case err := <-done:
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "worker writer") {
			t.Fatalf("Run error = %v, want the worker's error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server kept running after a worker failed")
	}
}

func TestStoppingClosesWhenShutdownBegins(t *testing.T) {
	started := make(chan *App, 1)
	mod := Module{Name: "m", Start: func(_ context.Context, app *App) error { started <- app; return nil }}
	s, err := Build(context.Background(), testConfig(t), quietLog(), []Module{mod})
	if err != nil {
		t.Fatal(err)
	}
	_, stop := serve(t, s)
	a := <-started
	select {
	case <-a.Stopping():
		t.Fatal("Stopping closed while serving")
	default:
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Stopping():
	default:
		t.Fatal("Stopping still open after shutdown")
	}
}

func TestPanickingHandlerReturns500(t *testing.T) {
	mod := Module{Name: "m", Routes: func(a *App, mux *http.ServeMux) error {
		mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) { panic("bug") })
		return nil
	}}
	s, err := Build(context.Background(), testConfig(t), quietLog(), []Module{mod})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

func TestRegister(t *testing.T) {
	saved := registry
	t.Cleanup(func() { registry = saved })
	registry = nil

	Register(Module{Name: "z", Priority: 1})
	Register(Module{Name: "y", Priority: 1})
	Register(Module{Name: "x", Priority: 0})
	var names []string
	for _, m := range Modules() {
		names = append(names, m.Name)
	}
	if want := []string{"x", "y", "z"}; !slices.Equal(names, want) {
		t.Fatalf("Modules() = %v, want %v", names, want)
	}

	for _, m := range []Module{{Name: "z"}, {}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q) did not panic", m.Name)
				}
			}()
			Register(m)
		}()
	}
}
