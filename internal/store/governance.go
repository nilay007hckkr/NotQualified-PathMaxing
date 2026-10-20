package store

import (
	"context"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/urlcheck"
)

// Blocked implements domain.Blocklist from the in-memory copy, because the
// redirect path asks on every visit.
func (s *Store) Blocked(_ context.Context, host string) (string, bool, error) {
	rule, ok := s.blocked.Load().Match(host)
	return rule, ok, nil
}

// ReloadBlocklist reads blocked_domains and swaps in a fresh copy.
func (s *Store) ReloadBlocklist(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT domain, reason FROM blocked_domains`)
	if err != nil {
		return wrap("load blocklist", err)
	}
	defer rows.Close()
	entries := make(map[string]string)
	for rows.Next() {
		var d, reason string
		if err := rows.Scan(&d, &reason); err != nil {
			return wrap("load blocklist", err)
		}
		entries[d] = reason
	}
	if err := rows.Err(); err != nil {
		return wrap("load blocklist", err)
	}
	s.blocked.Store(urlcheck.NewDomainSet(entries))
	return nil
}

// Append implements domain.AuditLog.
func (s *Store) Append(ctx context.Context, e domain.AuditEntry) error {
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	var at *time.Time
	if !e.At.IsZero() {
		at = &e.At
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log (actor_id, action, entity_type, entity_id, detail, ts)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6, now()))`,
		e.ActorID, e.Action, e.EntityType, e.EntityID, detail, at)
	return wrap("append audit entry", err)
}
