package trust

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"pathmaxing/internal/domain"
)

// NextCheck is the re-check policy: right after creation (a link with no
// next_check_at is due at once), then after 1 hour, after 24 hours, then
// daily while the link is live and weekly once it is paused, archived or
// expired. A failed check is retried after 30 minutes, so a dead
// destination turns degraded within the hour instead of the next day.
func NextCheck(l domain.Link, checks int, t Transition, now time.Time) time.Time {
	switch {
	case t.Verdict == VerdictFailed:
		return now.Add(30 * time.Minute)
	case checks <= 1:
		return now.Add(time.Hour)
	case checks == 2:
		return now.Add(24 * time.Hour)
	case l.Status == domain.StatusActive && !l.IsExpired(now) && !l.Deleted():
		return now.Add(24 * time.Hour)
	default:
		return now.Add(7 * 24 * time.Hour)
	}
}

// Scheduler finds links that are due and scans them, a few at a time.
type Scheduler struct {
	Scanner     *Scanner
	Repo        Repo
	Interval    time.Duration // how often to look for due links
	Concurrency int
	Batch       int
	Log         *slog.Logger

	kick chan struct{}
	once sync.Once
}

func (s *Scheduler) kicks() chan struct{} {
	s.once.Do(func() { s.kick = make(chan struct{}, 1) })
	return s.kick
}

// Kick wakes the scheduler now instead of at the next interval, so a forced
// re-scan takes seconds, not up to a whole interval.
func (s *Scheduler) Kick() {
	select {
	case s.kicks() <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// Run scans due links until ctx ends.
func (s *Scheduler) Run(ctx context.Context) error {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		s.runOnce(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-s.kicks():
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context) {
	ids, err := s.Repo.Due(ctx, s.Scanner.Now(), s.Batch)
	if err != nil {
		if ctx.Err() == nil {
			s.Log.Warn("trust scheduler: list due links", "err", err)
		}
		return
	}
	sem := make(chan struct{}, max(1, s.Concurrency))
	var wg sync.WaitGroup
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := s.Scanner.Scan(ctx, id); err != nil && ctx.Err() == nil {
				s.Log.Warn("trust scan failed", "link_id", id, "err", err)
			}
		}()
	}
	wg.Wait()
}
