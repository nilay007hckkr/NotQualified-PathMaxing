// Package redirect serves short links: GET and HEAD /{code} run the rule
// engine and answer with a 302 or a status page, and POST /{code} checks a
// link's password. Every visit to an existing link, allowed or refused,
// becomes a click event, handed to the click sink without waiting.
package redirect

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pathmaxing/internal/classify"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
	"pathmaxing/internal/ratelimit"
	"pathmaxing/internal/rules"
	"pathmaxing/internal/secret"
)

// Handler holds the redirect service's dependencies. Every field is required.
type Handler struct {
	Links      domain.LinkResolver
	Engine     *rules.Engine
	Classifier domain.TrafficClassifier
	Clicks     domain.ClickSink
	Geo        domain.GeoLookup
	Unlocker   *secret.Unlocker
	Visitors   *secret.Visitors
	// PasswordLimiter is keyed by IP and link: 10 tries per 15 minutes.
	PasswordLimiter *ratelimit.Limiter
	ProxyHops       int
	// Invalidator, when set, drops a cached link whose cap turned out to be
	// full, so bots stop seeing the stale room left in the cached copy.
	Invalidator domain.CacheInvalidator
	Log         *slog.Logger
	Now         func() time.Time
}

// Register mounts the routes. A GET pattern also matches HEAD.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{code}", h.visit)
	mux.HandleFunc("POST /{code}", h.unlock)
}

// plausibleCode rejects paths that can't be a code (favicon.ico, wp-login.php)
// before they cost a cache or database lookup.
func plausibleCode(code string) bool {
	if len(code) == 0 || len(code) > 50 {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (h *Handler) visit(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !plausibleCode(code) {
		renderPage(w, http.StatusNotFound, notFoundPage())
		return
	}
	ctx := r.Context()
	link, err := h.Links.ResolveCode(ctx, code)
	if errors.Is(err, domain.ErrNotFound) {
		// No link, so no click event: there is nothing to report it under.
		renderPage(w, http.StatusNotFound, notFoundPage())
		return
	}
	if err != nil {
		h.Log.Error("resolve code", "code", code, "err", err)
		renderPage(w, http.StatusServiceUnavailable, unavailablePage())
		return
	}

	now := h.Now()
	ip := httpx.ClientIP(r, h.ProxyHops)
	ua := r.UserAgent()
	class := h.Classifier.Classify(domain.RequestInfo{
		Method: r.Method, UserAgent: ua, Referrer: r.Referer(), Purpose: purpose(r), IP: ip, At: now,
	})
	agent := classify.ParseUA(ua)
	unlocked := link.HasPassword() && h.Unlocker.Valid(r, link.ID, link.PasswordHash, now)

	decision, err := h.Engine.Decide(ctx, link, rules.Visit{
		Device: agent.Device, Human: class.Class.IsHuman(), Unlocked: unlocked, Now: now,
	})
	if err != nil {
		// Without a working cap reservation we can't promise the cap holds,
		// so refuse for now rather than redirect.
		h.Log.Error("rule engine", "link_id", link.ID, "err", err)
		renderPage(w, http.StatusServiceUnavailable, unavailablePage())
		return
	}

	if decision.Outcome == domain.OutcomeCapReached && !link.CapReached() && h.Invalidator != nil {
		h.Invalidator.Invalidate(link.Code)
	}

	qr := r.URL.Query().Get("s") == "qr"
	source := domain.SourceLink
	if qr {
		source = domain.SourceQR
	}
	h.Clicks.Record(domain.ClickEvent{
		EventID:      domain.NewEventID(),
		LinkID:       link.ID,
		WorkspaceID:  link.WorkspaceID,
		At:           now,
		Outcome:      decision.Outcome,
		Class:        class.Class,
		ClassReason:  class.Reason,
		Source:       source,
		Method:       r.Method,
		Device:       agent.Device,
		Browser:      agent.Browser,
		OS:           agent.OS,
		ReferrerHost: referrerHost(r.Referer()),
		Country:      h.Geo.Country(ip),
		VisitorHash:  h.Visitors.Hash(ip, ua, now),
	})

	switch decision.Outcome {
	case domain.OutcomeRedirected:
		// 302, never 301: browsers cache a 301 forever, which would hide repeat
		// clicks and keep sending people to a link after it is taken down.
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer-when-downgrade")
		http.Redirect(w, r, decision.Destination, http.StatusFound)
	case domain.OutcomePasswordRequired:
		renderPage(w, http.StatusOK, passwordPage(link.Code, qr, ""))
	case domain.OutcomeQuarantined, domain.OutcomeBlockedDomain:
		p := pageFor(decision.Outcome)
		p.Host = hostOf(link.Destination)
		renderPage(w, decision.Status, p)
	default:
		renderPage(w, decision.Status, pageFor(decision.Outcome))
	}
}

func (h *Handler) unlock(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !plausibleCode(code) {
		renderPage(w, http.StatusNotFound, notFoundPage())
		return
	}
	link, err := h.Links.ResolveCode(r.Context(), code)
	if errors.Is(err, domain.ErrNotFound) {
		renderPage(w, http.StatusNotFound, notFoundPage())
		return
	}
	if err != nil {
		h.Log.Error("resolve code", "code", code, "err", err)
		renderPage(w, http.StatusServiceUnavailable, unavailablePage())
		return
	}
	qr := r.URL.Query().Get("s") == "qr"
	back := "/" + link.Code
	if qr {
		back += "?s=qr"
	}
	if !link.HasPassword() {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	ip := httpx.ClientIP(r, h.ProxyHops)
	if ok, wait := h.PasswordLimiter.Allow(ip.String() + "|" + link.Code); !ok {
		retryAfter(w, wait)
		renderPage(w, http.StatusTooManyRequests, passwordPage(link.Code, qr, "Too many attempts. Wait a few minutes and try again."))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	password := r.PostFormValue("password")
	if password == "" || bcrypt.CompareHashAndPassword(link.PasswordHash, []byte(password)) != nil {
		renderPage(w, http.StatusForbidden, passwordPage(link.Code, qr, "That password is not right."))
		return
	}
	http.SetCookie(w, h.Unlocker.Cookie(link.ID, link.PasswordHash, h.Now()))
	// 303 turns the POST into a GET, which runs the rules again (and counts
	// the visit) with the new cookie.
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// purpose reads the headers browsers send when they prefetch a page nobody
// has clicked yet.
func purpose(r *http.Request) string {
	for _, name := range []string{"Sec-Purpose", "Purpose", "X-Moz", "X-Purpose"} {
		if v := r.Header.Get(name); v != "" {
			if strings.Contains(strings.ToLower(v), "prefetch") || strings.Contains(strings.ToLower(v), "preview") {
				return "prefetch"
			}
		}
	}
	return ""
}

// referrerHost keeps only the referring site's host. The full Referer can
// carry personal data in its path or query.
func referrerHost(ref string) string {
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func hostOf(dest string) string {
	u, err := url.Parse(dest)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
