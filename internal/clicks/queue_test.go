package clicks

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"pathmaxing/internal/domain"
)

// fakeWriter records the batches it receives and can fail the first calls.
type fakeWriter struct {
	mu      sync.Mutex
	batches [][]domain.ClickEvent
	failN   int
	calls   int
}

func (f *fakeWriter) InsertClicks(_ context.Context, events []domain.ClickEvent) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failN {
		return 0, errors.New("database is down")
	}
	f.batches = append(f.batches, append([]domain.ClickEvent(nil), events...))
	return int64(len(events)), nil
}

func (f *fakeWriter) sizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for _, b := range f.batches {
		out = append(out, len(b))
	}
	return out
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func event(i int) domain.ClickEvent {
	return domain.ClickEvent{EventID: domain.NewEventID(), LinkID: int64(i), Outcome: domain.OutcomeRedirected, Class: domain.ClassHuman}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRecordDropsWhenFull(t *testing.T) {
	q := New(&fakeWriter{}, Options{QueueSize: 2, Log: quiet()})
	done := make(chan struct{})
	go func() {
		for i := range 5 {
			q.Record(event(i)) // nothing drains the queue, and this must still not block
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Record blocked on a full queue")
	}
	if q.Dropped() != 3 {
		t.Fatalf("dropped %d, want 3", q.Dropped())
	}
}

func TestBatching(t *testing.T) {
	tests := []struct {
		name      string
		batchSize int
		interval  time.Duration
		events    int
		want      []int // batch sizes written before shutdown
	}{
		{name: "full batches go at once", batchSize: 3, interval: time.Hour, events: 7, want: []int{3, 3}},
		{name: "a partial batch goes after the interval", batchSize: 100, interval: 20 * time.Millisecond, events: 2, want: []int{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &fakeWriter{}
			q := New(w, Options{BatchSize: tt.batchSize, FlushInterval: tt.interval, Log: quiet()})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- q.Run(ctx) }()
			for i := range tt.events {
				q.Record(event(i))
			}
			waitFor(t, "batches", func() bool { return len(w.sizes()) == len(tt.want) })
			if got := w.sizes(); !slices.Equal(got, tt.want) {
				t.Fatalf("batches %v, want %v", got, tt.want)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			total := 0
			for _, n := range w.sizes() {
				total += n
			}
			if total != tt.events || q.Written() != int64(tt.events) {
				t.Fatalf("wrote %d (counter %d), want %d", total, q.Written(), tt.events)
			}
		})
	}
}

func TestShutdownFlushesEverything(t *testing.T) {
	w := &fakeWriter{}
	q := New(w, Options{BatchSize: 500, FlushInterval: time.Hour, Log: quiet()})
	for i := range 1200 {
		q.Record(event(i))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already shutting down: Run must drain and write, not return early
	if err := q.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.sizes(); !slices.Equal(got, []int{500, 500, 200}) {
		t.Fatalf("batches %v, want [500 500 200]", got)
	}
}

func TestRetries(t *testing.T) {
	tests := []struct {
		name        string
		failN       int
		wantCalls   int
		wantBatches []int
		wantDropped int64
	}{
		{name: "recovers after two failures", failN: 2, wantCalls: 3, wantBatches: []int{2}},
		{name: "gives up after max attempts", failN: 100, wantCalls: maxAttempts, wantDropped: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &fakeWriter{failN: tt.failN}
			committed := 0
			q := New(w, Options{BatchSize: 2, FlushInterval: time.Hour, Log: quiet(),
				OnCommit: func(ev []domain.ClickEvent) { committed += len(ev) }})
			q.backoff = func(int) time.Duration { return time.Millisecond }
			q.Record(event(1))
			q.Record(event(2))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := q.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if w.calls != tt.wantCalls || !slices.Equal(w.sizes(), tt.wantBatches) || q.Dropped() != tt.wantDropped {
				t.Fatalf("calls %d, batches %v, dropped %d", w.calls, w.sizes(), q.Dropped())
			}
			if want := 2 - int(tt.wantDropped); committed != want {
				t.Fatalf("OnCommit saw %d events, want %d", committed, want)
			}
		})
	}
}
