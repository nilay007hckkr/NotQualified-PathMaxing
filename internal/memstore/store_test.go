package memstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"pathmaxing/internal/domain"
)

func TestCodesAreCaseInsensitiveAndUnique(t *testing.T) {
	ctx := context.Background()
	s := New()
	sale, err := s.Put(domain.Link{Code: "Sale", Destination: "https://shop.example"})
	if err != nil {
		t.Fatal(err)
	}
	if sale.ID == 0 || sale.Status != domain.StatusActive || sale.TrustState != domain.TrustOK {
		t.Fatalf("Put did not fill defaults: %+v", sale)
	}
	if _, err := s.Put(domain.Link{Code: "sale", Destination: "https://other.example"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second link with code 'sale' = %v, want ErrConflict", err)
	}
	for _, code := range []string{"Sale", "sale", "SALE"} {
		got, err := s.ResolveCode(ctx, code)
		if err != nil || got.ID != sale.ID {
			t.Errorf("ResolveCode(%q) = %d, %v; want link %d", code, got.ID, err, sale.ID)
		}
	}
	if _, err := s.ResolveCode(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown code: %v, want ErrNotFound", err)
	}
	if _, err := s.GetLink(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown id: %v, want ErrNotFound", err)
	}

	// Replacing a link may keep its own code, in a different case.
	sale.Code = "SALE"
	if _, err := s.Put(sale); err != nil {
		t.Fatalf("re-putting a link with its own code: %v", err)
	}
}

func TestStoreHandsOutCopies(t *testing.T) {
	ctx := context.Background()
	s := New()
	l, _ := s.Put(domain.Link{Code: "x", Destination: "https://a.example", Targets: []domain.Target{{Device: domain.DeviceMobile, Destination: "https://m.example"}}})
	got, _ := s.GetLink(ctx, l.ID)
	got.Targets[0].Destination = "https://evil.example"
	again, _ := s.GetLink(ctx, l.ID)
	if again.Targets[0].Destination != "https://m.example" {
		t.Fatal("a caller changed the stored link through a returned copy")
	}
}

// TestReserveClickNeverOvershoots is the in-memory twin of the Postgres cap
// race test: 1,000 concurrent reservations against a cap of 37 must succeed
// exactly 37 times.
func TestReserveClickNeverOvershoots(t *testing.T) {
	const capacity, callers = 37, 1000
	ctx := context.Background()
	s := New()
	max := int64(capacity)
	l, _ := s.Put(domain.Link{Code: "race", Destination: "https://a.example", MaxClicks: &max})

	var granted atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := s.ReserveClick(ctx, l.ID, max)
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
	if granted.Load() != capacity {
		t.Fatalf("granted %d reservations, want exactly %d", granted.Load(), capacity)
	}
	got, _ := s.GetLink(ctx, l.ID)
	if got.CapUsed != capacity || !got.CapReached() {
		t.Fatalf("cap_used = %d, want %d", got.CapUsed, capacity)
	}
	if _, err := s.ReserveClick(ctx, 999, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reserve on a missing link: %v", err)
	}
}

func TestBlocklistCoversSubdomains(t *testing.T) {
	ctx := context.Background()
	s := New()
	s.BlockDomain("Evil.Example.", "phishing")
	tests := []struct {
		host     string
		wantRule string
		want     bool
	}{
		{"evil.example", "evil.example", true},
		{"EVIL.example", "evil.example", true},
		{"login.evil.example", "evil.example", true},
		{"a.b.evil.example.", "evil.example", true},
		{"notevil.example", "", false},
		{"evil.example.com", "", false},
		{"example", "", false},
	}
	for _, tt := range tests {
		rule, blocked, err := s.Blocked(ctx, tt.host)
		if err != nil || blocked != tt.want || rule != tt.wantRule {
			t.Errorf("Blocked(%q) = %q, %v, %v; want %q, %v", tt.host, rule, blocked, err, tt.wantRule, tt.want)
		}
	}
	s.UnblockDomain("evil.example")
	if _, blocked, _ := s.Blocked(ctx, "login.evil.example"); blocked {
		t.Error("still blocked after UnblockDomain")
	}
}

func TestAuditLog(t *testing.T) {
	s := New()
	if err := s.Append(context.Background(), domain.AuditEntry{Action: "link.quarantined", EntityType: "link", EntityID: 4}); err != nil {
		t.Fatal(err)
	}
	got := s.AuditEntries()
	if len(got) != 1 || got[0].Action != "link.quarantined" || got[0].At.IsZero() {
		t.Fatalf("AuditEntries = %+v", got)
	}
}

func TestClickLogKeepsTheLatest(t *testing.T) {
	c := NewClickLog(3)
	if len(c.Events()) != 0 {
		t.Fatal("new log is not empty")
	}
	for i := 1; i <= 5; i++ {
		c.Record(domain.ClickEvent{EventID: fmt.Sprint(i)})
	}
	var ids []string
	for _, e := range c.Events() {
		ids = append(ids, e.EventID)
	}
	if fmt.Sprint(ids) != "[3 4 5]" || c.Total() != 5 {
		t.Fatalf("events %v total %d, want [3 4 5] and 5", ids, c.Total())
	}
}
