package store

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/migrate/pgtest"
)

// newTestStore returns a store on a throwaway schema. The tests in this file
// only run when TEST_DATABASE_URL is set.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	pool := pgtest.New(t)
	s, err := New(context.Background(), pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ptr[T any](v T) *T { return &v }

func TestCreateAndResolve(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	expires := time.Date(2026, 11, 5, 18, 29, 59, 0, time.UTC)

	l, err := s.CreateLink(ctx, NewLink{
		WorkspaceID: 1, OwnerID: 1, Alias: "Diwali", Destination: "https://shop.example/sale",
		Title: "  Diwali sale ", ExpiresAt: &expires, MaxClicks: ptr(int64(100)),
		PasswordHash: []byte("$2a$10$hash"),
		Targets: []domain.Target{
			{Device: domain.DeviceTablet, Destination: "https://t.shop.example"},
			{Device: domain.DeviceMobile, Destination: "https://m.shop.example"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.ID == 0 || l.Code != "Diwali" || l.Title != "Diwali sale" || l.Status != domain.StatusActive || l.TrustState != domain.TrustOK {
		t.Fatalf("created link = %+v", l)
	}
	if !l.ExpiresAt.Equal(expires) || *l.MaxClicks != 100 || !l.HasPassword() || l.CreatedAt.IsZero() {
		t.Fatalf("options not stored: %+v", l)
	}
	if len(l.Targets) != 2 || l.Targets[0].Device != domain.DeviceMobile || l.Targets[1].Destination != "https://t.shop.example" {
		t.Fatalf("targets = %+v", l.Targets)
	}

	for _, code := range []string{"Diwali", "diwali", "DIWALI"} {
		got, err := s.ResolveCode(ctx, code)
		if err != nil || got.ID != l.ID {
			t.Errorf("ResolveCode(%q) = %d, %v", code, got.ID, err)
		}
	}
	if _, err := s.ResolveCode(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown code: %v", err)
	}
	if _, err := s.GetLink(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestAliasTakenIgnoringCase(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	in := NewLink{WorkspaceID: 1, OwnerID: 1, Alias: "Sale", Destination: "https://a.example"}
	if _, err := s.CreateLink(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Alias = "sale"
	if _, err := s.CreateLink(ctx, in); !errors.Is(err, ErrAliasTaken) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second alias = %v, want ErrAliasTaken", err)
	}
}

// Two people racing for "Sale" and "sale": exactly one wins.
func TestAliasRace(t *testing.T) {
	s := newTestStore(t)
	var won, lost atomic.Int64
	var wg sync.WaitGroup
	for _, alias := range []string{"Sale", "sale", "SALE", "sAlE", "salE", "Sale", "sale", "SaLe"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateLink(context.Background(), NewLink{WorkspaceID: 1, OwnerID: 1, Alias: alias, Destination: "https://a.example"})
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, ErrAliasTaken):
				lost.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 || lost.Load() != 7 {
		t.Fatalf("won %d, lost %d; want 1 and 7", won.Load(), lost.Load())
	}
}

func TestGeneratedCodeRetriesOnCollision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateLink(ctx, NewLink{WorkspaceID: 1, OwnerID: 1, Alias: "taken123", Destination: "https://a.example"}); err != nil {
		t.Fatal(err)
	}
	codes := []string{"TAKEN123", "taken123", "fresh456"}
	var calls int
	s.newCode = func() string { c := codes[calls]; calls++; return c }

	l, err := s.CreateLink(ctx, NewLink{WorkspaceID: 1, OwnerID: 1, Destination: "https://b.example"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Code != "fresh456" || calls != 3 {
		t.Fatalf("code %q after %d tries, want fresh456 after 3", l.Code, calls)
	}

	s.newCode = func() string { return "taken123" }
	if _, err := s.CreateLink(ctx, NewLink{WorkspaceID: 1, OwnerID: 1, Destination: "https://c.example"}); err == nil {
		t.Fatal("CreateLink succeeded although every generated code collided")
	}
}

// TestReserveClickRace is the Postgres version of the cap race: 1,000
// concurrent reservations on a cap of 50 succeed exactly 50 times.
func TestReserveClickRace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const capacity, callers = 50, 1000
	l, err := s.CreateLink(ctx, NewLink{WorkspaceID: 1, OwnerID: 1, Alias: "race", Destination: "https://a.example", MaxClicks: ptr(int64(capacity))})
	if err != nil {
		t.Fatal(err)
	}
	var granted atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := s.ReserveClick(ctx, l.ID, capacity)
			if err != nil {
				t.Error(err)
			}
			if ok {
				granted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	got, _ := s.GetLink(ctx, l.ID)
	if granted.Load() != capacity || got.CapUsed != capacity {
		t.Fatalf("granted %d, cap_used %d; want exactly %d", granted.Load(), got.CapUsed, capacity)
	}
}

func TestSoftDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	l, _ := s.CreateLink(ctx, NewLink{WorkspaceID: 1, OwnerID: 1, Alias: "gone", Destination: "https://a.example"})
	deleted, err := s.DeleteLink(ctx, l.ID)
	if err != nil || deleted.Code != "gone" {
		t.Fatalf("DeleteLink = %+v, %v", deleted, err)
	}
	got, err := s.ResolveCode(ctx, "gone")
	if err != nil || !got.Deleted() {
		t.Fatalf("a deleted link must still resolve (for 410): %+v, %v", got, err)
	}
	if _, err := s.DeleteLink(ctx, l.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleting twice = %v, want ErrNotFound", err)
	}
	if _, err := s.CreateLink(ctx, NewLink{WorkspaceID: 2, OwnerID: 2, Alias: "GONE", Destination: "https://b.example"}); !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("reusing a deleted code = %v, want ErrAliasTaken", err)
	}
}

func TestBlocklist(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, blocked, _ := s.Blocked(ctx, "evil.example"); blocked {
		t.Fatal("empty blocklist blocks")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO blocked_domains (domain, reason) VALUES ('evil.example', 'phishing')`); err != nil {
		t.Fatal(err)
	}
	// Not visible until the next reload: the redirect path never queries.
	if _, blocked, _ := s.Blocked(ctx, "evil.example"); blocked {
		t.Fatal("blocklist changed without a reload")
	}
	if err := s.ReloadBlocklist(ctx); err != nil {
		t.Fatal(err)
	}
	rule, blocked, err := s.Blocked(ctx, "login.evil.example")
	if err != nil || !blocked || rule != "evil.example" {
		t.Fatalf("Blocked(login.evil.example) = %q, %v, %v", rule, blocked, err)
	}

	refreshCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.RefreshBlocklist(10 * time.Millisecond)(refreshCtx) }()
	if _, err := s.pool.Exec(ctx, `DELETE FROM blocked_domains`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, blocked, _ := s.Blocked(ctx, "evil.example"); !blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refresh loop never picked up the change")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAuditAppend(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	actor := int64(7)
	if err := s.Append(ctx, domain.AuditEntry{ActorID: &actor, Action: "link.disabled", EntityType: "link", EntityID: 42, Detail: map[string]any{"reason": "phishing"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, domain.AuditEntry{Action: "link.quarantined", EntityType: "link", EntityID: 42}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT actor_id, action, detail->>'reason' FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a *int64
		var action string
		var reason *string
		if err := rows.Scan(&a, &action, &reason); err != nil {
			t.Fatal(err)
		}
		entry := action
		if a != nil {
			entry += " by 7"
		}
		if reason != nil {
			entry += " for " + *reason
		}
		got = append(got, entry)
	}
	if len(got) != 2 || got[0] != "link.disabled by 7 for phishing" || got[1] != "link.quarantined" {
		t.Fatalf("audit rows = %v", got)
	}
}
