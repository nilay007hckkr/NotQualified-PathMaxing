// Package api is the management API under /api/: accounts, links and the
// rest of what the web app manages. Every handler that touches data runs as
// an authenticated principal, and everything is scoped to that principal's
// workspace; other users' resources answer 404, never 403.
package api

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"pathmaxing/internal/admin"
	"pathmaxing/internal/analytics"
	"pathmaxing/internal/auth"
	"pathmaxing/internal/clicks"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
	"pathmaxing/internal/ratelimit"
	"pathmaxing/internal/store"
)

// Server holds the API's dependencies. Build it with the services the module
// registry resolved, then call Register.
type Server struct {
	Auth  *auth.Service
	Guard domain.AuthGuard
	Store *store.Store
	Trust domain.TrustChecker
	Cache domain.CacheInvalidator
	// Analytics answers the dashboard; Live, when set, feeds the SSE stream.
	Analytics *analytics.Store
	Live      *clicks.Broker
	// Stopping closes when shutdown begins, ending live streams.
	Stopping <-chan struct{}
	// Admin runs takedowns and blocklist changes.
	Admin *admin.Service
	// Alerts, when set, fills the overview's alerts_open.
	Alerts domain.AlertCounter
	// Rate limits; nil means unlimited (tests).
	CreateLimit, ImportLimit, LoginLimit *ratelimit.Limiter
	// ProxyHops is TRUSTED_PROXY_HOPS, for the client IP the login limit keys on.
	ProxyHops int
	// BaseURL is where short links live, without a trailing slash.
	BaseURL string
	Log     *slog.Logger
	Now     func() time.Time // time.Now when nil
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Register mounts the API routes.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/signup", s.signup)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/me", s.me)
	s.registerLinks(mux)
	s.registerOrg(mux)
	s.registerAnalytics(mux)
	s.registerQRBio(mux)
	s.registerImports(mux)
	s.registerAdmin(mux)
}

// authedFunc is a handler that runs only for an authenticated principal.
type authedFunc func(w http.ResponseWriter, r *http.Request, p domain.Principal)

func (s *Server) authed(h authedFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Guard.Authenticate(r)
		if err != nil {
			if !errors.Is(err, domain.ErrUnauthenticated) {
				s.Log.Error("authenticate", "err", err)
			}
			httpx.WriteError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "log in to continue")
			return
		}
		h(w, r, p)
	}
}

// fail answers err with the status its kind calls for. Unexpected errors are
// logged and answered without detail.
func (s *Server) fail(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, httpx.ErrBadRequest):
		httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case errors.Is(err, domain.ErrConflict):
		httpx.WriteError(w, http.StatusConflict, "CONFLICT", err.Error())
	default:
		s.Log.Error("api error", "what", what, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "INTERNAL", "something went wrong; please try again")
	}
}

// pathID reads the {id} path value.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// Issue is one validation error or warning, as API.md shows them.
type Issue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Validation is the body of validate responses and of 422s.
type Validation struct {
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}

func newValidation() Validation { return Validation{Errors: []Issue{}, Warnings: []Issue{}} }

func (v *Validation) err(field, code, msg string) {
	v.Errors = append(v.Errors, Issue{field, code, msg})
}
func (v *Validation) warn(field, code, msg string) {
	v.Warnings = append(v.Warnings, Issue{field, code, msg})
}
func (v Validation) ok() bool { return len(v.Errors) == 0 }

// issueCodes lists the codes of issues, for places that only report codes.
func issueCodes(issues []Issue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

// limited takes a token for key and, when none is left, answers 429 with
// Retry-After and reports true.
func limited(w http.ResponseWriter, l *ratelimit.Limiter, key string) bool {
	if l == nil {
		return false
	}
	ok, retry := l.Allow(key)
	if ok {
		return false
	}
	secs := int(math.Ceil(retry.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	httpx.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", fmt.Sprintf("too many requests; try again in %d seconds", secs))
	return true
}

func userKey(p domain.Principal) string { return "user:" + strconv.FormatInt(p.UserID, 10) }
