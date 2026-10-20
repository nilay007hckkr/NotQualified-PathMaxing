package linkcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/memstore"
)

// counting wraps a resolver and counts the lookups that reach it.
type counting struct {
	next  domain.LinkResolver
	calls atomic.Int64
}

func (c *counting) ResolveCode(ctx context.Context, code string) (domain.Link, error) {
	c.calls.Add(1)
	return c.next.ResolveCode(ctx, code)
}

func setup(t *testing.T) (*Cache, *memstore.Store, *counting, *time.Time) {
	t.Helper()
	store := memstore.New()
	cnt := &counting{next: store}
	c := New(cnt, time.Minute, 1000)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, store, cnt, &now
}

func TestHitsMissesAndTTL(t *testing.T) {
	c, store, cnt, now := setup(t)
	ctx := context.Background()
	store.Put(domain.Link{Code: "Sale", Destination: "https://a.example"})

	for _, code := range []string{"Sale", "sale", "SALE"} {
		l, err := c.ResolveCode(ctx, code)
		if err != nil || l.Destination != "https://a.example" {
			t.Fatalf("ResolveCode(%q) = %+v, %v", code, l, err)
		}
	}
	if cnt.calls.Load() != 1 {
		t.Fatalf("store lookups = %d, want 1 (case-insensitive hits)", cnt.calls.Load())
	}
	*now = now.Add(61 * time.Second)
	c.ResolveCode(ctx, "sale")
	if cnt.calls.Load() != 2 {
		t.Fatalf("store lookups after TTL = %d, want 2", cnt.calls.Load())
	}
}

func TestNotFoundIsCachedBrieflyAndInvalidated(t *testing.T) {
	c, store, cnt, now := setup(t)
	ctx := context.Background()
	for range 3 {
		if _, err := c.ResolveCode(ctx, "promo"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
	}
	if cnt.calls.Load() != 1 {
		t.Fatalf("store lookups = %d, want 1", cnt.calls.Load())
	}
	// Creating the link invalidates its code, so it works immediately.
	store.Put(domain.Link{Code: "promo", Destination: "https://a.example"})
	c.Invalidate("PROMO")
	if _, err := c.ResolveCode(ctx, "promo"); err != nil {
		t.Fatalf("after create + invalidate: %v", err)
	}
	// And without an invalidation the miss expires after NegativeTTL.
	c.ResolveCode(ctx, "other")
	store.Put(domain.Link{Code: "other", Destination: "https://b.example"})
	*now = now.Add(NegativeTTL)
	if _, err := c.ResolveCode(ctx, "other"); err != nil {
		t.Fatalf("after NegativeTTL: %v", err)
	}
}

func TestInvalidateShowsChanges(t *testing.T) {
	c, store, _, _ := setup(t)
	ctx := context.Background()
	l, _ := store.Put(domain.Link{Code: "x", Destination: "https://a.example"})
	c.ResolveCode(ctx, "x")
	l.TrustState = domain.TrustQuarantined
	store.Put(l)
	if got, _ := c.ResolveCode(ctx, "x"); got.TrustState != domain.TrustOK {
		t.Fatal("cache served the change before any invalidation; the test isn't testing anything")
	}
	c.Invalidate("x")
	if got, _ := c.ResolveCode(ctx, "x"); got.TrustState != domain.TrustQuarantined {
		t.Fatalf("after Invalidate the cache still serves %s", got.TrustState)
	}
}

// TestStaleReadIsNotStored reproduces the race the generation counter
// exists for: a lookup reads the old row, the link is quarantined and
// invalidated, then the lookup finishes. The old row must not be cached.
func TestStaleReadIsNotStored(t *testing.T) {
	c, store, _, _ := setup(t)
	ctx := context.Background()
	l, _ := store.Put(domain.Link{Code: "race", Destination: "https://a.example"})

	reading := make(chan struct{})
	finish := make(chan struct{})
	var once sync.Once
	c.missLoader = func(ctx context.Context, code string) (domain.Link, error) {
		got, err := store.ResolveCode(ctx, code) // reads the old row...
		once.Do(func() {
			close(reading)
			<-finish // ...and is slow to return it
		})
		return got, err
	}

	done := make(chan domain.Link)
	go func() {
		got, _ := c.ResolveCode(ctx, "race")
		done <- got
	}()
	<-reading
	l.TrustState = domain.TrustQuarantined
	store.Put(l)
	c.Invalidate("race")
	close(finish)
	if stale := <-done; stale.TrustState != domain.TrustOK {
		t.Fatal("setup: the slow read should have returned the old row")
	}
	if got, _ := c.ResolveCode(ctx, "race"); got.TrustState != domain.TrustQuarantined {
		t.Fatalf("the stale read was cached: next lookup sees %s", got.TrustState)
	}
}

func TestConcurrentMissesShareOneRead(t *testing.T) {
	c, store, cnt, _ := setup(t)
	store.Put(domain.Link{Code: "hot", Destination: "https://a.example"})
	release := make(chan struct{})
	c.missLoader = func(ctx context.Context, code string) (domain.Link, error) {
		<-release
		return cnt.ResolveCode(ctx, code)
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.ResolveCode(context.Background(), "hot"); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if cnt.calls.Load() != 1 {
		t.Fatalf("store lookups = %d, want 1 for 100 concurrent misses", cnt.calls.Load())
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	c, _, _, _ := setup(t)
	fail := true
	c.missLoader = func(context.Context, string) (domain.Link, error) {
		if fail {
			return domain.Link{}, errors.New("database down")
		}
		return domain.Link{Code: "x"}, nil
	}
	if _, err := c.ResolveCode(context.Background(), "x"); err == nil {
		t.Fatal("expected the store error")
	}
	fail = false
	if _, err := c.ResolveCode(context.Background(), "x"); err != nil {
		t.Fatalf("an error was cached: %v", err)
	}
}

func TestSizeIsBounded(t *testing.T) {
	store := memstore.New()
	c := New(store, time.Minute, shardCount*2)
	for i := range 1000 {
		code := fmt.Sprintf("code%d", i)
		store.Put(domain.Link{Code: code, Destination: "https://a.example"})
		c.ResolveCode(context.Background(), code)
	}
	if c.Len() > shardCount*2 {
		t.Fatalf("cache holds %d entries, limit %d", c.Len(), shardCount*2)
	}
}
