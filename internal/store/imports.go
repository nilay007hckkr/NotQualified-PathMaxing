package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"pathmaxing/internal/domain"
)

// ErrAlreadyCommitted means an import was committed before.
var ErrAlreadyCommitted = fmt.Errorf("import was already committed: %w", domain.ErrConflict)

// ImportRow is one row of an import with its validation result.
type ImportRow struct {
	Line        int
	Destination string
	Alias       string
	Title       string
	Campaign    string
	Tags        []string
	ExpiresAt   *time.Time
	MaxClicks   *int64
	Status      string // valid or invalid
	Errors      []string
	LinkID      *int64
}

// ImportJob is an uploaded file and its rows, in file order.
type ImportJob struct {
	ID          int64
	WorkspaceID int64
	OwnerID     int64
	Status      string // preview or committed
	Rows        []ImportRow
}

// CreateImport stores a previewed file in one transaction.
func (s *Store) CreateImport(ctx context.Context, workspaceID, ownerID int64, rows []ImportRow) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, wrap("create import", err)
	}
	defer tx.Rollback(ctx) // a no-op once committed
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO bulk_import_jobs (workspace_id, owner_id) VALUES ($1, $2) RETURNING id`,
		workspaceID, ownerID).Scan(&id); err != nil {
		return 0, wrap("create import", err)
	}
	src := make([][]any, 0, len(rows))
	for _, r := range rows {
		src = append(src, []any{id, r.Line, r.Destination, r.Alias, r.Title, r.Campaign, nonNil(r.Tags),
			r.ExpiresAt, r.MaxClicks, r.Status, nonNil(r.Errors)})
	}
	_, err = tx.CopyFrom(ctx, pgx.Identifier{"bulk_import_rows"},
		[]string{"job_id", "row_no", "destination", "alias", "title", "campaign", "tags", "expires_at", "max_clicks", "status", "errors"},
		pgx.CopyFromRows(src))
	if err != nil {
		return 0, wrap("create import", err)
	}
	return id, wrap("create import", tx.Commit(ctx))
}

// GetImport returns a job and its rows.
func (s *Store) GetImport(ctx context.Context, id int64) (ImportJob, error) {
	var j ImportJob
	err := s.pool.QueryRow(ctx, `SELECT id, workspace_id, owner_id, status FROM bulk_import_jobs WHERE id = $1`, id).
		Scan(&j.ID, &j.WorkspaceID, &j.OwnerID, &j.Status)
	if err != nil {
		return ImportJob{}, wrap("get import", notFound(fmt.Sprintf("import %d", id), err))
	}
	j.Rows, err = collect(ctx, s, "get import", func(row pgx.Row) (ImportRow, error) {
		var r ImportRow
		err := row.Scan(&r.Line, &r.Destination, &r.Alias, &r.Title, &r.Campaign, &r.Tags, &r.ExpiresAt, &r.MaxClicks,
			&r.Status, &r.Errors, &r.LinkID)
		return r, err
	}, `SELECT row_no, destination, alias, title, campaign, tags, expires_at, max_clicks, status, errors, link_id
	    FROM bulk_import_rows WHERE job_id = $1 ORDER BY row_no`, id)
	return j, err
}

// MarkImportCommitted moves a job from preview to committed. Only one
// caller can win, so a double-clicked commit creates the links once.
func (s *Store) MarkImportCommitted(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE bulk_import_jobs SET status = 'committed', committed_at = now() WHERE id = $1 AND status = 'preview'`, id)
	if err != nil {
		return wrap("commit import", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyCommitted
	}
	return nil
}

// UpdateImportRows records what committing did to each row, in one round
// trip.
func (s *Store) UpdateImportRows(ctx context.Context, jobID int64, rows []ImportRow) error {
	b := &pgx.Batch{}
	for _, r := range rows {
		b.Queue(`UPDATE bulk_import_rows SET status = $3, errors = $4, link_id = $5 WHERE job_id = $1 AND row_no = $2`,
			jobID, r.Line, r.Status, nonNil(r.Errors), r.LinkID)
	}
	return wrap("update import rows", s.pool.SendBatch(ctx, b).Close())
}

// TakenCodes returns which of codes (lowercased) some link, live or
// deleted, already uses. It is only a preview: creating the link still
// relies on the unique index.
func (s *Store) TakenCodes(ctx context.Context, codes []string) (map[string]bool, error) {
	taken := map[string]bool{}
	if len(codes) == 0 {
		return taken, nil
	}
	lower := make([]string, len(codes))
	for i, c := range codes {
		lower[i] = strings.ToLower(c)
	}
	rows, err := s.pool.Query(ctx, `SELECT lower(code) FROM links WHERE lower(code) = ANY($1)`, lower)
	if err != nil {
		return nil, wrap("taken codes", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, wrap("taken codes", err)
	}
	for _, c := range found {
		taken[c] = true
	}
	return taken, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
