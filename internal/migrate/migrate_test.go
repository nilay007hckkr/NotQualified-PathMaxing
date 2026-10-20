package migrate_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgconn"

	"pathmaxing/internal/migrate"
	"pathmaxing/internal/migrate/pgtest"
	"pathmaxing/migrations"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestFiles(t *testing.T) {
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		want    []string
		wantErr string
	}{
		{
			name: "sorted by commit number, other files ignored",
			fsys: fstest.MapFS{
				"C09_click_events.sql": {}, "C03_core_schema.sql": {}, "C11_auth.sql": {},
				"embed.go": {}, "README.md": {},
			},
			want: []string{"C03_core_schema.sql", "C09_click_events.sql", "C11_auth.sql"},
		},
		{name: "missing commit prefix", fsys: fstest.MapFS{"core.sql": {}}, wantErr: "core.sql"},
		{name: "upper case name", fsys: fstest.MapFS{"C03_Core.sql": {}}, wantErr: "C03_Core.sql"},
		{name: "one-digit commit", fsys: fstest.MapFS{"C3_core.sql": {}}, wantErr: "C3_core.sql"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := migrate.Files(tt.fsys)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one naming %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("Files = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

// The embedded files must follow the naming rule, or the server can't start.
func TestEmbeddedMigrationsAreNamedCorrectly(t *testing.T) {
	files, err := migrate.Files(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 || files[0] != "C03_core_schema.sql" {
		t.Fatalf("embedded migrations = %v, want C03_core_schema.sql first", files)
	}
}

func TestRunAppliesOnceInOrder(t *testing.T) {
	pool := pgtest.Empty(t)
	ctx := context.Background()
	fsys := fstest.MapFS{
		"C01_a.sql": {Data: []byte("CREATE TABLE a (id int);")},
		"C02_b.sql": {Data: []byte("CREATE TABLE b (a_id int); INSERT INTO a VALUES (1);")},
	}
	applied, err := migrate.Run(ctx, pool, fsys, quietLog())
	if err != nil || !slices.Equal(applied, []string{"C01_a.sql", "C02_b.sql"}) {
		t.Fatalf("first run = %v, %v", applied, err)
	}
	applied, err = migrate.Run(ctx, pool, fsys, quietLog())
	if err != nil || len(applied) != 0 {
		t.Fatalf("second run = %v, %v; want nothing applied", applied, err)
	}

	// A new file applies on its own; the old ones don't run again (they
	// would fail: table a already exists).
	fsys["C03_c.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE c (id int);")}
	applied, err = migrate.Run(ctx, pool, fsys, quietLog())
	if err != nil || !slices.Equal(applied, []string{"C03_c.sql"}) {
		t.Fatalf("third run = %v, %v", applied, err)
	}
}

func TestFailedMigrationLeavesNothingBehind(t *testing.T) {
	pool := pgtest.Empty(t)
	ctx := context.Background()
	fsys := fstest.MapFS{
		"C01_ok.sql":  {Data: []byte("CREATE TABLE ok (id int);")},
		"C02_bad.sql": {Data: []byte("CREATE TABLE half (id int); SELECT no_such_column FROM ok;")},
	}
	applied, err := migrate.Run(ctx, pool, fsys, quietLog())
	if err == nil || !strings.Contains(err.Error(), "C02_bad.sql") {
		t.Fatalf("Run error = %v, want one naming C02_bad.sql", err)
	}
	if !slices.Equal(applied, []string{"C01_ok.sql"}) {
		t.Fatalf("applied = %v, want only C01_ok.sql", applied)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('half') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("the failed migration's first statement was not rolled back")
	}
	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE filename = 'C02_bad.sql'`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatal("a failed migration was recorded as applied")
	}
}

// Two instances starting together must not apply a file twice.
func TestConcurrentRunsTakeTurns(t *testing.T) {
	pool := pgtest.Empty(t)
	fsys := fstest.MapFS{"C01_a.sql": {Data: []byte("CREATE TABLE a (id int);")}}
	var wg sync.WaitGroup
	results := make([][]string, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = migrate.Run(context.Background(), pool, fsys, quietLog())
		}()
	}
	wg.Wait()
	total := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("run %d: %v", i, errs[i])
		}
		total += len(results[i])
	}
	if total != 1 {
		t.Fatalf("the file was applied %d times, want once", total)
	}
}

// TestCoreSchema checks the rules the rest of the system relies on.
func TestCoreSchema(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()

	insert := func(code string) (int64, error) {
		var id int64
		err := pool.QueryRow(ctx,
			`INSERT INTO links (workspace_id, owner_id, code, destination, max_clicks)
			 VALUES (1, 1, $1, 'https://shop.example', 2) RETURNING id`, code).Scan(&id)
		return id, err
	}
	id, err := insert("Sale")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("codes are unique ignoring case", func(t *testing.T) {
		_, err := insert("sale")
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "links_code_lower_key" {
			t.Fatalf("insert 'sale' after 'Sale' = %v, want a unique violation on links_code_lower_key", err)
		}
	})

	t.Run("deleted codes stay taken", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE links SET deleted_at = now() WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := insert("SALE"); err == nil {
			t.Fatal("a soft-deleted link's code was reused")
		}
	})

	t.Run("cap reservation stops at the cap", func(t *testing.T) {
		reserve := func() int64 {
			tag, err := pool.Exec(ctx, `UPDATE links SET cap_used = cap_used + 1 WHERE id = $1 AND cap_used < $2`, id, 2)
			if err != nil {
				t.Fatal(err)
			}
			return tag.RowsAffected()
		}
		got := []int64{reserve(), reserve(), reserve()}
		if !slices.Equal(got, []int64{1, 1, 0}) {
			t.Fatalf("rows affected = %v, want [1 1 0]", got)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		var status, trust string
		if err := pool.QueryRow(ctx, `SELECT status, trust_state FROM links WHERE id = $1`, id).Scan(&status, &trust); err != nil {
			t.Fatal(err)
		}
		if status != "active" || trust != "ok" {
			t.Fatalf("defaults: status %q trust %q", status, trust)
		}
	})

	checks := []struct {
		name string
		sql  string
	}{
		{"unknown status", `UPDATE links SET status = 'expired' WHERE id = $1`},
		{"unknown trust state", `UPDATE links SET trust_state = 'fine' WHERE id = $1`},
		{"cap of zero", `UPDATE links SET max_clicks = 0 WHERE id = $1`},
		{"negative cap use", `UPDATE links SET cap_used = -1 WHERE id = $1`},
		{"start after expiry", `UPDATE links SET starts_at = now(), expires_at = now() - interval '1 hour' WHERE id = $1`},
		{"unknown device", `INSERT INTO redirect_rules (link_id, device, destination) VALUES ($1, 'watch', 'https://a.example')`},
		{"mixed-case blocked domain", `INSERT INTO blocked_domains (domain) SELECT 'Evil.Example' WHERE $1::bigint > 0`},
	}
	for _, c := range checks {
		t.Run("rejects "+c.name, func(t *testing.T) {
			var pgErr *pgconn.PgError
			if _, err := pool.Exec(ctx, c.sql, id); !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatalf("got %v, want a check violation", err)
			}
		})
	}

	t.Run("device targets cascade with their link", func(t *testing.T) {
		other, err := insert("promo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO redirect_rules (link_id, device, destination) VALUES ($1, 'mobile', 'https://m.example')`, other); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM links WHERE id = $1`, other); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM redirect_rules WHERE link_id = $1`, other).Scan(&n); err != nil || n != 0 {
			t.Fatalf("redirect rules left after delete: %d, %v", n, err)
		}
	})

	t.Run("audit detail is JSON", func(t *testing.T) {
		var detail map[string]any
		err := pool.QueryRow(ctx,
			`INSERT INTO audit_log (action, entity_type, entity_id, detail) VALUES ('link.disabled', 'link', $1, $2) RETURNING detail`,
			id, map[string]any{"reason": "phishing"}).Scan(&detail)
		if err != nil || detail["reason"] != "phishing" {
			t.Fatalf("audit detail = %v, %v", detail, err)
		}
	})
}
