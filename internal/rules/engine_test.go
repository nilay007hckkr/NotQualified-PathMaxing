package rules

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/memstore"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

// link returns an active link with the given changes applied.
func link(changes ...func(*domain.Link)) domain.Link {
	l := domain.Link{
		Code:        "diwali",
		Destination: "https://shop.example/sale",
		Status:      domain.StatusActive,
		TrustState:  domain.TrustOK,
		Targets: []domain.Target{
			{Device: domain.DeviceMobile, Destination: "https://m.shop.example/sale"},
		},
	}
	for _, c := range changes {
		c(&l)
	}
	return l
}

var (
	disabled    = func(l *domain.Link) { l.Status = domain.StatusDisabledAdmin }
	quarantined = func(l *domain.Link) { l.TrustState = domain.TrustQuarantined }
	paused      = func(l *domain.Link) { l.Status = domain.StatusPaused }
	archived    = func(l *domain.Link) { l.Status = domain.StatusArchived }
	deleted     = func(l *domain.Link) { l.DeletedAt = ptr(now.Add(-time.Hour)) }
	notStarted  = func(l *domain.Link) { l.StartsAt = ptr(now.Add(time.Hour)) }
	expiresNow  = func(l *domain.Link) { l.ExpiresAt = ptr(now) }
	expired     = func(l *domain.Link) { l.ExpiresAt = ptr(now.Add(-time.Minute)) }
	protected   = func(l *domain.Link) { l.PasswordHash = []byte("$2a$10$hash") }
	capOf       = func(max, used int64) func(*domain.Link) {
		return func(l *domain.Link) { l.MaxClicks = ptr(max); l.CapUsed = used }
	}
	toBlocked = func(l *domain.Link) { l.Destination = "https://login.evil.example/x" }
	mobileTo  = func(l *domain.Link) {
		l.Targets = []domain.Target{{Device: domain.DeviceMobile, Destination: "https://login.evil.example/m"}}
	}
)

// setup stores l in a fresh memstore (with evil.example blocked) and returns
// an engine over it and the stored link.
func setup(t *testing.T, l domain.Link) (*Engine, *memstore.Store, domain.Link) {
	t.Helper()
	s := memstore.New()
	s.BlockDomain("evil.example", "phishing")
	stored, err := s.Put(l)
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{Blocklist: s, Caps: s}, s, stored
}

