// Package linkcache keeps resolved links in memory, so the redirect path
// usually answers without touching the database.
//
// The hard part is staying correct when links change. Every change (an
// edit, a pause, a takedown, a quarantine) calls Invalidate after its
// database commit. That alone leaves a race: a lookup that read the old row
// just before the change could store it just after the invalidation, and the
// stale copy would live until its TTL ran out. So each shard keeps a
// generation counter that Invalidate bumps, and a lookup only stores what it
// read if the generation is still the one it saw before reading.
package linkcache

import (
	"context"
	"errors"
	"hash/maphash"
	"strconv"
	"sync"
	"time"

	"pathmaxing/internal/domain"
)

const shardCount = 64

// NegativeTTL is how long "no such code" is remembered. It is short because
// a link with that code may be created at any moment (creation invalidates
// it too, but a short TTL bounds the damage of a missed invalidation).
const NegativeTTL = 5 * time.Second

// Cache implements domain.LinkResolver in front of another resolver, and
// domain.CacheInvalidator.
type Cache struct {
	next       domain.LinkResolver
	ttl        time.Duration
	perShard   int
	now        func() time.Time
	seed       maphash.Seed
	shards     [shardCount]shard
	loadsMu    sync.Mutex
	loads      map[string]*load
	missLoader func(ctx context.Context, code string) (domain.Link, error)
}

type shard struct {
	mu      sync.Mutex
	gen     uint64
	entries map[string]entry
}

type entry struct {
	link    domain.Link
	found   bool
	expires time.Time
}

// load is one in-flight read from the next resolver that concurrent misses
// for the same code (and generation) share.
type load struct {
	done chan struct{}
	link domain.Link
	err  error
}

// New returns a cache over next holding up to size links for ttl each.
func New(next domain.LinkResolver, ttl time.Duration, size int) *Cache {
	c := &Cache{
		next:     next,
		ttl:      ttl,
		perShard: max(1, size/shardCount),
		now:      time.Now,
		seed:     maphash.MakeSeed(),
		loads:    make(map[string]*load),
	}
	for i := range c.shards {
		c.shards[i].entries = make(map[string]entry)
	}
	c.missLoader = c.next.ResolveCode
	return c
}

func (c *Cache) shardFor(key string) *shard {
	return &c.shards[maphash.String(c.seed, key)%shardCount]
}

// ResolveCode implements domain.LinkResolver.
func (c *Cache) ResolveCode(ctx context.Context, code string) (domain.Link, error) {
	key := domain.NormalizeCode(code)
	sh := c.shardFor(key)

	sh.mu.Lock()
	e, ok := sh.entries[key]
	gen := sh.gen
	sh.mu.Unlock()
	if ok && c.now().Before(e.expires) {
		if !e.found {
			return domain.Link{}, domain.ErrNotFound
		}
		return e.link.Clone(), nil
	}

	link, err := c.loadShared(ctx, key, code, gen)
	switch {
	case err == nil:
		c.store(sh, key, gen, entry{link: link, found: true, expires: c.now().Add(c.ttl)})
		return link.Clone(), nil
	case errors.Is(err, domain.ErrNotFound):
		c.store(sh, key, gen, entry{expires: c.now().Add(NegativeTTL)})
		return domain.Link{}, err
	default:
		return domain.Link{}, err // errors are never cached
	}
}

// loadShared makes concurrent misses for one code share one database read.
// The generation is part of the key, so a lookup that starts after an
// invalidation never waits for, and reuses, a read that began before it.
func (c *Cache) loadShared(ctx context.Context, key, code string, gen uint64) (domain.Link, error) {
	id := key + "#" + strconv.FormatUint(gen, 10)
	c.loadsMu.Lock()
	if l, ok := c.loads[id]; ok {
		c.loadsMu.Unlock()
		select {
		case <-l.done:
			return l.link.Clone(), l.err
		case <-ctx.Done():
			return domain.Link{}, ctx.Err()
		}
	}
	l := &load{done: make(chan struct{})}
	c.loads[id] = l
	c.loadsMu.Unlock()

	l.link, l.err = c.missLoader(ctx, code)
	close(l.done)
	c.loadsMu.Lock()
	delete(c.loads, id)
	c.loadsMu.Unlock()
	return l.link.Clone(), l.err
}

// store saves e unless an invalidation happened since gen was read.
func (c *Cache) store(sh *shard, key string, gen uint64, e entry) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.gen != gen {
		return
	}
	if len(sh.entries) >= c.perShard {
		c.evict(sh)
	}
	sh.entries[key] = e
}

// evict drops expired entries, and if that isn't enough, arbitrary ones
// (Go's map order is random, which makes this a cheap random eviction).
func (c *Cache) evict(sh *shard) {
	now := c.now()
	for k, e := range sh.entries {
		if !now.Before(e.expires) {
			delete(sh.entries, k)
		}
	}
	for k := range sh.entries {
		if len(sh.entries) < c.perShard {
			break
		}
		delete(sh.entries, k)
	}
}

// Invalidate implements domain.CacheInvalidator. Call it after the change
// has been committed, so the next lookup reads the new row.
func (c *Cache) Invalidate(code string) {
	key := domain.NormalizeCode(code)
	sh := c.shardFor(key)
	sh.mu.Lock()
	sh.gen++
	delete(sh.entries, key)
	sh.mu.Unlock()
}

// Len returns the number of cached entries, for tests and metrics.
func (c *Cache) Len() int {
	n := 0
	for i := range c.shards {
		c.shards[i].mu.Lock()
		n += len(c.shards[i].entries)
		c.shards[i].mu.Unlock()
	}
	return n
}
