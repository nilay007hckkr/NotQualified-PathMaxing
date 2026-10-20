package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/urlcheck"
)

// BlockedDomains returns the current in-memory blocklist, for validating a
// destination before it is saved.
func (s *Store) BlockedDomains() *urlcheck.DomainSet { return s.blocked.Load() }

// LinkFilter selects links for a list. Deleted links are never listed.
type LinkFilter struct {
	WorkspaceID int64 // ignored when All is set
	All         bool  // admins: every workspace
	Q           string
	Status      string
	TrustState  string
	BeforeID    int64 // cursor: only links with a smaller id
	Limit       int
}

// ListLinks returns links newest first. Paging by id rather than by offset
// keeps pages stable while links are being created.
func (s *Store) ListLinks(ctx context.Context, f LinkFilter) ([]domain.Link, error) {
	var (
		where = []string{"l.deleted_at IS NULL"}
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if !f.All {
		where = append(where, "l.workspace_id = "+arg(f.WorkspaceID))
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		p := arg("%" + likeEscaper.Replace(q) + "%")
		where = append(where, fmt.Sprintf("(l.code ILIKE %[1]s OR l.title ILIKE %[1]s OR l.destination ILIKE %[1]s)", p))
	}
	if f.Status != "" {
		where = append(where, "l.status = "+arg(f.Status))
	}
	if f.TrustState != "" {
		where = append(where, "l.trust_state = "+arg(f.TrustState))
	}
	if f.BeforeID > 0 {
		where = append(where, "l.id < "+arg(f.BeforeID))
	}
	limit := arg(f.Limit)
	rows, err := s.pool.Query(ctx, `SELECT `+linkColumns+` FROM links l WHERE `+strings.Join(where, " AND ")+
		` ORDER BY l.id DESC LIMIT `+limit, args...)
	if err != nil {
		return nil, wrap("list links", err)
	}
	defer rows.Close()
	var out []domain.Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, wrap("list links", err)
		}
		out = append(out, l)
	}
	return out, wrap("list links", rows.Err())
}

// likeEscaper makes a search term match literally inside ILIKE.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// LinkUpdate is the full new state of a link's editable fields; the API
// merges a partial PATCH into the current link and validates the result
// before calling UpdateLink.
type LinkUpdate struct {
	Destination  string
	Title        string
	StartsAt     *time.Time
	ExpiresAt    *time.Time
	MaxClicks    *int64
	PasswordHash []byte
	Targets      []domain.Target
}

// UpdateLink writes the editable fields and the device targets in one
// transaction. cap_used is left alone: only the atomic reservation moves it.
func (s *Store) UpdateLink(ctx context.Context, id int64, u LinkUpdate) (domain.Link, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Link{}, wrap("update link", err)
	}
	defer tx.Rollback(ctx) // a no-op once committed
	tag, err := tx.Exec(ctx, `
		UPDATE links SET destination = $2, title = $3, starts_at = $4, expires_at = $5, max_clicks = $6,
		                 password_hash = $7, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`,
		id, u.Destination, strings.TrimSpace(u.Title), u.StartsAt, u.ExpiresAt, u.MaxClicks, nullBytes(u.PasswordHash))
	if err != nil {
		return domain.Link{}, wrap("update link", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Link{}, wrap("update link", fmt.Errorf("link %d: %w", id, domain.ErrNotFound))
	}
	if err := replaceTargets(ctx, tx, id, u.Targets); err != nil {
		return domain.Link{}, wrap("update link", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Link{}, wrap("update link", err)
	}
	return s.GetLink(ctx, id)
}

// ErrDisabledByAdmin means an owner tried to change the status of a link an
// admin disabled; only an admin can lift that.
var ErrDisabledByAdmin = fmt.Errorf("link was disabled by an admin: %w", domain.ErrConflict)

// SetStatus changes a live link's status. With byAdmin false it refuses to
// touch an admin-disabled link, in the same statement, so an owner's resume
// can't race an admin's takedown.
func (s *Store) SetStatus(ctx context.Context, id int64, status domain.LinkStatus, reason string, byAdmin bool) (domain.Link, error) {
	l, err := scanLink(s.pool.QueryRow(ctx, `
		UPDATE links l SET status = $2, disabled_reason = $3, updated_at = now()
		WHERE l.id = $1 AND l.deleted_at IS NULL AND ($4 OR l.status <> 'disabled_admin')
		RETURNING `+linkColumns, id, string(status), reason, byAdmin))
	if err == nil {
		return l, nil
	}
	err = notFound(fmt.Sprintf("link %d", id), err)
	if !byAdmin && errors.Is(err, domain.ErrNotFound) {
		if cur, gerr := s.GetLink(ctx, id); gerr == nil && !cur.Deleted() && cur.Status == domain.StatusDisabledAdmin {
			return domain.Link{}, ErrDisabledByAdmin
		}
	}
	return domain.Link{}, wrap("set status", err)
}
