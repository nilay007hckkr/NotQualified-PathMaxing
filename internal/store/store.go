// Package store is the Postgres implementation of the link store, the
// blocklist and the audit log. All SQL for the tables part 4 owns lives in
// this package, always with placeholders.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/shortcode"
	"pathmaxing/internal/urlcheck"
)

// Store talks to Postgres through a connection pool.
type Store struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	// blocked is swapped as a whole on every reload, so the redirect path
	// reads it without a lock.
	blocked atomic.Pointer[urlcheck.DomainSet]
	// newCode makes random codes; tests replace it to force collisions.
	newCode func() string
}

// New returns a store over pool and loads the blocklist into memory.
func New(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) (*Store, error) {
	s := &Store{pool: pool, log: log, newCode: shortcode.Generate}
	s.blocked.Store(urlcheck.NewDomainSet(nil))
	if err := s.ReloadBlocklist(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Pool returns the underlying pool, for the other part 4 packages.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// RefreshBlocklist reloads the blocklist every interval until ctx ends. The
// admin endpoints reload it right after a change; this loop picks up
// changes made elsewhere (another instance, or by hand in SQL).
func (s *Store) RefreshBlocklist(interval time.Duration) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				if err := s.ReloadBlocklist(ctx); err != nil && ctx.Err() == nil {
					s.log.Warn("blocklist reload failed; keeping the previous copy", "err", err)
				}
			}
		}
	}
}

// uniqueViolation reports whether err is a unique violation on constraint.
func uniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("store: %s: %w", op, err)
}
