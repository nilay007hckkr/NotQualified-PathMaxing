package main

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/app"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/ratelimit"
	"pathmaxing/internal/threatintel"
	"pathmaxing/internal/trust"
)

// The trust module (part 3) checks destinations against threat feeds. Every
// feed key is optional: a feed without one is skipped, and the skip is
// logged at startup and reported with every check.
func init() {
	app.Register(app.Module{
		Name:     "trust",
		Priority: 30,
		Provide: func(_ context.Context, a *app.App) error {
			var feeds []threatintel.Feed
			var notConfigured []string
			if key := a.Config.String("SAFE_BROWSING_API_KEY", ""); key != "" {
				feeds = append(feeds, threatintel.SafeBrowsing{Key: key})
			} else {
				notConfigured = append(notConfigured, "safe_browsing: SAFE_BROWSING_API_KEY not set")
			}
			if key := a.Config.String("URLHAUS_AUTH_KEY", ""); key != "" {
				feeds = append(feeds, threatintel.URLhaus{Key: key})
			} else {
				notConfigured = append(notConfigured, "urlhaus: URLHAUS_AUTH_KEY not set")
			}
			for _, s := range notConfigured {
				a.Log.Info("threat feed skipped", "reason", s)
			}
			set := threatintel.NewSet(feeds, notConfigured, 5*time.Second)
			app.Provide(a, set)
			checker := trust.NewChecker("PathMaxingTrustBot/1.0 (+" + a.Config.BaseURL + ")")
			app.Provide(a, checker)
			app.Provide[domain.TrustChecker](a, &trust.Prechecker{Feeds: set, Probe: checker})
			return nil
		},
		// The scanner needs the final link store and cache, so it is built
		// once every module has provided them.
		Wire: func(_ context.Context, a *app.App) error {
			pool, ok := app.Lookup[*pgxpool.Pool](a)
			if !ok {
				a.Log.Info("trust scanner disabled: it keeps its history in the database")
				return nil
			}
			repo := trust.PGRepo{Pool: pool}
			scanner, err := newScanner(a, repo)
			if err != nil {
				return err
			}
			interval, err := a.Config.Duration("TRUST_SCAN_INTERVAL", 30*time.Second)
			if err != nil {
				return err
			}
			workers, err := a.Config.Int("TRUST_SCAN_CONCURRENCY", 4)
			if err != nil {
				return err
			}
			app.Provide(a, scanner)
			app.Provide(a, &trust.Scheduler{
				Scanner: scanner, Repo: repo, Interval: interval, Concurrency: workers, Batch: 20, Log: a.Log,
			})
			return nil
		},
		Routes: func(a *app.App, mux *http.ServeMux) error {
			sched, ok := app.Lookup[*trust.Scheduler](a)
			if !ok {
				return nil
			}
			guard, err := app.Get[domain.AuthGuard](a)
			if err != nil {
				return err
			}
			links, err := app.Get[domain.LinkStore](a)
			if err != nil {
				return err
			}
			(&trust.API{
				Guard: guard, Links: links, Repo: sched.Repo, Scheduler: sched,
				RescanLimiter: ratelimit.New(10, time.Hour), Log: a.Log,
			}).Register(mux)
			return nil
		},
		Start: func(_ context.Context, a *app.App) error {
			if sched, ok := app.Lookup[*trust.Scheduler](a); ok {
				a.Go("trust-scheduler", sched.Run)
			}
			return nil
		},
	})
}

func newScanner(a *app.App, repo trust.Repo) (*trust.Scanner, error) {
	links, err := app.Get[domain.LinkStore](a)
	if err != nil {
		return nil, err
	}
	blocklist, err := app.Get[domain.Blocklist](a)
	if err != nil {
		return nil, err
	}
	invalidator, err := app.Get[domain.CacheInvalidator](a)
	if err != nil {
		return nil, err
	}
	audit, err := app.Get[domain.AuditLog](a)
	if err != nil {
		return nil, err
	}
	checker, err := app.Get[*trust.Checker](a)
	if err != nil {
		return nil, err
	}
	feeds, err := app.Get[*threatintel.Set](a)
	if err != nil {
		return nil, err
	}
	return &trust.Scanner{
		Links: links, Repo: repo, Fetch: checker, Feeds: feeds, Blocklist: blocklist,
		Invalidator: invalidator, Audit: audit, Log: a.Log, Now: time.Now,
		NextCheck: trust.NextCheck,
	}, nil
}