func TestRuleOrder(t *testing.T) {
	human := Visit{Human: true, Device: domain.DeviceDesktop, Now: now}
	bot := Visit{Human: false, Now: now}
	tests := []struct {
		name     string
		link     domain.Link
		visit    Visit
		want     domain.Outcome
		status   int
		wantDest string
		capAfter int64
	}{
		// Each rule on its own.
		{name: "active link redirects", link: link(), visit: human, want: domain.OutcomeRedirected, status: http.StatusFound, wantDest: "https://shop.example/sale"},
		{name: "admin-disabled", link: link(disabled), visit: human, want: domain.OutcomeBlockedAdmin, status: 451},
		{name: "quarantined", link: link(quarantined), visit: human, want: domain.OutcomeQuarantined, status: 451},
		{name: "destination blocked (subdomain of a blocked domain)", link: link(toBlocked), visit: human, want: domain.OutcomeBlockedDomain, status: 451},
		{name: "a device target is blocked", link: link(mobileTo), visit: human, want: domain.OutcomeBlockedDomain, status: 451},
		{name: "paused", link: link(paused), visit: human, want: domain.OutcomeInactive, status: 410},
		{name: "archived", link: link(archived), visit: human, want: domain.OutcomeInactive, status: 410},
		{name: "deleted", link: link(deleted), visit: human, want: domain.OutcomeInactive, status: 410},
		{name: "not started", link: link(notStarted), visit: human, want: domain.OutcomeNotStarted, status: 404},
		{name: "expires exactly now", link: link(expiresNow), visit: human, want: domain.OutcomeExpired, status: 410},
		{name: "expired", link: link(expired), visit: human, want: domain.OutcomeExpired, status: 410},
		{name: "password, locked", link: link(protected), visit: human, want: domain.OutcomePasswordRequired, status: 200},
		{name: "password, unlocked", link: link(protected), visit: Visit{Human: true, Unlocked: true, Now: now}, want: domain.OutcomeRedirected, status: 302, wantDest: "https://shop.example/sale"},
		{name: "cap with room uses a click", link: link(capOf(3, 2)), visit: human, want: domain.OutcomeRedirected, status: 302, capAfter: 3},
		{name: "cap full", link: link(capOf(3, 3)), visit: human, want: domain.OutcomeCapReached, status: 410, capAfter: 3},

		// The first refusal wins.
		{name: "disabled beats everything", link: link(disabled, quarantined, deleted, expired, protected, capOf(1, 1)), visit: human, want: domain.OutcomeBlockedAdmin, status: 451, capAfter: 1},
		{name: "quarantined beats paused", link: link(quarantined, paused), visit: human, want: domain.OutcomeQuarantined, status: 451},
		{name: "blocked domain beats expired", link: link(toBlocked, expired), visit: human, want: domain.OutcomeBlockedDomain, status: 451},
		{name: "paused beats expired", link: link(paused, expired), visit: human, want: domain.OutcomeInactive, status: 410},
		{name: "not started beats password", link: link(notStarted, protected), visit: human, want: domain.OutcomeNotStarted, status: 404},
		{name: "expired beats password", link: link(expired, protected), visit: human, want: domain.OutcomeExpired, status: 410},
		{name: "password beats a full cap", link: link(protected, capOf(1, 1)), visit: human, want: domain.OutcomePasswordRequired, status: 200, capAfter: 1},

		// Refused visits never use up a click.
		{name: "locked password visit keeps the click", link: link(protected, capOf(5, 0)), visit: human, want: domain.OutcomePasswordRequired, status: 200, capAfter: 0},
		{name: "expired visit keeps the click", link: link(expired, capOf(5, 0)), visit: human, want: domain.OutcomeExpired, status: 410, capAfter: 0},

		// Bots only look at the cap.
		{name: "bot under the cap is redirected without using a click", link: link(capOf(1, 0)), visit: bot, want: domain.OutcomeRedirected, status: 302, capAfter: 0},
		{name: "bot at the cap is refused", link: link(capOf(1, 1)), visit: bot, want: domain.OutcomeCapReached, status: 410, capAfter: 1},

		// Device targeting.
		{name: "mobile gets its target", link: link(), visit: Visit{Human: true, Device: domain.DeviceMobile, Now: now}, want: domain.OutcomeRedirected, status: 302, wantDest: "https://m.shop.example/sale"},
		{name: "tablet falls back to the main destination", link: link(), visit: Visit{Human: true, Device: domain.DeviceTablet, Now: now}, want: domain.OutcomeRedirected, status: 302, wantDest: "https://shop.example/sale"},
		{name: "unknown device falls back too", link: link(), visit: bot, want: domain.OutcomeRedirected, status: 302, wantDest: "https://shop.example/sale"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, s, l := setup(t, tt.link)
			d, err := e.Decide(context.Background(), l, tt.visit)
			if err != nil {
				t.Fatal(err)
			}
			if d.Outcome != tt.want || d.Status != tt.status {
				t.Fatalf("got %s/%d, want %s/%d", d.Outcome, d.Status, tt.want, tt.status)
			}
			if d.Redirected() != (tt.want == domain.OutcomeRedirected) {
				t.Errorf("Redirected() = %v", d.Redirected())
			}
			if tt.wantDest != "" && d.Destination != tt.wantDest {
				t.Errorf("destination %q, want %q", d.Destination, tt.wantDest)
			}
			if !d.Redirected() && d.Destination != "" {
				t.Errorf("a refusal carries destination %q", d.Destination)
			}
			if tt.want == domain.OutcomeBlockedDomain && d.BlockedBy != "evil.example" {
				t.Errorf("BlockedBy = %q, want evil.example", d.BlockedBy)
			}
			if l.MaxClicks != nil {
				after, _ := s.GetLink(context.Background(), l.ID)
				if after.CapUsed != tt.capAfter {
					t.Errorf("cap_used = %d after the visit, want %d", after.CapUsed, tt.capAfter)
				}
			}
		})
	}
}

