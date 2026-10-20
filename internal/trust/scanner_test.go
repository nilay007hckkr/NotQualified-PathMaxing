package trust

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/linkcache"
	"pathmaxing/internal/memstore"
	"pathmaxing/internal/threatintel"
)

type fakeFetch map[string]Result

func (f fakeFetch) Check(_ context.Context, u string) Result { return f[u] }

func newScanner(t *testing.T) (*Scanner, *memstore.Store, *threatintel.Fake, *linkcache.Cache) {
	t.Helper()
	store := memstore.New()
	feed := threatintel.NewFake("safe_browsing")
	cache := linkcache.New(store, time.Minute, 100)
	s := &Scanner{
		Links: store,
		Repo:  NewMemRepo(store),
		Fetch: fakeFetch{
			"https://shop.example/sale": {Status: 200, FinalURL: "https://shop.example/sale", FinalHost: "shop.example", HopHosts: []string{"shop.example"}},
			"https://bit.example/x":     {Status: 200, FinalURL: "https://login.evil.example/", FinalHost: "login.evil.example", Hops: 1, HopHosts: []string{"bit.example", "login.evil.example"}},
		},
		Feeds:       threatintel.NewSet([]threatintel.Feed{feed}, nil, time.Second),
		Blocklist:   store,
		Invalidator: cache,
		Audit:       store,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:         func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) },
	}
	return s, store, feed, cache
}

// The demo's takedown: a destination starts redirecting to a blocklisted
// domain, a re-scan quarantines the link, the cache is cleared so the very
// next visit is refused, and the audit log records it.
func TestScanQuarantinesAndClearsTheCache(t *testing.T) {
	s, store, _, cache := newScanner(t)
	ctx := context.Background()
	l, _ := store.Put(domain.Link{Code: "promo", Destination: "https://shop.example/sale"})

	tr, err := s.Scan(ctx, l.ID)
	if err != nil || tr.Verdict != VerdictOK {
		t.Fatalf("first scan: %+v, %v", tr, err)
	}
	cache.ResolveCode(ctx, "promo") // the redirect path now has it cached

	// The owner's destination now bounces to a domain an admin blocked.
	l, _ = store.GetLink(ctx, l.ID)
	l.Destination = "https://bit.example/x"
	store.Put(l)
	store.BlockDomain("evil.example", "phishing")

	tr, err = s.Scan(ctx, l.ID)
	if err != nil || !tr.Quarantined || !strings.Contains(tr.Detail, "evil.example") {
		t.Fatalf("second scan: %+v, %v", tr, err)
	}
	if got, _ := cache.ResolveCode(ctx, "promo"); got.TrustState != domain.TrustQuarantined {
		t.Fatalf("cache still serves trust %s after the quarantine", got.TrustState)
	}
	audit := store.AuditEntries()
	if len(audit) != 1 || audit[0].Action != "link.quarantined" || audit[0].ActorID != nil {
		t.Fatalf("audit = %+v", audit)
	}
	hist, _ := s.Repo.History(ctx, l.ID, 10)
	if len(hist) != 2 || hist[0].Verdict != VerdictQuarantined || hist[0].Hops != 1 || hist[0].FinalDomain != "login.evil.example" {
		t.Fatalf("history = %+v", hist)
	}
	if !strings.Contains(strings.Join(hist[0].Sources, ","), "blocklist") {
		t.Fatalf("sources = %v", hist[0].Sources)
	}
}

func TestScanThreatFeedChecksTheFinalURL(t *testing.T) {
	s, store, feed, _ := newScanner(t)
	ctx := context.Background()
	feed.Flag("login.evil.example") // the feed knows the landing page, not the short hop
	l, _ := store.Put(domain.Link{Code: "hop", Destination: "https://bit.example/x"})
	tr, err := s.Scan(ctx, l.ID)
	if err != nil || !tr.Quarantined || !strings.Contains(tr.Detail, "safe_browsing") {
		t.Fatalf("got %+v, %v", tr, err)
	}
}
