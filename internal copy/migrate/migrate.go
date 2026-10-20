// Package migrate applies the embedded SQL migrations at startup.
//
// Each file runs in its own transaction and is recorded in
// schema_migrations in the same transaction, so a restart applies only new
// files and a failing file leaves nothing behind. A Postgres advisory lock
// makes two instances that start at the same time take turns instead of
// applying the same file twice.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fileName is the naming rule: the commit that added the file, then a name.
var fileName = regexp.MustCompile(`^C[0-9]{2}_[a-z0-9_]+\.sql$`)

// Files returns the migration files in fsys in the order they apply. It
// rejects any .sql file that doesn't follow the naming rule, because a
// misnamed file would silently apply in the wrong order.
func Files(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if !fileName.MatchString(e.Name()) {
			return nil, fmt.Errorf("migration %q: name must look like C07_click_events.sql", e.Name())
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names, nil
}

// Run applies every file in fsys that schema_migrations doesn't list yet,
// in name order, and returns the names it applied. It works in whatever
// schema the pool's search_path points to, which is how tests get a schema
// of their own.
func Run(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, log *slog.Logger) ([]string, error) {
	files, err := Files(fsys)
	if err != nil {
		return nil, err
	}

	// Advisory locks belong to a session, so lock and migrate on one connection.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	// One lock per schema: parallel test schemas don't wait for each other.
	const lockKey = `hashtext('pathmaxing.migrate.' || current_schema())`
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+lockKey+`)`); err != nil {
		return nil, fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		// Unlock even if ctx was cancelled mid-run.
		if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(`+lockKey+`)`); err != nil {
			log.Warn("could not release the migration lock", "err", err)
		}
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		filename   text PRIMARY KEY,
		checksum   text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	done, err := appliedChecksums(ctx, conn.Conn())
	if err != nil {
		return nil, err
	}

	var applied []string
	for _, name := range files {
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return applied, fmt.Errorf("read %s: %w", name, err)
		}
		sum := checksum(body)
		if prev, ok := done[name]; ok {
			if prev != sum {
				// Migrations are never edited after their commit; say so loudly,
				// but keep serving rather than take the demo down.
				log.Warn("an applied migration has changed since it ran; add a new migration instead", "file", name)
			}
			continue
		}
		if err := applyOne(ctx, conn.Conn(), name, string(body), sum); err != nil {
			return applied, err
		}
		log.Info("applied migration", "file", name)
		applied = append(applied, name)
	}
	return applied, nil
}

func appliedChecksums(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, `SELECT filename, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	done := make(map[string]string)
	for rows.Next() {
		var name, sum string
		if err := rows.Scan(&name, &sum); err != nil {
			return nil, fmt.Errorf("read schema_migrations: %w", err)
		}
		done[name] = sum
	}
	return done, rows.Err()
}

func applyOne(ctx context.Context, conn *pgx.Conn, name, body, sum string) (err error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s: begin: %w", name, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	// No arguments, so pgx uses the simple protocol, which allows a file to
	// hold several statements.
	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)`, name, sum); err != nil {
		return fmt.Errorf("%s: record: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s: commit: %w", name, err)
	}
	return nil
}

func checksum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
