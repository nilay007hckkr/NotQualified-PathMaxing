// Package clicks records visits without slowing redirects down. The redirect
// handler hands every click event to a bounded in-memory queue; a background
// worker drains it and writes the events to Postgres in batches.
package clicks

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"pathmaxing/internal/domain"
)

// Writer persists a batch of events and reports how many were new. It must
// be idempotent on EventID, because a batch whose write timed out may have
// committed anyway and is sent again.
type Writer interface {
	InsertClicks(ctx context.Context, events []domain.ClickEvent) (int64, error)
}

// Options configure a Queue. Zero values take the defaults from .env.example.
type Options struct {
	QueueSize     int           // CLICK_QUEUE_SIZE, default 10000
	BatchSize     int           // CLICK_BATCH_SIZE, default 500
	FlushInterval time.Duration // CLICK_FLUSH_INTERVAL, default 1s
	Log           *slog.Logger
	// OnCommit, when set, sees each batch after it has been written, for
	// live updates. It runs on the writer goroutine and must not keep the
	// slice.
	OnCommit func(events []domain.ClickEvent)
}

const (
	maxAttempts  = 5
	writeTimeout = 10 * time.Second
)

// Queue implements domain.ClickSink.
type Queue struct {
	ch      chan domain.ClickEvent
	w       Writer
	opts    Options
	backoff func(attempt int) time.Duration
	dropped atomic.Int64
	written atomic.Int64
}

// New returns a queue that writes through w once Run is started.
func New(w Writer, opts Options) *Queue {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 10000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Queue{
		ch:   make(chan domain.ClickEvent, opts.QueueSize),
		w:    w,
		opts: opts,
		backoff: func(attempt int) time.Duration {
			return time.Duration(100<<attempt) * time.Millisecond // 200ms, 400ms, 800ms, ...
		},
	}
}

// Record queues e and returns at once. When the queue is full the event is
// dropped and counted: losing a click from the analytics is better than
// making visitors wait for the database during a spike or an outage.
func (q *Queue) Record(e domain.ClickEvent) {
	select {
	case q.ch <- e:
	default:
		if n := q.dropped.Add(1); n == 1 || n%1000 == 0 {
			q.opts.Log.Warn("click queue full; dropping events", "dropped_total", n)
		}
	}
}

// Dropped returns how many events were dropped because the queue was full
// or a batch kept failing.
func (q *Queue) Dropped() int64 { return q.dropped.Load() }

// Written returns how many new events have been written.
func (q *Queue) Written() int64 { return q.written.Load() }

// Run writes queued events in batches of BatchSize, or whatever has arrived
// once FlushInterval passes, until ctx is cancelled. The app cancels ctx only
// after the HTTP server has stopped, so nothing is queued after that: Run then
// drains the queue, writes the rest and returns.
func (q *Queue) Run(ctx context.Context) error {
	batch := make([]domain.ClickEvent, 0, q.opts.BatchSize)
	ticker := time.NewTicker(q.opts.FlushInterval)
	defer ticker.Stop()
	flush := func() {
		if len(batch) > 0 {
			q.flush(ctx, batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case e := <-q.ch:
			batch = append(batch, e)
			if len(batch) >= q.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case e := <-q.ch:
					batch = append(batch, e)
					if len(batch) >= q.opts.BatchSize {
						flush()
					}
				default:
					flush()
					q.opts.Log.Info("click writer stopped", "written", q.written.Load(), "dropped", q.dropped.Load())
					return nil
				}
			}
		}
	}
}

// flush writes one batch, retrying with backoff. It deliberately ignores the
// cancellation of ctx (each attempt has its own timeout), so the final flush
// during shutdown still reaches the database.
func (q *Queue) flush(ctx context.Context, batch []domain.ClickEvent) {
	base := context.WithoutCancel(ctx)
	for attempt := 1; ; attempt++ {
		wctx, cancel := context.WithTimeout(base, writeTimeout)
		n, err := q.w.InsertClicks(wctx, batch)
		cancel()
		if err == nil {
			q.written.Add(n)
			if q.opts.OnCommit != nil {
				q.opts.OnCommit(batch)
			}
			return
		}
		if attempt == maxAttempts {
			q.dropped.Add(int64(len(batch)))
			q.opts.Log.Error("click batch failed; dropping it", "events", len(batch), "attempts", attempt, "err", err)
			return
		}
		q.opts.Log.Warn("click batch failed; retrying", "events", len(batch), "attempt", attempt, "err", err)
		time.Sleep(q.backoff(attempt))
	}
}
