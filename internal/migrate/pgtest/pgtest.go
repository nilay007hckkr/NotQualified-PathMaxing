// Package pgtest gives a database test an empty schema of its own with
// every migration applied. Tests from different packages run in parallel
// against one database (TEST_DATABASE_URL) without seeing each other's rows,
// and each schema is dropped when its test ends.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/migrate"
	"pathmaxing/migrations"
)

// URL returns TEST_DATABASE_URL, skipping the test when it is unset.
func URL(t testing.TB) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping a database test")
	}
	return url
}

// New returns a pool on a fresh schema with all migrations applied.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := Empty(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := migrate.Run(ctx, pool, migrations.FS, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("pgtest: migrate: %v", err)
	}
	return pool
}

// Empty returns a pool on a fresh schema with nothing applied, for tests of
// the migration runner itself.
func Empty(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(b[:])
	exec(t, url, "CREATE SCHEMA "+schema)

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("pgtest: parse TEST_DATABASE_URL: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		exec(t, url, "DROP SCHEMA "+schema+" CASCADE")
	})
	return pool
}

func exec(t testing.TB, url, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("pgtest: %s: %v", sql, err)
	}
}
