// Package alerts tells link owners when something needs their attention:
// a traffic spike, a link close to its click cap, a link about to expire, a
// destination that stopped answering, or a quarantine.
//
// The SQL here works on the alerts table, which part 3 owns. To decide
// which alerts to raise it also reads links and click_events, never
// writing them.
package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/domain"
)

// Kinds, as in API.md.
const (
	KindSpike       = "spike"
	KindNearCap     = "near_cap"
	KindExpiring    = "expiring"
	KindDeadLink    = "dead_link"
	KindQuarantined = "quarantined"
)

// Thresholds. A spike is at least spikeMin human clicks in the last 15
// minutes and at least spikeFactor times the link's average 15 minutes over
// the previous day.
const (
	nearCapShare = 0.8
	expiringIn   = 24 * time.Hour
	spikeMin     = 20
	spikeFactor  = 5.0
	spikeLasts   = time.Hour
)

// Alert is one alert.
type Alert struct {
	ID        int64     `json:"id"`
	LinkID    int64     `json:"link_id"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Store keeps alerts in Postgres.
type Store struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
}

// Raise opens an alert unless one of the same kind is already open for the
// link; the partial unique index makes that a no-op, not an error.
func (s *Store) Raise(ctx context.Context, linkID, workspaceID int64, kind, message string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO alerts (link_id, workspace_id, kind, message) VALUES ($1, $2, $3, $4)
		ON CONFLICT (link_id, kind) WHERE resolved_at IS NULL DO NOTHING`,
		linkID, workspaceID, kind, message)
	return err
}

// Resolve closes the open alert of a kind for a link, if any.
func (s *Store) Resolve(ctx context.Context, linkID int64, kind string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE alerts SET resolved_at = now() WHERE link_id = $1 AND kind = $2 AND resolved_at IS NULL`, linkID, kind)
	return err
}

// List returns a workspace's alerts (every workspace's when all), newest
// first; open only unless includeResolved.
func (s *Store) List(ctx context.Context, workspaceID int64, all, includeResolved bool, limit int) ([]Alert, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, link_id, kind, message, created_at FROM alerts
		WHERE ($2 OR workspace_id = $1) AND ($3 OR resolved_at IS NULL)
		ORDER BY created_at DESC, id DESC LIMIT $4`, workspaceID, all, includeResolved, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.LinkID, &a.Kind, &a.Message, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountOpenAlerts implements domain.AlertCounter.
func (s *Store) CountOpenAlerts(ctx context.Context, workspaceID int64, all bool) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE resolved_at IS NULL AND ($2 OR workspace_id = $1)`, workspaceID, all).Scan(&n)
	return n, err
}

// OnTrust raises or clears the trust alerts when a scan changes a link's
// state; the trust scanner calls it after every check.
func (s *Store) OnTrust(ctx context.Context, l domain.Link, next domain.TrustState, detail string) {
	var err error
	switch next {
	case domain.TrustQuarantined:
		err = s.Raise(ctx, l.ID, l.WorkspaceID, KindQuarantined, fmt.Sprintf("/%s was quarantined: %s", l.Code, detail))
	case domain.TrustDegraded:
		err = s.Raise(ctx, l.ID, l.WorkspaceID, KindDeadLink, fmt.Sprintf("/%s: the destination stopped answering (%s)", l.Code, detail))
	default:
		err = s.Resolve(ctx, l.ID, KindDeadLink)
		if err == nil {
			err = s.Resolve(ctx, l.ID, KindQuarantined)
		}
	}
	if err != nil {
		s.Log.Warn("trust alert", "link_id", l.ID, "err", err)
	}
}

// Evaluate raises and clears the alerts that depend on time and traffic.
// It runs every minute; each query is one statement over all links.
func (s *Store) Evaluate(ctx context.Context) error {
	stmts := []struct {
		name string
		sql  string
		args []any
	}{
		{"near cap", `
			INSERT INTO alerts (link_id, workspace_id, kind, message)
			SELECT id, workspace_id, 'near_cap', '/' || code || ' has used ' || cap_used || ' of its ' || max_clicks || ' clicks'
			FROM links
			WHERE deleted_at IS NULL AND max_clicks IS NOT NULL AND cap_used >= ceil(max_clicks * $1::float8)
			ON CONFLICT (link_id, kind) WHERE resolved_at IS NULL DO NOTHING`, []any{nearCapShare}},
		{"expiring", `
			INSERT INTO alerts (link_id, workspace_id, kind, message)
			SELECT id, workspace_id, 'expiring', '/' || code || ' expires at ' || to_char(expires_at AT TIME ZONE 'Asia/Kolkata', 'DD Mon HH24:MI') || ' IST'
			FROM links
			WHERE deleted_at IS NULL AND status = 'active' AND expires_at > now() AND expires_at <= now() + $1::interval
			ON CONFLICT (link_id, kind) WHERE resolved_at IS NULL DO NOTHING`, []any{expiringIn.String()}},
		{"expired", `
			UPDATE alerts a SET resolved_at = now() FROM links l
			WHERE a.link_id = l.id AND a.kind = 'expiring' AND a.resolved_at IS NULL AND l.expires_at <= now()`, nil},
		{"spike", `
			INSERT INTO alerts (link_id, workspace_id, kind, message)
			SELECT c.link_id, l.workspace_id, 'spike', '/' || l.code || ': ' || c.recent || ' human clicks in the last 15 minutes'
			FROM (
				SELECT link_id,
					count(*) FILTER (WHERE ts > now() - interval '15 minutes') AS recent,
					count(*) FILTER (WHERE ts <= now() - interval '15 minutes') / 96.0 AS usual
				FROM click_events
				WHERE ts > now() - interval '24 hours 15 minutes' AND traffic_class = 'human'
				GROUP BY link_id
			) c JOIN links l ON l.id = c.link_id
			WHERE c.recent >= $1 AND c.recent >= $2::float8 * GREATEST(c.usual, 1)
			ON CONFLICT (link_id, kind) WHERE resolved_at IS NULL DO NOTHING`, []any{spikeMin, spikeFactor}},
		{"spike over", `UPDATE alerts SET resolved_at = now() WHERE kind = 'spike' AND resolved_at IS NULL AND created_at < now() - $1::interval`, []any{spikeLasts.String()}},
	}
	for _, st := range stmts {
		if _, err := s.Pool.Exec(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("alerts: %s: %w", st.name, err)
		}
	}
	return nil
}

// Run evaluates every interval until ctx ends.
func (s *Store) Run(interval time.Duration) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			if err := s.Evaluate(ctx); err != nil && ctx.Err() == nil {
				s.Log.Warn("alert evaluation failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
		}
	}
}
