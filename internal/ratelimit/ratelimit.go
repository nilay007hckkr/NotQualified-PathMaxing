// Package ratelimit limits how often one key (an IP, a user, an IP plus a
// link) may do something, such as guessing a link's password.
//
// Each key gets a token bucket that holds up to Limit tokens and refills at
// Limit per Window, so "10 per 15 minutes" allows a burst of 10 and then one
// more every 90 seconds. Buckets live in memory: with one server instance
// that is exact, and with several each instance enforces its own limit
// (the Redis roadmap item would share them).
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Limiter is safe for concurrent use.
type Limiter struct {
	limit  float64
	window time.Duration
	now    func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a limiter that allows limit events per window for each key.
func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		limit:   float64(limit),
		window:  window,
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
}

// Allow takes one token for key. When none is left it reports false and how
// long until one is, for a Retry-After header.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()
	rate := l.limit / l.window.Seconds() // tokens per second

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.limit, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.limit, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / rate * float64(time.Second))
	return false, wait.Round(time.Second) + time.Second
}

// sweep drops buckets that have refilled completely, at most once a window,
// so keys that stop coming back don't pile up in memory.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.last) >= l.window {
			delete(l.buckets, k)
		}
	}
}

// Len returns how many keys are being tracked, for tests and metrics.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
