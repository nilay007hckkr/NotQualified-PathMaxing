package trust

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/memstore"
)

// PGRepo stores trust state in Postgres: the trust columns on links and the
// destination_checks table that part 3 owns. It only reads the rest of links.
type PGRepo struct{ Pool *pgxpool.Pool }

// TrustState implements Repo.
func (r PGRepo) TrustState(ctx context.Context, linkID int64) (State, error) {
	var s State
	var trust string
	err := r.Pool.QueryRow(ctx, `
		SELECT trust_state, trust_failures, trust_baseline_host, trust_baseline_destination
		FROM links WHERE id = $1`, linkID).Scan(&trust, &s.Failures, &s.BaselineHost, &s.BaselineDestination)
	if errors.Is(err, pgx.ErrNoRows) {
		return State{}, fmt.Errorf("link %d: %w", linkID, domain.ErrNotFound)
	}
	s.Trust = domain.TrustState(trust)
	return s, err
}

// SaveCheck implements Repo.
func (r PGRepo) SaveCheck(ctx context.Context, linkID int64, c CheckRecord, s State, nextCheck time.Time) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // a no-op once committed
	var status *int
	if c.HTTPStatus > 0 {
		status = &c.HTTPStatus
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO destination_checks
			(link_id, checked_at, destination, http_status, final_url, final_domain, hops, error, verdict, sources, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		linkID, c.CheckedAt, c.Destination, status, c.FinalURL, c.FinalDomain, c.Hops, c.Error, c.Verdict, c.Sources, c.Detail); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE links SET trust_state = $2, trust_failures = $3, trust_baseline_host = $4,
			trust_baseline_destination = $5, last_checked_at = $6, next_check_at = $7
		WHERE id = $1`,
		linkID, string(s.Trust), s.Failures, s.BaselineHost, s.BaselineDestination, c.CheckedAt, nextCheck); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// History implements Repo, newest first.
func (r PGRepo) History(ctx context.Context, linkID int64, limit int) ([]CheckRecord, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT checked_at, destination, COALESCE(http_status, 0), final_url, final_domain, hops, error, verdict, sources, detail
		FROM destination_checks WHERE link_id = $1 ORDER BY checked_at DESC, id DESC LIMIT $2`, linkID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckRecord
	for rows.Next() {
		var c CheckRecord
		if err := rows.Scan(&c.CheckedAt, &c.Destination, &c.HTTPStatus, &c.FinalURL, &c.FinalDomain, &c.Hops, &c.Error, &c.Verdict, &c.Sources, &c.Detail); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Due implements Repo. Quarantined and deleted links are not re-scanned.
func (r PGRepo) Due(ctx context.Context, now time.Time, limit int) ([]int64, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT id FROM links
		WHERE deleted_at IS NULL AND trust_state <> 'quarantined' AND (next_check_at IS NULL OR next_check_at <= $1)
		ORDER BY next_check_at NULLS FIRST, id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ScheduleNow implements Repo.
func (r PGRepo) ScheduleNow(ctx context.Context, linkID int64) error {
	_, err := r.Pool.Exec(ctx, `UPDATE links SET next_check_at = now() WHERE id = $1`, linkID)
	return err
}

// NextCheckAt implements Repo.
func (r PGRepo) NextCheckAt(ctx context.Context, linkID int64) (*time.Time, error) {
	var next *time.Time
	err := r.Pool.QueryRow(ctx, `SELECT next_check_at FROM links WHERE id = $1`, linkID).Scan(&next)
	return next, err
}

// MemRepo keeps trust state for links in a memstore, for tests and for
// running without a database.
type MemRepo struct {
	Store *memstore.Store

	mu      sync.Mutex
	states  map[int64]State
	history map[int64][]CheckRecord
	next    map[int64]time.Time
}

// NewMemRepo returns a repo over store.
func NewMemRepo(store *memstore.Store) *MemRepo {
	return &MemRepo{Store: store, states: map[int64]State{}, history: map[int64][]CheckRecord{}, next: map[int64]time.Time{}}
}

// TrustState implements Repo.
func (m *MemRepo) TrustState(ctx context.Context, linkID int64) (State, error) {
	l, err := m.Store.GetLink(ctx, linkID)
	if err != nil {
		return State{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.states[linkID]
	s.Trust = l.TrustState
	return s, nil
}

// SaveCheck implements Repo; it writes the trust state back to the link.
func (m *MemRepo) SaveCheck(ctx context.Context, linkID int64, c CheckRecord, s State, nextCheck time.Time) error {
	l, err := m.Store.GetLink(ctx, linkID)
	if err != nil {
		return err
	}
	l.TrustState = s.Trust
	l.LastCheckedAt = &c.CheckedAt
	if _, err := m.Store.Put(l); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[linkID] = s
	m.history[linkID] = append([]CheckRecord{c}, m.history[linkID]...)
	m.next[linkID] = nextCheck
	return nil
}

// History implements Repo.
func (m *MemRepo) History(_ context.Context, linkID int64, limit int) ([]CheckRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.history[linkID]
	return slices.Clone(h[:min(limit, len(h))]), nil
}

// Due implements Repo for the links it has seen.
func (m *MemRepo) Due(_ context.Context, now time.Time, limit int) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []int64
	for id, t := range m.next {
		if !t.After(now) && m.states[id].Trust != domain.TrustQuarantined {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids[:min(limit, len(ids))], nil
}

// ScheduleNow implements Repo.
func (m *MemRepo) ScheduleNow(_ context.Context, linkID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next[linkID] = time.Time{}
	return nil
}

// NextCheckAt implements Repo.
func (m *MemRepo) NextCheckAt(_ context.Context, linkID int64) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.next[linkID]
	if !ok || t.IsZero() {
		return nil, nil
	}
	return &t, nil
}
