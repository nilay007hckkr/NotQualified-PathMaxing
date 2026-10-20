package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"pathmaxing/internal/auth"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/migrate/pgtest"
	"pathmaxing/internal/store"
)

const testOrigin = "http://pm.test"

// env is one API server on a throwaway schema.
type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	auth  *auth.Service
	store *store.Store
	trust *fakeTrust
	cache *recordingCache
	srv   *Server
	http  *httptest.Server
}

// fakeTrust answers prechecks from a table instead of the network.
type fakeTrust struct {
	mu      sync.Mutex
	answers map[string]domain.Precheck
}

func (f *fakeTrust) set(url string, pc domain.Precheck) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[url] = pc
}

func (f *fakeTrust) Precheck(_ context.Context, url string) (domain.Precheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answers[url], nil
}

// recordingCache remembers which codes were invalidated.
type recordingCache struct {
	mu    sync.Mutex
	codes []string
}

func (c *recordingCache) Invalidate(code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes = append(c.codes, code)
}

func (c *recordingCache) invalidated() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.codes...)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := auth.New(pool, auth.Options{TTL: time.Hour, AllowedOrigins: []string{testOrigin}, Log: log, Cost: bcrypt.MinCost})
	st, err := store.New(context.Background(), pool, log)
	if err != nil {
		t.Fatal(err)
	}
	trust := &fakeTrust{answers: map[string]domain.Precheck{}}
	cache := &recordingCache{}
	srv := &Server{Auth: svc, Guard: svc, Store: st, Trust: trust, Cache: cache, BaseURL: testOrigin, Log: log}
	e := &env{t: t, pool: pool, auth: svc, store: st, trust: trust, cache: cache, srv: srv}
	mux := http.NewServeMux()
	srv.Register(mux)
	e.http = httptest.NewServer(mux)
	t.Cleanup(e.http.Close)
	return e
}

// client is a browser: a cookie jar, and our Origin on every request.
type client struct {
	e      *env
	hc     *http.Client
	origin string
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, hc: &http.Client{Jar: jar}, origin: testOrigin}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

// errorCode returns the code of an {"error": ...} body.
func (r response) errorCode(t *testing.T) string {
	t.Helper()
	var b struct {
		Error struct{ Code string } `json:"error"`
	}
	r.json(t, &b)
	return b.Error.Code
}

func (c *client) do(method, path string, body any) response {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.e.http.URL+path, rd)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: b}
}

// signup creates an account and returns a logged-in client and the user.
func (e *env) signup(email string) (*client, auth.User) {
	e.t.Helper()
	c := e.client()
	r := c.do("POST", "/api/auth/signup", map[string]string{"email": email, "password": "correct horse", "name": "Test"})
	if r.status != http.StatusCreated {
		e.t.Fatalf("signup %s = %d %s", email, r.status, r.body)
	}
	var out struct{ User auth.User }
	r.json(e.t, &out)
	return c, out.User
}

// admin creates an admin account and returns a logged-in client.
func (e *env) admin(email string) *client {
	e.t.Helper()
	if err := e.auth.EnsureAdmin(context.Background(), email, "admin password"); err != nil {
		e.t.Fatal(err)
	}
	c := e.client()
	if r := c.do("POST", "/api/auth/login", map[string]string{"email": email, "password": "admin password"}); r.status != http.StatusOK {
		e.t.Fatalf("admin login = %d %s", r.status, r.body)
	}
	return c
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
