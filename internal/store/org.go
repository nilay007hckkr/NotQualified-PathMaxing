package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"pathmaxing/internal/domain"
)

// ErrNameTaken means the workspace already has a campaign, tag or folder
// with that name in some letter case.
var ErrNameTaken = fmt.Errorf("name is already used: %w", domain.ErrConflict)

func nameConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrNameTaken
	}
	return err
}

// Campaign groups links for reporting.
type Campaign struct {
	ID          int64
	WorkspaceID int64
	Name        string
	UTMSource   string
	UTMMedium   string
	UTMCampaign string
	LinkCount   int64 // live links in the campaign
	CreatedAt   time.Time
}

const campaignColumns = `c.id, c.workspace_id, c.name, c.utm_source, c.utm_medium, c.utm_campaign,
	(SELECT count(*) FROM links l WHERE l.campaign_id = c.id AND l.deleted_at IS NULL), c.created_at`

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.UTMSource, &c.UTMMedium, &c.UTMCampaign, &c.LinkCount, &c.CreatedAt)
	return c, err
}

// ListCampaigns returns a workspace's campaigns by name.
func (s *Store) ListCampaigns(ctx context.Context, workspaceID int64) ([]Campaign, error) {
	return collect(ctx, s, "list campaigns", scanCampaign,
		`SELECT `+campaignColumns+` FROM campaigns c WHERE c.workspace_id = $1 ORDER BY lower(c.name)`, workspaceID)
}

// GetCampaign returns one campaign.
func (s *Store) GetCampaign(ctx context.Context, id int64) (Campaign, error) {
	c, err := scanCampaign(s.pool.QueryRow(ctx, `SELECT `+campaignColumns+` FROM campaigns c WHERE c.id = $1`, id))
	return c, wrap("get campaign", notFound(fmt.Sprintf("campaign %d", id), err))
}

// CampaignByName finds a campaign by name, ignoring case.
func (s *Store) CampaignByName(ctx context.Context, workspaceID int64, name string) (Campaign, error) {
	c, err := scanCampaign(s.pool.QueryRow(ctx,
		`SELECT `+campaignColumns+` FROM campaigns c WHERE c.workspace_id = $1 AND lower(c.name) = lower($2)`,
		workspaceID, strings.TrimSpace(name)))
	return c, wrap("campaign by name", notFound("campaign "+name, err))
}

