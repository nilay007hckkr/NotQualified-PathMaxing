package trust

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/threatintel"
)

// Repo stores trust state and check history.
type Repo interface {
	TrustState(ctx context.Context, linkID int64) (State, error)
	// SaveCheck records a check and the link's new state in one transaction.
	SaveCheck(ctx context.Context, linkID int64, rec CheckRecord, s State, nextCheck time.Time) error
	History(ctx context.Context, linkID int64, limit int) ([]CheckRecord, error)
	// Due returns up to limit links whose next check is due at now.
	Due(ctx context.Context, now time.Time, limit int) ([]int64, error)
	// ScheduleNow makes a link due immediately (a forced re-scan).
	ScheduleNow(ctx context.Context, linkID int64) error
	// NextCheckAt returns when the link is due next; nil means now.
	NextCheckAt(ctx context.Context, linkID int64) (*time.Time, error)
}

// Fetcher fetches a destination; *Checker is the real one.
type Fetcher interface {
	Check(ctx context.Context, rawURL string) Result
}

// Scanner runs one trust check of one link and applies the result.
type Scanner struct {
	Links       domain.LinkStore
	Repo        Repo
	Fetch       Fetcher
	Feeds       *threatintel.Set
	Blocklist   domain.Blocklist
	Invalidator domain.CacheInvalidator
	Audit       domain.AuditLog
	Log         *slog.Logger
	Now         func() time.Time
	// NextCheck decides when to look again; the scheduler sets it.
	NextCheck func(l domain.Link, checks int, t Transition, now time.Time) time.Time
	// OnTransition, when set, hears every check (alerts use it).
	OnTransition func(ctx context.Context, l domain.Link, t Transition)
}

// Scan checks one link now.
func (s *Scanner) Scan(ctx context.Context, linkID int64) (Transition, error) {
	l, err := s.Links.GetLink(ctx, linkID)
	if err != nil {
		return Transition{}, err
	}
	prev, err := s.Repo.TrustState(ctx, linkID)
	if err != nil {
		return Transition{}, err
	}
	now := s.Now()

	fetch := s.Fetch.Check(ctx, l.Destination)
	obs := Observation{Destination: l.Destination, Fetch: fetch}

	// Ask the feeds about the URL as written and, if it redirects, about where
	// it really ends: a clean short hop to a phishing page must still count.
	sources := s.Feeds.Feeds()
	targets := []string{l.Destination}
	if fetch.FinalURL != "" && fetch.FinalURL != l.Destination {
		targets = append(targets, fetch.FinalURL)
	}
	for _, u := range targets {
		rep := s.Feeds.Check(ctx, u)
		if rep.Malicious {
			obs.Threat = true
			for _, m := range rep.Matches {
				obs.ThreatWhy = append(obs.ThreatWhy, m.Source+": "+m.Detail)
			}
		}
	}
	sources = append(sources, "blocklist")
	for _, h := range fetch.HopHosts {
		if rule, blocked, err := s.Blocklist.Blocked(ctx, h); err == nil && blocked {
			obs.BlockedBy = rule
			break
		}
	}

	t := Evaluate(prev, obs)
	rec := CheckRecord{
		CheckedAt: now, Destination: l.Destination, HTTPStatus: fetch.Status, FinalURL: fetch.FinalURL,
		FinalDomain: fetch.FinalHost, Hops: fetch.Hops, Error: fetch.Error,
		Verdict: t.Verdict, Sources: sources, Detail: t.Detail,
	}
	next := now.Add(24 * time.Hour)
	if s.NextCheck != nil {
		history, _ := s.Repo.History(ctx, linkID, 3)
		next = s.NextCheck(l, len(history)+1, t, now)
	}
	if err := s.Repo.SaveCheck(ctx, linkID, rec, t.Next, next); err != nil {
		return Transition{}, fmt.Errorf("save check: %w", err)
	}

	if t.Next.Trust != prev.Trust {
		// After the commit: the next visit must read the new state.
		s.Invalidator.Invalidate(l.Code)
	}
	if t.Quarantined {
		s.Log.Warn("link quarantined", "link_id", l.ID, "code", l.Code, "why", t.Detail)
		if err := s.Audit.Append(ctx, domain.AuditEntry{
			Action: "link.quarantined", EntityType: "link", EntityID: l.ID,
			Detail: map[string]any{"reason": t.Detail, "destination": l.Destination},
		}); err != nil {
			s.Log.Error("audit quarantine", "link_id", l.ID, "err", err)
		}
	}
	if s.OnTransition != nil {
		s.OnTransition(ctx, l, t)
	}
	return t, nil
}
