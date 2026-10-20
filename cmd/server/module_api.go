package main

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/admin"
	"pathmaxing/internal/analytics"
	"pathmaxing/internal/api"
	"pathmaxing/internal/app"
	"pathmaxing/internal/auth"
	"pathmaxing/internal/bio"
	"pathmaxing/internal/clicks"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/ratelimit"
	"pathmaxing/internal/store"
)

// The api module (part 4) replaces the deny-all guard with real sessions and
// mounts the management API under /api/ and the public bio pages under /bio/. Without a database there are no
// accounts, so it does nothing: the guard keeps refusing and /api/ stays
// unmounted.
func init() {
	app.Register(app.Module{
		Name:     "api",
		Priority: 50,
		Provide: func(ctx context.Context, a *app.App) error {
			pool, ok := app.Lookup[*pgxpool.Pool](a)
			if !ok {
				return nil
			}
			ttl, err := a.Config.Duration("SESSION_TTL", 168*time.Hour)
			if err != nil {
				return err
			}
			svc := auth.New(pool, auth.Options{
				TTL: ttl, Secure: a.Config.Production(), AllowedOrigins: a.Config.AllowedOrigins, Log: a.Log,
			})
			app.Provide[domain.AuthGuard](a, svc)
			app.Provide(a, svc)

			email, password := a.Config.String("ADMIN_EMAIL", ""), a.Config.String("ADMIN_PASSWORD", "")
			switch {
			case email == "" && password == "":
			case email == "" || len(password) < 8:
				a.Log.Warn("ADMIN_EMAIL and ADMIN_PASSWORD (at least 8 characters) must both be set; no admin seeded")
			default:
				if err := svc.EnsureAdmin(ctx, email, password); err != nil {
					return err
				}
				a.Log.Info("admin account ready", "email", email)
			}
			return nil
		},
		Routes: func(a *app.App, mux *http.ServeMux) error {
			svc, ok := app.Lookup[*auth.Service](a)
			if !ok {
				return nil
			}
			guard, err := app.Get[domain.AuthGuard](a)
			if err != nil {
				return err
			}
			st, err := app.Get[*store.Store](a)
			if err != nil {
				return err
			}
			trust, err := app.Get[domain.TrustChecker](a)
			if err != nil {
				return err
			}
			cache, err := app.Get[domain.CacheInvalidator](a)
			if err != nil {
				return err
			}
			an, err := app.Get[*analytics.Store](a)
			if err != nil {
				return err
			}
			live, _ := app.Lookup[*clicks.Broker](a)
			// Alerts arrive with the trust layer; until then alerts_open is null.
			alerts, _ := app.Lookup[domain.AlertCounter](a)
			srv := &api.Server{
				Auth: svc, Guard: guard, Store: st, Trust: trust, Cache: cache,
				Analytics: an, Live: live, Stopping: a.Stopping(),
				Admin:  &admin.Service{Store: st, Cache: cache, Log: a.Log},
				Alerts: alerts,
				// The limits from API.md.
				CreateLimit: ratelimit.New(30, time.Minute),
				ImportLimit: ratelimit.New(5, time.Hour),
				LoginLimit:  ratelimit.New(10, 15*time.Minute),
				ProxyHops:   a.Config.TrustedProxyHops,
				BaseURL:     a.Config.BaseURL, Log: a.Log,
			}
			srv.Register(mux)
			(&bio.Handler{Pages: st, BaseURL: a.Config.BaseURL, Log: a.Log}).Register(mux)
			return nil
		},
	})
}
