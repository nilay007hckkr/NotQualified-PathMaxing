package ratelimit

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTest(limit int, window time.Duration) (*Limiter, *clock) {
	c := &clock{t: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	l := New(limit, window)
	l.now = c.now
	return l, c
}

func TestBurstThenRefill(t *testing.T) {
	// The password rule: 10 per 15 minutes.
	l, c := newTest(10, 15*time.Minute)
	for i := range 10 {
		if ok, _ := l.Allow("ip|link"); !ok {
			t.Fatalf("attempt %d refused within the burst", i+1)
		}
	}
	ok, retry := l.Allow("ip|link")
	if ok {
		t.Fatal("11th attempt allowed")
	}
	// One token refills every 15m/10 = 90s.
	if retry < 89*time.Second || retry > 92*time.Second {
		t.Fatalf("Retry-After %v, want about 90s", retry)
	}
	c.advance(90 * time.Second)
	if ok, _ := l.Allow("ip|link"); !ok {
		t.Fatal("no token after 90 seconds")
	}
	if ok, _ := l.Allow("ip|link"); ok {
		t.Fatal("two tokens after 90 seconds")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTest(1, time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a refused")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("b refused because of a")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("a allowed twice")
	}
}

func TestIdleKeysAreSwept(t *testing.T) {
	l, c := newTest(5, time.Minute)
	for i := range 100 {
		l.Allow(fmt.Sprint(i))
	}
	if l.Len() != 100 {
		t.Fatalf("Len = %d", l.Len())
	}
	c.advance(2 * time.Minute)
	l.Allow("fresh")
	if l.Len() != 1 {
		t.Fatalf("Len = %d after the sweep, want 1", l.Len())
	}
}

func TestConcurrentCallersShareTheLimit(t *testing.T) {
	l := New(25, time.Hour)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("same"); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 25 {
		t.Fatalf("allowed %d, want 25", allowed.Load())
	}
}
