// Package admin holds the governance actions: taking a link down and
// bringing it back, and blocking or unblocking a domain. Each one commits
// the change, then drops the cached link (or reloads the blocklist) so it
// applies from the next visit, then writes the audit entry.
package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/store"
	"pathmaxing/internal/urlcheck"
)

// ErrInvalidDomain means the admin typed something that is not a domain.
var ErrInvalidDomain = errors.New("not a domain name")

// Service runs admin actions.
type Service struct {
	Store *store.Store
	Cache domain.CacheInvalidator
	Log   *slog.Logger
}

// Disable takes a link down. The redirect answers 451 from the next visit.
func (s *Service) Disable(ctx context.Context, actor int64, linkID int64, reason string) (domain.Link, error) {
	l, err := s.Store.SetStatus(ctx, linkID, domain.StatusDisabledAdmin, strings.TrimSpace(reason), true)
	if err != nil {
		return domain.Link{}, err
	}
	s.Cache.Invalidate(l.Code)
	s.audit(ctx, actor, "link.disabled", "link", l.ID, map[string]any{"code": l.Code, "reason": strings.TrimSpace(reason)})
	return l, nil
}

// Enable brings a link back and releases a quarantine.
func (s *Service) Enable(ctx context.Context, actor int64, linkID int64) (domain.Link, error) {
	l, released, err := s.Store.EnableLink(ctx, linkID)
	if err != nil {
		return domain.Link{}, err
	}
	s.Cache.Invalidate(l.Code)
	s.audit(ctx, actor, "link.enabled", "link", l.ID, map[string]any{"code": l.Code})
	if released {
		s.audit(ctx, actor, "link.released", "link", l.ID, map[string]any{"code": l.Code, "from": string(domain.TrustQuarantined)})
	}
	return l, nil
}

// Block adds a domain (and so its subdomains) to the blocklist. Input may be
// a pasted URL; it is normalized first.
func (s *Service) Block(ctx context.Context, actor int64, input, reason string) (store.BlockedDomain, error) {
	d, err := urlcheck.NormalizeDomain(input)
	if err != nil {
		return store.BlockedDomain{}, fmt.Errorf("%w: %v", ErrInvalidDomain, err)
	}
	b, err := s.Store.AddBlocked(ctx, d, strings.TrimSpace(reason), actor)
	if err != nil {
		return store.BlockedDomain{}, err
	}
	s.audit(ctx, actor, "domain.blocked", "blocked_domain", 0, map[string]any{"domain": b.Domain, "reason": b.Reason})
	return b, nil
}

// Unblock removes a domain from the blocklist.
func (s *Service) Unblock(ctx context.Context, actor int64, input string) error {
	d, err := urlcheck.NormalizeDomain(input)
	if err != nil {
		return fmt.Errorf("blocked domain %q: %w", input, domain.ErrNotFound)
	}
	if err := s.Store.RemoveBlocked(ctx, d); err != nil {
		return err
	}
	s.audit(ctx, actor, "domain.unblocked", "blocked_domain", 0, map[string]any{"domain": d})
	return nil
}

func (s *Service) audit(ctx context.Context, actor int64, action, entity string, id int64, detail map[string]any) {
	err := s.Store.Append(ctx, domain.AuditEntry{ActorID: &actor, Action: action, EntityType: entity, EntityID: id, Detail: detail})
	if err != nil {
		s.Log.Error("audit write failed", "action", action, "err", err)
	}
}