// TestCapRace sends 1,000 concurrent human visits to a link capped at 100:
// exactly 100 must be redirected, and every other visit refused.
func TestCapRace(t *testing.T) {
	const capacity, visitors = 100, 1000
	e, s, l := setup(t, link(capOf(capacity, 0)))

	var redirected, refused atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range visitors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d, err := e.Decide(context.Background(), l, Visit{Human: true, Now: now})
			switch {
			case err != nil:
				t.Error(err)
			case d.Redirected():
				redirected.Add(1)
			case d.Outcome == domain.OutcomeCapReached:
				refused.Add(1)
			default:
				t.Errorf("unexpected outcome %s", d.Outcome)
			}
		}()
	}
	close(start)
	wg.Wait()

	if redirected.Load() != capacity || refused.Load() != visitors-capacity {
		t.Fatalf("redirected %d, refused %d; want exactly %d and %d", redirected.Load(), refused.Load(), capacity, visitors-capacity)
	}
	after, _ := s.GetLink(context.Background(), l.ID)
	if after.CapUsed != capacity {
		t.Fatalf("cap_used = %d, want %d", after.CapUsed, capacity)
	}
}

// TestBotsNeverUseTheCap runs bots and humans together on a one-time link:
// the single click goes to a human, however many bots arrive first.
func TestBotsNeverUseTheCap(t *testing.T) {
	e, s, l := setup(t, link(capOf(1, 0)))
	ctx := context.Background()
	for range 50 {
		d, err := e.Decide(ctx, l, Visit{Human: false, Now: now})
		if err != nil || !d.Redirected() {
			t.Fatalf("preview bot: %v, %v", d.Outcome, err)
		}
	}
	first, _ := e.Decide(ctx, l, Visit{Human: true, Now: now})
	second, _ := e.Decide(ctx, l, Visit{Human: true, Now: now})
	if !first.Redirected() || second.Outcome != domain.OutcomeCapReached {
		t.Fatalf("humans got %s then %s, want redirected then cap_reached", first.Outcome, second.Outcome)
	}
	after, _ := s.GetLink(ctx, l.ID)
	if after.CapUsed != 1 {
		t.Fatalf("cap_used = %d, want 1", after.CapUsed)
	}
}

type failingCaps struct{}

func (failingCaps) ReserveClick(context.Context, int64, int64) (bool, error) {
	return false, errors.New("database unavailable")
}

type failingBlocklist struct{}

func (failingBlocklist) Blocked(context.Context, string) (string, bool, error) {
	return "", false, errors.New("blocklist unavailable")
}

// When a dependency fails, the engine reports the error instead of guessing:
// granting a click it couldn't reserve could exceed the cap.
func TestDependencyFailuresAreErrors(t *testing.T) {
	s := memstore.New()
	capped := link(capOf(5, 0))
	e := &Engine{Blocklist: s, Caps: failingCaps{}}
	if _, err := e.Decide(context.Background(), capped, Visit{Human: true, Now: now}); err == nil {
		t.Error("a failed cap reservation was not reported")
	}
	e = &Engine{Blocklist: failingBlocklist{}, Caps: s}
	if _, err := e.Decide(context.Background(), link(), Visit{Human: true, Now: now}); err == nil {
		t.Error("a failed blocklist check was not reported")
	}
}

func TestHostOf(t *testing.T) {
	tests := map[string]string{
		"https://Shop.Example/sale":    "shop.example",
		"https://[2606:4700::1]:8443/": "2606:4700::1",
		"::not a url":                  "",
	}
	for in, want := range tests {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}
