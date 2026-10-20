package memstore

import (
	"sync"

	"pathmaxing/internal/domain"
)

// ClickLog is the default click sink. It keeps the most recent events in a
// fixed-size ring, so it never grows and Record never blocks. The Postgres
// click queue replaces it once a database is configured.
type ClickLog struct {
	mu    sync.Mutex
	buf   []domain.ClickEvent
	next  int
	full  bool
	total int64
}

// NewClickLog returns a log that keeps the last size events.
func NewClickLog(size int) *ClickLog {
	if size < 1 {
		size = 1
	}
	return &ClickLog{buf: make([]domain.ClickEvent, size)}
}

// Record implements domain.ClickSink.
func (c *ClickLog) Record(e domain.ClickEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf[c.next] = e
	c.next = (c.next + 1) % len(c.buf)
	if c.next == 0 {
		c.full = true
	}
	c.total++
}

// Events returns the kept events, oldest first.
func (c *ClickLog) Events() []domain.ClickEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.full {
		return append([]domain.ClickEvent(nil), c.buf[:c.next]...)
	}
	out := make([]domain.ClickEvent, 0, len(c.buf))
	out = append(out, c.buf[c.next:]...)
	return append(out, c.buf[:c.next]...)
}

// Total returns how many events were ever recorded, including ones the ring
// has since overwritten.
func (c *ClickLog) Total() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}
