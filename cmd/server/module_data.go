package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/app"
	"pathmaxing/internal/clicks"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/store"
)

// The data module (part 4) replaces the in-memory link store, blocklist,
// audit log and click log with Postgres once the db module has provided a
// pool. Without a database it does nothing and the defaults stay.
func init() {
	app.Register(app.Module{
		Name:     "data",
		Priority: 40,
		Provide: func(ctx context.Context, a *app.App) error {
			pool, ok := app.Lookup[*pgxpool.Pool](a)
			if !ok {
				return nil
			}
			st, err := store.New(ctx, pool, a.Log)
			if err != nil {
				return err
			}
			app.Provide[domain.LinkStore](a, st)
			app.Provide[domain.Blocklist](a, st)
			app.Provide[domain.AuditLog](a, st)
			app.Provide(a, st)

			q, err := newClickQueue(a, st)
			if err != nil {
				return err
			}
			app.Provide[domain.ClickSink](a, q)
			app.Provide(a, q)
			return nil
		},
		Start: func(_ context.Context, a *app.App) error {
			if st, ok := app.Lookup[*store.Store](a); ok {
				a.Go("blocklist-refresh", st.RefreshBlocklist(30*time.Second))
			}
			// The writer drains the queue after its context is cancelled, which
			// the app does only once HTTP has stopped, so no click is lost to a
			// deploy.
			if q, ok := app.Lookup[*clicks.Queue](a); ok {
				a.Go("click-writer", q.Run)
			}
			return nil
		},
	})
}

func newClickQueue(a *app.App, st *store.Store) (*clicks.Queue, error) {
	size, err := a.Config.Int("CLICK_QUEUE_SIZE", 10000)
	if err != nil {
		return nil, err
	}
	batch, err := a.Config.Int("CLICK_BATCH_SIZE", 500)
	if err != nil {
		return nil, err
	}
	interval, err := a.Config.Duration("CLICK_FLUSH_INTERVAL", time.Second)
	if err != nil {
		return nil, err
	}
	return clicks.New(st, clicks.Options{QueueSize: size, BatchSize: batch, FlushInterval: interval, Log: a.Log}), nil
}
