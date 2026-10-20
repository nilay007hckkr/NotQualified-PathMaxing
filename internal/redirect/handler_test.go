package redirect

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pathmaxing/internal/classify"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/linkcache"
	"pathmaxing/internal/memstore"
	"pathmaxing/internal/ratelimit"
	"pathmaxing/internal/rules"
	"pathmaxing/internal/secret"
)

const (
	chromeMobile = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.6668.81 Mobile Safari/537.36"
	chromeDesk   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	whatsApp     = "WhatsApp/2.23.20.0"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type env struct {
	h      *Handler
	mux    *http.ServeMux
	store  *memstore.Store
	clicks *memstore.ClickLog
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := memstore.New()
	store.BlockDomain("evil.example", "phishing")
	clicks := memstore.NewClickLog(100)
	keys := secret.NewKeys(strings.Repeat("k", 32))
	h := &Handler{
		Links:           store,
		Engine:          &rules.Engine{Blocklist: store, Caps: store},
		Classifier:      classify.Simple{},
		Clicks:          clicks,
		Geo:             domain.NoGeo{},
		Unlocker:        keys.NewUnlocker(false),
		Visitors:        keys.NewVisitors(),
		PasswordLimiter: ratelimit.New(10, 15*time.Minute),
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:             func() time.Time { return now },
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return &env{h: h, mux: mux, store: store, clicks: clicks}
}

func (e *env) put(t *testing.T, l domain.Link) domain.Link {
	t.Helper()
	if l.Destination == "" {
		l.Destination = "https://shop.example/sale"
	}
	stored, err := e.store.Put(l)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func (e *env) do(method, target, ua string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("User-Agent", ua)
	for _, m := range mutate {
		m(r)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, r)
	return rec
}

func (e *env) last(t *testing.T) domain.ClickEvent {
	t.Helper()
	ev := e.clicks.Events()
	if len(ev) == 0 {
		t.Fatal("no click event recorded")
	}
	return ev[len(ev)-1]
}

func TestRedirect(t *testing.T) {
	e := newEnv(t)
	l := e.put(t, domain.Link{Code: "Diwali", WorkspaceID: 3, Targets: []domain.Target{{Device: domain.DeviceMobile, Destination: "https://m.shop.example/sale"}}})

	rec := e.do("GET", "/diwali?s=qr", chromeMobile, func(r *http.Request) {
		r.Header.Set("Referer", "https://www.instagram.com/p/xyz?utm=1")
		r.RemoteAddr = "203.0.113.7:4000"
	})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://m.shop.example/sale" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("Cache-Control %q", cc)
	}
	ev := e.last(t)
	if ev.LinkID != l.ID || ev.WorkspaceID != 3 || ev.Outcome != domain.OutcomeRedirected || ev.Class != domain.ClassHuman ||
		ev.Source != domain.SourceQR || ev.Device != domain.DeviceMobile || ev.Browser != "Chrome" || ev.OS != "Android" ||
		ev.ReferrerHost != "instagram.com" || ev.Method != "GET" || ev.EventID == "" || !ev.At.Equal(now) {
		t.Fatalf("event %+v", ev)
	}
	if ev.VisitorHash == "" || strings.Contains(ev.VisitorHash, "203.0.113.7") {
		t.Fatalf("visitor hash %q", ev.VisitorHash)
	}

	rec = e.do("GET", "/DIWALI", chromeDesk)
	if rec.Header().Get("Location") != "https://shop.example/sale" || e.last(t).Source != domain.SourceLink {
		t.Fatalf("desktop visit: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestPreviewsAndHeadAreBots(t *testing.T) {
	e := newEnv(t)
	one := int64(1)
	l := e.put(t, domain.Link{Code: "once", MaxClicks: &one})

	// WhatsApp fetches the link to build a preview: redirected, but the
	// one-time link is not used up.
	if rec := e.do("GET", "/once", whatsApp); rec.Code != http.StatusFound {
		t.Fatalf("preview: %d", rec.Code)
	}
	if ev := e.last(t); ev.Class != domain.ClassLinkPreview || ev.Device != "" {
		t.Fatalf("preview event %+v", ev)
	}
	if rec := e.do("HEAD", "/once", chromeDesk); rec.Code != http.StatusFound || e.last(t).Class != domain.ClassOtherBot || e.last(t).Method != "HEAD" {
		t.Fatalf("HEAD: %d %+v", rec.Code, e.last(t))
	}
	got, _ := e.store.GetLink(t.Context(), l.ID)
	if got.CapUsed != 0 {
		t.Fatalf("bots used %d clicks", got.CapUsed)
	}
	if rec := e.do("GET", "/once", chromeDesk); rec.Code != http.StatusFound {
		t.Fatalf("first human: %d", rec.Code)
	}
	if rec := e.do("GET", "/once", chromeDesk); rec.Code != http.StatusGone || e.last(t).Outcome != domain.OutcomeCapReached {
		t.Fatalf("second human: %d %s", rec.Code, e.last(t).Outcome)
	}
}

func TestRefusalPages(t *testing.T) {
	e := newEnv(t)
	past := now.Add(-time.Hour)
	e.put(t, domain.Link{Code: "disabled", Status: domain.StatusDisabledAdmin})
	e.put(t, domain.Link{Code: "bad", TrustState: domain.TrustQuarantined, Destination: "https://phish.example/login"})
	e.put(t, domain.Link{Code: "blocked", Destination: "https://login.evil.example/"})
	e.put(t, domain.Link{Code: "paused", Status: domain.StatusPaused})
	e.put(t, domain.Link{Code: "old", ExpiresAt: &past})

	tests := []struct {
		path    string
		status  int
		outcome domain.Outcome
		text    string
	}{
		{"/disabled", 451, domain.OutcomeBlockedAdmin, "disabled"},
		{"/bad", 451, domain.OutcomeQuarantined, "phish.example"},
		{"/blocked", 451, domain.OutcomeBlockedDomain, "blocklist"},
		{"/paused", 410, domain.OutcomeInactive, "no longer active"},
		{"/old", 410, domain.OutcomeExpired, "expired"},
	}
	for _, tt := range tests {
		rec := e.do("GET", tt.path, chromeDesk)
		if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.text) {
			t.Errorf("%s: %d, body lacks %q", tt.path, rec.Code, tt.text)
		}
		if rec.Header().Get("Location") != "" || rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s: Location %q Cache-Control %q", tt.path, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))
		}
		if e.last(t).Outcome != tt.outcome {
			t.Errorf("%s: event outcome %s, want %s", tt.path, e.last(t).Outcome, tt.outcome)
		}
	}
	// The quarantine page shows the host but never links to it.
	if body := e.do("GET", "/bad", chromeDesk).Body.String(); strings.Contains(body, "href=") {
		t.Error("warning page links to the flagged destination")
	}
}

func TestUnknownCodes(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/nope", "/favicon.ico", "/wp-login.php"} {
		if rec := e.do("GET", p, chromeDesk); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if len(e.clicks.Events()) != 0 {
		t.Error("an unknown code produced a click event")
	}
}

func TestPasswordFlow(t *testing.T) {
	e := newEnv(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("open sesame"), bcrypt.MinCost)
	l := e.put(t, domain.Link{Code: "secret", PasswordHash: hash})

	rec := e.do("GET", "/secret?s=qr", chromeDesk)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `action="/secret?s=qr"`) || e.last(t).Outcome != domain.OutcomePasswordRequired {
		t.Fatalf("form: %d %s", rec.Code, e.last(t).Outcome)
	}

	post := func(pw string) *httptest.ResponseRecorder {
		return e.do("POST", "/secret?s=qr", chromeDesk, func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(url.Values{"password": {pw}}.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		})
	}
	if rec := post("wrong"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not right") {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	rec = post("open sesame")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/secret?s=qr" {
		t.Fatalf("right password: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != secret.CookieName(l.ID) || !cookie.HttpOnly {
		t.Fatalf("cookie %+v", cookie)
	}
	rec = e.do("GET", "/secret?s=qr", chromeDesk, func(r *http.Request) { r.AddCookie(cookie) })
	if rec.Code != http.StatusFound || e.last(t).Source != domain.SourceQR {
		t.Fatalf("unlocked visit: %d", rec.Code)
	}
}

func TestPasswordRateLimit(t *testing.T) {
	e := newEnv(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("right"), bcrypt.MinCost)
	e.put(t, domain.Link{Code: "locked", PasswordHash: hash})
	var rec *httptest.ResponseRecorder
	for range 11 {
		rec = e.do("POST", "/locked", chromeDesk, func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader("password=guess"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		})
	}
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th attempt: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestReferrerHost(t *testing.T) {
	tests := map[string]string{
		"":                                     "",
		"https://www.Instagram.com/p/x?a=1":    "instagram.com",
		"android-app://com.google.android.gm/": "com.google.android.gm",
		"::bad":                                "",
	}
	for in, want := range tests {
		if got := referrerHost(in); got != want {
			t.Errorf("referrerHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// With the cache in front of the store, the cached copy of a capped link
// keeps its old cap_used. Once a human is refused for a full cap, the
// handler drops that copy, so bots are refused too instead of redirected.
func TestFullCapRefreshesTheCache(t *testing.T) {
	e := newEnv(t)
	cache := linkcache.New(e.store, time.Minute, 100)
	e.h.Links = cache
	e.h.Invalidator = cache
	one := int64(1)
	e.put(t, domain.Link{Code: "once", MaxClicks: &one})

	if rec := e.do("GET", "/once", chromeDesk); rec.Code != http.StatusFound {
		t.Fatalf("first human: %d", rec.Code)
	}
	if rec := e.do("GET", "/once", chromeDesk); rec.Code != http.StatusGone {
		t.Fatalf("second human: %d", rec.Code)
	}
	if rec := e.do("GET", "/once", whatsApp); rec.Code != http.StatusGone {
		t.Fatalf("bot after the cap filled: %d, want 410", rec.Code)
	}
}
