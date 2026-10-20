package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pathmaxing/internal/auth"
	"pathmaxing/internal/domain"
)

func TestSignupLoginLogoutMe(t *testing.T) {
	e := newEnv(t)
	c, u := e.signup("Asha@Example.com")
	if u.ID == 0 || u.WorkspaceID == 0 || u.Role != domain.RoleUser || u.Email != "Asha@Example.com" {
		t.Fatalf("signup user = %+v", u)
	}

	r := c.do("GET", "/api/auth/me", nil)
	var me struct{ User auth.User }
	r.json(t, &me)
	if r.status != http.StatusOK || me.User != u {
		t.Fatalf("me = %d %+v, want %+v", r.status, me.User, u)
	}

	if r := c.do("POST", "/api/auth/logout", nil); r.status != http.StatusNoContent {
		t.Fatalf("logout = %d", r.status)
	}
	if r := c.do("GET", "/api/auth/me", nil); r.status != http.StatusUnauthorized || r.errorCode(t) != "UNAUTHENTICATED" {
		t.Fatalf("me after logout = %d %s", r.status, r.body)
	}

	// Email matching ignores case, both for login and for signing up twice.
	if r := c.do("POST", "/api/auth/login", map[string]string{"email": "asha@example.COM", "password": "correct horse"}); r.status != http.StatusOK {
		t.Fatalf("login = %d %s", r.status, r.body)
	}
	if r := c.do("GET", "/api/auth/me", nil); r.status != http.StatusOK {
		t.Fatalf("me after login = %d", r.status)
	}
	dup := e.client().do("POST", "/api/auth/signup", map[string]string{"email": "ASHA@example.com", "password": "another one", "name": "x"})
	if dup.status != http.StatusConflict || dup.errorCode(t) != "EMAIL_TAKEN" {
		t.Fatalf("duplicate signup = %d %s", dup.status, dup.body)
	}
}

func TestSessionCookie(t *testing.T) {
	e := newEnv(t)
	r := e.client().do("POST", "/api/auth/signup", map[string]string{"email": "c@example.com", "password": "12345678", "name": ""})
	cookie := r.header.Get("Set-Cookie")
	for _, want := range []string{"lp_session=", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie %q lacks %q", cookie, want)
		}
	}
	// Only a hash of the token is stored.
	token := strings.TrimPrefix(strings.SplitN(cookie, ";", 2)[0], "lp_session=")
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM sessions WHERE token_hash = $1::text::bytea`, token).Scan(&n); err != nil || n != 0 {
		t.Fatalf("raw token found in sessions: %d, %v", n, err)
	}

	// After logout the old cookie value is useless, even when replayed.
	c, _ := e.signup("d@example.com")
	stolen := c.hc.Jar.Cookies(mustURL(e.http.URL))
	c.do("POST", "/api/auth/logout", nil)
	replay := e.client()
	replay.hc.Jar.SetCookies(mustURL(e.http.URL), stolen)
	if r := replay.do("GET", "/api/auth/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("replayed cookie after logout = %d", r.status)
	}
}

func TestSignupValidation(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name, email, password, field, code string
	}{
		{"no at sign", "nobody", "12345678", "email", "EMAIL_INVALID"},
		{"no domain dot", "a@localhost", "12345678", "email", "EMAIL_INVALID"},
		{"display name", "Asha <a@example.com>", "12345678", "email", "EMAIL_INVALID"},
		{"short password", "a@example.com", "1234567", "password", "PASSWORD_TOO_SHORT"},
		{"over bcrypt's limit", "a@example.com", strings.Repeat("x", 73), "password", "PASSWORD_TOO_LONG"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.client().do("POST", "/api/auth/signup", map[string]string{"email": tt.email, "password": tt.password, "name": "x"})
			var v Validation
			r.json(t, &v)
			if r.status != http.StatusUnprocessableEntity || len(v.Errors) != 1 || v.Errors[0].Field != tt.field || v.Errors[0].Code != tt.code {
				t.Fatalf("= %d %s, want 422 %s on %s", r.status, r.body, tt.code, tt.field)
			}
		})
	}
}

func TestLoginFailuresLookAlike(t *testing.T) {
	e := newEnv(t)
	e.signup("known@example.com")
	var bodies []string
	for _, email := range []string{"known@example.com", "unknown@example.com"} {
		r := e.client().do("POST", "/api/auth/login", map[string]string{"email": email, "password": "wrong password"})
		if r.status != http.StatusUnauthorized || r.errorCode(t) != "BAD_CREDENTIALS" {
			t.Fatalf("login %s = %d %s", email, r.status, r.body)
		}
		bodies = append(bodies, string(r.body))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("unknown email and wrong password answer differently: %q vs %q", bodies[0], bodies[1])
	}
}

func TestOriginCheck(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("o@example.com")

	// A cross-site form can't log someone in or out.
	evil := e.client()
	evil.origin = "https://evil.example"
	if r := evil.do("POST", "/api/auth/login", map[string]string{"email": "o@example.com", "password": "correct horse"}); r.status != http.StatusForbidden {
		t.Fatalf("cross-site login = %d", r.status)
	}
	c.origin = "https://evil.example"
	if r := c.do("POST", "/api/auth/logout", nil); r.status != http.StatusForbidden {
		t.Fatalf("cross-site logout = %d", r.status)
	}

	// The guard: a valid session is not enough for a cross-site POST.
	cookies := c.hc.Jar.Cookies(mustURL(e.http.URL))
	tests := []struct {
		name, method, origin, referer string
		ok                            bool
	}{
		{"same-origin POST", "POST", testOrigin, "", true},
		{"cross-site POST", "POST", "https://evil.example", "", false},
		{"POST with only a same-origin Referer", "POST", "", testOrigin + "/app/links", true},
		{"POST with a cross-site Referer", "POST", "", "https://evil.example/page", false},
		{"POST with neither header", "DELETE", "", "", false},
		{"GET needs no origin", "GET", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "/api/links", nil)
			for _, ck := range cookies {
				r.AddCookie(ck)
			}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if tt.referer != "" {
				r.Header.Set("Referer", tt.referer)
			}
			p, err := e.auth.Authenticate(r)
			if tt.ok && (err != nil || p.UserID == 0) {
				t.Fatalf("refused: %v", err)
			}
			if !tt.ok && !errors.Is(err, domain.ErrUnauthenticated) {
				t.Fatalf("allowed: %+v, %v", p, err)
			}
		})
	}
}

func TestRoles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, u := e.signup("promote@example.com")
	if u.Role != domain.RoleUser {
		t.Fatalf("new account role = %q", u.Role)
	}
	// Seeding an admin creates the account, and promotes an existing one
	// without touching its password.
	admin := e.admin("root@example.com")
	var me struct{ User auth.User }
	admin.do("GET", "/api/auth/me", nil).json(t, &me)
	if me.User.Role != domain.RoleAdmin {
		t.Fatalf("seeded admin role = %q", me.User.Role)
	}
	if err := e.auth.EnsureAdmin(ctx, "PROMOTE@example.com", "ignored password"); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	r := c.do("POST", "/api/auth/login", map[string]string{"email": "promote@example.com", "password": "correct horse"})
	r.json(t, &me)
	if r.status != http.StatusOK || me.User.Role != domain.RoleAdmin {
		t.Fatalf("promoted login = %d role %q", r.status, me.User.Role)
	}
	if err := e.auth.EnsureAdmin(ctx, "root@example.com", "admin password"); err != nil {
		t.Fatalf("seeding twice: %v", err)
	}
}