// SaveCampaign inserts c when c.ID is 0 and updates it otherwise.
func (s *Store) SaveCampaign(ctx context.Context, c Campaign) (Campaign, error) {
	var err error
	if c.ID == 0 {
		err = s.pool.QueryRow(ctx, `
			INSERT INTO campaigns (workspace_id, name, utm_source, utm_medium, utm_campaign)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			c.WorkspaceID, c.Name, c.UTMSource, c.UTMMedium, c.UTMCampaign).Scan(&c.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = s.pool.Exec(ctx, `
			UPDATE campaigns SET name = $2, utm_source = $3, utm_medium = $4, utm_campaign = $5 WHERE id = $1`,
			c.ID, c.Name, c.UTMSource, c.UTMMedium, c.UTMCampaign)
		if err == nil && tag.RowsAffected() == 0 {
			err = fmt.Errorf("campaign %d: %w", c.ID, domain.ErrNotFound)
		}
	}
	if err != nil {
		return Campaign{}, wrap("save campaign", nameConflict(err))
	}
	return s.GetCampaign(ctx, c.ID)
}

// Tag labels links; a link can have many.
type Tag struct {
	ID          int64
	WorkspaceID int64
	Name        string
	Color       string
}

func scanTag(row pgx.Row) (Tag, error) {
	var t Tag
	err := row.Scan(&t.ID, &t.WorkspaceID, &t.Name, &t.Color)
	return t, err
}

// ListTags returns a workspace's tags by name.
func (s *Store) ListTags(ctx context.Context, workspaceID int64) ([]Tag, error) {
	return collect(ctx, s, "list tags", scanTag,
		`SELECT id, workspace_id, name, color FROM tags WHERE workspace_id = $1 ORDER BY lower(name)`, workspaceID)
}

// GetTag returns one tag.
func (s *Store) GetTag(ctx context.Context, id int64) (Tag, error) {
	t, err := scanTag(s.pool.QueryRow(ctx, `SELECT id, workspace_id, name, color FROM tags WHERE id = $1`, id))
	return t, wrap("get tag", notFound(fmt.Sprintf("tag %d", id), err))
}

// SaveTag inserts t when t.ID is 0 and updates it otherwise.
func (s *Store) SaveTag(ctx context.Context, t Tag) (Tag, error) {
	var err error
	if t.ID == 0 {
		err = s.pool.QueryRow(ctx, `INSERT INTO tags (workspace_id, name, color) VALUES ($1, $2, $3) RETURNING id`,
			t.WorkspaceID, t.Name, t.Color).Scan(&t.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = s.pool.Exec(ctx, `UPDATE tags SET name = $2, color = $3 WHERE id = $1`, t.ID, t.Name, t.Color)
		if err == nil && tag.RowsAffected() == 0 {
			err = fmt.Errorf("tag %d: %w", t.ID, domain.ErrNotFound)
		}
	}
	if err != nil {
		return Tag{}, wrap("save tag", nameConflict(err))
	}
	return s.GetTag(ctx, t.ID)
}

// EnsureTags returns the ids of the named tags, creating the missing ones.
// Bulk import uses it, so a CSV can introduce new tags.
func (s *Store) EnsureTags(ctx context.Context, workspaceID int64, names []string) ([]int64, error) {
	var ids []int64
	for _, name := range names {
		var id int64
		// The no-op update makes RETURNING give the id of an existing tag too.
		err := s.pool.QueryRow(ctx, `
			INSERT INTO tags (workspace_id, name) VALUES ($1, $2)
			ON CONFLICT (workspace_id, lower(name)) DO UPDATE SET name = tags.name
			RETURNING id`, workspaceID, name).Scan(&id)
		if err != nil {
			return nil, wrap("ensure tags", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Folder holds links; folders can nest.
type Folder struct {
	ID          int64
	WorkspaceID int64
	Name        string
	ParentID    *int64
}

func scanFolder(row pgx.Row) (Folder, error) {
	var f Folder
	err := row.Scan(&f.ID, &f.WorkspaceID, &f.Name, &f.ParentID)
	return f, err
}

// ListFolders returns a workspace's folders by name.
func (s *Store) ListFolders(ctx context.Context, workspaceID int64) ([]Folder, error) {
	return collect(ctx, s, "list folders", scanFolder,
		`SELECT id, workspace_id, name, parent_id FROM folders WHERE workspace_id = $1 ORDER BY lower(name), id`, workspaceID)
}

// GetFolder returns one folder.
func (s *Store) GetFolder(ctx context.Context, id int64) (Folder, error) {
	f, err := scanFolder(s.pool.QueryRow(ctx, `SELECT id, workspace_id, name, parent_id FROM folders WHERE id = $1`, id))
	return f, wrap("get folder", notFound(fmt.Sprintf("folder %d", id), err))
}

// FolderAncestors returns the ids on the path from id up to the top level,
// id included, so a move that would put a folder inside itself is caught.
func (s *Store) FolderAncestors(ctx context.Context, id int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE up(id, parent_id, depth) AS (
			SELECT id, parent_id, 0 FROM folders WHERE id = $1
			UNION ALL
			SELECT f.id, f.parent_id, up.depth + 1 FROM folders f JOIN up ON f.id = up.parent_id WHERE up.depth < 100
		)
		SELECT id FROM up`, id)
	if err != nil {
		return nil, wrap("folder ancestors", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	return ids, wrap("folder ancestors", err)
}

// SaveFolder inserts f when f.ID is 0 and updates it otherwise.
func (s *Store) SaveFolder(ctx context.Context, f Folder) (Folder, error) {
	var err error
	if f.ID == 0 {
		err = s.pool.QueryRow(ctx, `INSERT INTO folders (workspace_id, name, parent_id) VALUES ($1, $2, $3) RETURNING id`,
			f.WorkspaceID, f.Name, f.ParentID).Scan(&f.ID)
	} else {
		var tag pgconn.CommandTag
		tag, err = s.pool.Exec(ctx, `UPDATE folders SET name = $2, parent_id = $3 WHERE id = $1`, f.ID, f.Name, f.ParentID)
		if err == nil && tag.RowsAffected() == 0 {
			err = fmt.Errorf("folder %d: %w", f.ID, domain.ErrNotFound)
		}
	}
	if err != nil {
		return Folder{}, wrap("save folder", err)
	}
	return s.GetFolder(ctx, f.ID)
}

// orgTables are the tables DeleteOrg and CountInWorkspace may touch; the
// name never comes from a request.
var orgTables = map[string]bool{"campaigns": true, "tags": true, "folders": true}

// DeleteOrg deletes a campaign, tag or folder. Links keep existing; the
// foreign keys clear their reference.
func (s *Store) DeleteOrg(ctx context.Context, table string, id int64) error {
	if !orgTables[table] {
		return fmt.Errorf("store: delete from unknown table %q", table)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = fmt.Errorf("%s %d: %w", table, id, domain.ErrNotFound)
	}
	return wrap("delete "+table, err)
}

// CountInWorkspace counts how many of ids are rows of table in workspaceID,
// to check that a link only refers to its own workspace's labels.
func (s *Store) CountInWorkspace(ctx context.Context, table string, workspaceID int64, ids []int64) (int, error) {
	if !orgTables[table] {
		return 0, fmt.Errorf("store: count in unknown table %q", table)
	}
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE workspace_id = $1 AND id = ANY($2)`, workspaceID, ids).Scan(&n)
	return n, wrap("count "+table, err)
}

// collect runs a query and scans every row with scan.
func collect[T any](ctx context.Context, s *Store, op string, scan func(pgx.Row) (T, error), sql string, args ...any) ([]T, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, v)
	}
	return out, wrap(op, rows.Err())
}
