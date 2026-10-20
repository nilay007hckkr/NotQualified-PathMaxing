package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/app"
	"pathmaxing/internal/migrate"
	"pathmaxing/migrations"
)

// The db module (part 2) connects to Postgres when DATABASE_URL is set,
// applies the embedded migrations before any other module can touch the
// database, and provides the connection pool to the modules that need it.
// Without DATABASE_URL it does nothing, and the in-memory defaults stay.
func init() {
	// One server runs per process, so the module keeps its pool here to close
	// it in Stop, after the workers that write through it have drained.
	var pool *pgxpool.Pool

	app.Register(app.Module{
		Name:     "db",
		Priority: 20,
		Provide: func(ctx context.Context, a *app.App) error {
			if a.Config.DatabaseURL == "" {
				a.Log.Info("DATABASE_URL is not set; running on in-memory defaults")
				return nil
			}
			cfg, err := pgxpool.ParseConfig(a.Config.DatabaseURL)
			if err != nil {
				// pgx error messages can include the URL, password and all.
				return fmt.Errorf("DATABASE_URL is not a valid Postgres URL")
			}
			pool, err = pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				return fmt.Errorf("open database pool: %w", err)
			}
			// A serverless database (Neon) may need a few seconds to wake up.
			pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := pool.Ping(pingCtx); err != nil {
				return fmt.Errorf("connect to database: %w", err)
			}
			applied, err := migrate.Run(ctx, pool, migrations.FS, a.Log)
			if err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			a.Log.Info("database ready", "migrations_applied", len(applied))

			app.Provide(a, pool)
			a.AddCheck("database", pool.Ping)
			return nil
		},
		Stop: func(context.Context) error {
			if pool != nil {
				pool.Close()
				pool = nil
			}
			return nil
		},
	})
}
