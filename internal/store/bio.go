package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"pathmaxing/internal/domain"
)

// ErrSlugTaken means another bio page has the slug in some letter case.
var ErrSlugTaken = fmt.Errorf("slug is already taken: %w", domain.ErrConflict)

// BioPage is a link-in-bio page.
type BioPage struct {
	ID          int64
	WorkspaceID int64
	Slug        string
	Title       string
	LinkIDs     []int64 // in display order
}

const bioColumns = `b.id, b.workspace_id, b.slug, b.title,
	COALESCE((SELECT array_agg(bl.link_id ORDER BY bl.position) FROM bio_page_links bl WHERE bl.bio_page_id = b.id), '{}')`

func scanBio(row pgx.Row) (BioPage, error) {
	var b BioPage
	err := row.Scan(&b.ID, &b.WorkspaceID, &b.Slug, &b.Title, &b.LinkIDs)
	return b, err
}

// ListBioPages returns a workspace's bio pages.
func (s *Store) ListBioPages(ctx context.Context, workspaceID int64) ([]BioPage, error) {
	return collect(ctx, s, "list bio pages", scanBio,
		`SELECT `+bioColumns+` FROM bio_pages b WHERE b.workspace_id = $1 ORDER BY b.id DESC`, workspaceID)
}

// GetBioPage returns one bio page.
func (s *Store) GetBioPage(ctx context.Context, id int64) (BioPage, error) {
	b, err := scanBio(s.pool.QueryRow(ctx, `SELECT `+bioColumns+` FROM bio_pages b WHERE b.id = $1`, id))
	return b, wrap("get bio page", notFound(fmt.Sprintf("bio page %d", id), err))
}

// BioPageBySlug finds a page by slug, ignoring case.
func (s *Store) BioPageBySlug(ctx context.Context, slug string) (BioPage, error) {
	b, err := scanBio(s.pool.QueryRow(ctx, `SELECT `+bioColumns+` FROM bio_pages b WHERE lower(b.slug) = lower($1)`, slug))
	return b, wrap("bio page by slug", notFound("bio page "+slug, err))
}

// SaveBioPage inserts b when b.ID is 0, updates it otherwise, and replaces
// its links, all in one transaction.
func (s *Store) SaveBioPage(ctx context.Context, b BioPage) (BioPage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BioPage{}, wrap("save bio page", err)
	}
	defer tx.Rollback(ctx) // a no-op once committed
	if b.ID == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO bio_pages (workspace_id, slug, title) VALUES ($1, $2, $3) RETURNING id`,
			b.WorkspaceID, b.Slug, strings.TrimSpace(b.Title)).Scan(&b.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `UPDATE bio_pages SET slug = $2, title = $3, updated_at = now() WHERE id = $1`,
			b.ID, b.Slug, strings.TrimSpace(b.Title))
		if err == nil && tag.RowsAffected() == 0 {
			err = fmt.Errorf("bio page %d: %w", b.ID, domain.ErrNotFound)
		}
	}
	if uniqueViolation(err, "bio_pages_slug_lower_key") {
		return BioPage{}, ErrSlugTaken
	}
	if err != nil {
		return BioPage{}, wrap("save bio page", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM bio_page_links WHERE bio_page_id = $1`, b.ID); err != nil {
		return BioPage{}, wrap("save bio page", err)
	}
	if len(b.LinkIDs) > 0 {
		_, err := tx.Exec(ctx, `
			INSERT INTO bio_page_links (bio_page_id, link_id, position)
			SELECT $1, id, pos FROM unnest($2::bigint[]) WITH ORDINALITY AS t(id, pos)`, b.ID, b.LinkIDs)
		if err != nil {
			return BioPage{}, wrap("save bio page", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return BioPage{}, wrap("save bio page", err)
	}
	return s.GetBioPage(ctx, b.ID)
}

// LiveLinksInWorkspace counts how many of ids are live links of workspaceID.
func (s *Store) LiveLinksInWorkspace(ctx context.Context, workspaceID int64, ids []int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM links WHERE workspace_id = $1 AND id = ANY($2) AND deleted_at IS NULL`,
		workspaceID, ids).Scan(&n)
	return n, wrap("count links", err)
}

// BioLinks returns the links a public bio page shows, in order: only those a
// visitor could open right now, so the page never offers a dead link.
func (s *Store) BioLinks(ctx context.Context, pageID int64) ([]domain.Link, error) {
	return collect(ctx, s, "bio links", scanLink, `
		SELECT `+linkColumns+`
		FROM bio_page_links bl JOIN links l ON l.id = bl.link_id
		WHERE bl.bio_page_id = $1 AND l.deleted_at IS NULL AND l.status = 'active' AND l.trust_state <> 'quarantined'
		  AND (l.starts_at IS NULL OR l.starts_at <= now()) AND (l.expires_at IS NULL OR l.expires_at > now())
		  AND (l.max_clicks IS NULL OR l.cap_used < l.max_clicks)
		ORDER BY bl.position`, pageID)
}
