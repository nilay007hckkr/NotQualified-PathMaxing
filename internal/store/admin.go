package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"pathmaxing/internal/domain"
)

// EnableLink lifts an admin's takedown and, when the trust scanner had
// quarantined the link, releases it too: an admin has looked at it. It
// reports whether a quarantine was released.
func (s *Store) EnableLink(ctx context.Context, id int64) (domain.Link, bool, error) {
	old, err := s.GetLink(ctx, id)
	if err != nil {
		return domain.Link{}, false, err
	}
	if old.Deleted() {
		return domain.Link{}, false, wrap("enable link", fmt.Errorf("link %d: %w", id, domain.ErrNotFound))
	}
	l, err := scanLink(s.pool.QueryRow(ctx, `
		UPDATE links l SET status = 'active', disabled_reason = '',
		       trust_state = CASE WHEN l.trust_state = 'quarantined' THEN 'ok' ELSE l.trust_state END,
		       updated_at = now()
		WHERE l.id = $1 AND l.deleted_at IS NULL
		RETURNING `+linkColumns, id))
	if err != nil {
		return domain.Link{}, false, wrap("enable link", notFound(fmt.Sprintf("link %d", id), err))
	}
	return l, old.TrustState == domain.TrustQuarantined, nil
}

// BlockedDomain is one blocklist entry.
type BlockedDomain struct {
	Domain    string
	Reason    string
	CreatedAt time.Time
}

// ListBlocked returns the blocklist, newest first.
func (s *Store) ListBlocked(ctx context.Context) ([]BlockedDomain, error) {
	return collect(ctx, s, "list blocklist", func(row pgx.Row) (BlockedDomain, error) {
		var b BlockedDomain
		err := row.Scan(&b.Domain, &b.Reason, &b.CreatedAt)
		return b, err
	}, `SELECT domain, reason, created_at FROM blocked_domains ORDER BY created_at DESC, domain`)
}

// AddBlocked blocks a normalized domain, or updates the reason when it is
// already blocked, then reloads the in-memory copy so validation and the
// redirect refuse it at once.
func (s *Store) AddBlocked(ctx context.Context, domainName, reason string, by int64) (BlockedDomain, error) {
	var b BlockedDomain
	err := s.pool.QueryRow(ctx, `
		INSERT INTO blocked_domains (domain, reason, created_by) VALUES ($1, $2, $3)
		ON CONFLICT (domain) DO UPDATE SET reason = EXCLUDED.reason
		RETURNING domain, reason, created_at`, domainName, reason, by).Scan(&b.Domain, &b.Reason, &b.CreatedAt)
	if err != nil {
		return BlockedDomain{}, wrap("block domain", err)
	}
	return b, s.ReloadBlocklist(ctx)
}

// RemoveBlocked unblocks a domain and reloads the in-memory copy.
func (s *Store) RemoveBlocked(ctx context.Context, domainName string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM blocked_domains WHERE domain = $1`, domainName)
	if err != nil {
		return wrap("unblock domain", err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("unblock domain", fmt.Errorf("blocked domain %s: %w", domainName, domain.ErrNotFound))
	}
	return s.ReloadBlocklist(ctx)
}

// AuditRecord is a stored audit entry.
type AuditRecord struct {
	ID         int64
	ActorID    *int64
	Action     string
	EntityType string
	EntityID   int64
	Detail     json.RawMessage
	TS         time.Time
}

// ListAudit returns audit entries newest first, before the id cursor.
func (s *Store) ListAudit(ctx context.Context, beforeID int64, limit int) ([]AuditRecord, error) {
	return collect(ctx, s, "list audit", func(row pgx.Row) (AuditRecord, error) {
		var a AuditRecord
		err := row.Scan(&a.ID, &a.ActorID, &a.Action, &a.EntityType, &a.EntityID, &a.Detail, &a.TS)
		return a, err
	}, `SELECT id, actor_id, action, entity_type, entity_id, detail, ts FROM audit_log
	    WHERE ($1 = 0 OR id < $1) ORDER BY id DESC LIMIT $2`, beforeID, limit)
}
