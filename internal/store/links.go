package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"pathmaxing/internal/domain"
)

// linkColumns is the one place that lists what a link row holds, so every
// query returns links the same way. Device targets come along as JSON, which
// saves a second round trip on the redirect path's cache misses.
const linkColumns = `
	l.id, l.workspace_id, l.owner_id, l.code, l.destination, l.title, l.status, l.trust_state,
	l.starts_at, l.expires_at, l.max_clicks, l.cap_used, l.password_hash, l.last_checked_at,
	l.created_at, l.updated_at, l.deleted_at,
	COALESCE((SELECT json_agg(json_build_object('device', r.device, 'destination', r.destination) ORDER BY r.device)
	          FROM redirect_rules r WHERE r.link_id = l.id), '[]')`

type targetRow struct {
	Device      string `json:"device"`
	Destination string `json:"destination"`
}

func scanLink(row pgx.Row) (domain.Link, error) {
	var (
		l       domain.Link
		status  string
		trust   string
		targets []targetRow
	)
	err := row.Scan(
		&l.ID, &l.WorkspaceID, &l.OwnerID, &l.Code, &l.Destination, &l.Title, &status, &trust,
		&l.StartsAt, &l.ExpiresAt, &l.MaxClicks, &l.CapUsed, &l.PasswordHash, &l.LastCheckedAt,
		&l.CreatedAt, &l.UpdatedAt, &l.DeletedAt, &targets,
	)
	if err != nil {
		return domain.Link{}, err
	}
	l.Status = domain.LinkStatus(status)
	l.TrustState = domain.TrustState(trust)
	for _, t := range targets {
		l.Targets = append(l.Targets, domain.Target{Device: domain.Device(t.Device), Destination: t.Destination})
	}
	return l, nil
}

func notFound(what string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, domain.ErrNotFound)
	}
	return err
}

// ResolveCode implements domain.LinkResolver. It uses the lower(code) index
// and returns deleted links too, so the redirect can answer 410 for them.
func (s *Store) ResolveCode(ctx context.Context, code string) (domain.Link, error) {
	l, err := scanLink(s.pool.QueryRow(ctx, `SELECT `+linkColumns+` FROM links l WHERE lower(l.code) = lower($1)`, code))
	return l, wrap("resolve code", notFound("code "+code, err))
}

// GetLink implements domain.LinkStore.
func (s *Store) GetLink(ctx context.Context, id int64) (domain.Link, error) {
	l, err := scanLink(s.pool.QueryRow(ctx, `SELECT `+linkColumns+` FROM links l WHERE l.id = $1`, id))
	return l, wrap("get link", notFound(fmt.Sprintf("link %d", id), err))
}

// ReserveClick implements domain.CapReserver with one conditional UPDATE.
// Postgres locks the row for the update, so concurrent reservations queue up
// and re-check cap_used < max: the cap can never be overshot.
func (s *Store) ReserveClick(ctx context.Context, linkID, max int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE links SET cap_used = cap_used + 1 WHERE id = $1 AND cap_used < $2`, linkID, max)
	if err != nil {
		return false, wrap("reserve click", err)
	}
	return tag.RowsAffected() == 1, nil
}

// NewLink is what CreateLink needs; validation happens before it.
type NewLink struct {
	WorkspaceID  int64
	OwnerID      int64
	Alias        string // empty: generate a random code
	Destination  string
	Title        string
	StartsAt     *time.Time
	ExpiresAt    *time.Time
	MaxClicks    *int64
	PasswordHash []byte
	Targets      []domain.Target
}

// generatedCodeAttempts bounds the retries for random codes. With 36^8
// codes a second collision in a row is already vanishingly unlikely.
const generatedCodeAttempts = 5

// ErrAliasTaken means another link, live or deleted, already has the alias
// in some letter case.
var ErrAliasTaken = fmt.Errorf("alias is already taken: %w", domain.ErrConflict)

// CreateLink inserts a link and its device targets in one transaction.
//
// It never checks whether a code is free before inserting: two people asking
// for "Sale" and "sale" at the same moment would both see it free. Instead it
// inserts and lets the unique index on lower(code) decide. A custom alias
// that loses gets ErrAliasTaken; a random code that collides is replaced and
// the insert retried.
func (s *Store) CreateLink(ctx context.Context, in NewLink) (domain.Link, error) {
	attempts := 1
	if in.Alias == "" {
		attempts = generatedCodeAttempts
	}
	for i := 0; i < attempts; i++ {
		code := in.Alias
		if code == "" {
			code = s.newCode()
		}
		id, err := s.insertLink(ctx, in, code)
		if uniqueViolation(err, "links_code_lower_key") {
			if in.Alias != "" {
				return domain.Link{}, ErrAliasTaken
			}
			s.log.Info("generated code collided; retrying", "attempt", i+1)
			continue
		}
		if err != nil {
			return domain.Link{}, wrap("create link", err)
		}
		return s.GetLink(ctx, id)
	}
	return domain.Link{}, wrap("create link", fmt.Errorf("no free code after %d attempts", attempts))
}

func (s *Store) insertLink(ctx context.Context, in NewLink, code string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) // a no-op once committed

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO links (workspace_id, owner_id, code, destination, title, starts_at, expires_at, max_clicks, password_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		in.WorkspaceID, in.OwnerID, code, in.Destination, strings.TrimSpace(in.Title),
		in.StartsAt, in.ExpiresAt, in.MaxClicks, nullBytes(in.PasswordHash),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err := replaceTargets(ctx, tx, id, in.Targets); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

func replaceTargets(ctx context.Context, tx pgx.Tx, linkID int64, targets []domain.Target) error {
	if _, err := tx.Exec(ctx, `DELETE FROM redirect_rules WHERE link_id = $1`, linkID); err != nil {
		return err
	}
	for _, t := range targets {
		if _, err := tx.Exec(ctx,
			`INSERT INTO redirect_rules (link_id, device, destination) VALUES ($1, $2, $3)`,
			linkID, string(t.Device), t.Destination); err != nil {
			return err
		}
	}
	return nil
}

// DeleteLink soft-deletes a link and returns it as it was, so the caller can
// drop it from the cache. The row stays, so its analytics are kept and its
// code is never handed out again.
func (s *Store) DeleteLink(ctx context.Context, id int64) (domain.Link, error) {
	l, err := scanLink(s.pool.QueryRow(ctx, `
		UPDATE links l SET deleted_at = now(), updated_at = now()
		WHERE l.id = $1 AND l.deleted_at IS NULL
		RETURNING `+linkColumns, id))
	return l, wrap("delete link", notFound(fmt.Sprintf("link %d", id), err))
}

func nullBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
