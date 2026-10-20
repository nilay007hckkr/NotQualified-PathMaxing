package main

import (
	"context"
	"net/http"
	"time"

	"pathmaxing/internal/app"
	"pathmaxing/internal/classify"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/linkcache"
	"pathmaxing/internal/memstore"
	"pathmaxing/internal/ratelimit"
	"pathmaxing/internal/redirect"
	"pathmaxing/internal/rules"
	"pathmaxing/internal/secret"
)

// The core module (part 3) provides a working default for every shared
// contract in internal/domain, at the lowest priority, so the server starts
// with nothing else configured. Modules with a higher priority replace these
// defaults: the Postgres store, the click queue, real sessions, and so on.
// Once every module has provided its services, core serves the short links.
func init() {
	app.Register(app.Module{
		Name:     "core",
		Priority: 10,
		Provide: func(_ context.Context, a *app.App) error {
			store := memstore.New()
			app.Provide[domain.LinkStore](a, store)
			app.Provide[domain.Blocklist](a, store)
			app.Provide[domain.AuditLog](a, store)
			app.Provide[domain.ClickSink](a, memstore.NewClickLog(1000))
			app.Provide[domain.TrafficClassifier](a, classify.NewHonest())
			app.Provide[domain.TrustChecker](a, domain.SkipTrustChecker{})
			app.Provide[domain.CacheInvalidator](a, domain.NoopInvalidator{})
			app.Provide[domain.AuthGuard](a, domain.DenyAllGuard{})
			app.Provide[domain.GeoLookup](a, domain.NoGeo{})
			return nil
		},
		Wire: func(_ context.Context, a *app.App) error {
			// Every module has provided its link store by now (in memory, or
			// Postgres), so the cache goes in front of the final one.
			store, err := app.Get[domain.LinkStore](a)
			if err != nil {
				return err
			}
			ttl, err := a.Config.Duration("LINK_CACHE_TTL", time.Minute)
			if err != nil {
				return err
			}
			size, err := a.Config.Int("LINK_CACHE_SIZE", 50000)
			if err != nil {
				return err
			}
			cache := linkcache.New(store, ttl, size)
			app.Provide[domain.LinkResolver](a, cache)
			app.Provide[domain.CacheInvalidator](a, cache)
			return nil
		},
		Routes: func(a *app.App, mux *http.ServeMux) error {
			h, err := newRedirectHandler(a)
			if err != nil {
				return err
			}
			h.Register(mux)
			return nil
		},
	})
}

func newRedirectHandler(a *app.App) (*redirect.Handler, error) {
	resolver, err := app.Get[domain.LinkResolver](a)
	if err != nil {
		return nil, err
	}
	store, err := app.Get[domain.LinkStore](a)
	if err != nil {
		return nil, err
	}
	blocklist, err := app.Get[domain.Blocklist](a)
	if err != nil {
		return nil, err
	}
	classifier, err := app.Get[domain.TrafficClassifier](a)
	if err != nil {
		return nil, err
	}
	clicks, err := app.Get[domain.ClickSink](a)
	if err != nil {
		return nil, err
	}
	geo, err := app.Get[domain.GeoLookup](a)
	if err != nil {
		return nil, err
	}
	invalidator, err := app.Get[domain.CacheInvalidator](a)
	if err != nil {
		return nil, err
	}
	keys := secret.NewKeys(a.Config.SecretKey)
	return &redirect.Handler{
		Links:           resolver,
		Engine:          &rules.Engine{Blocklist: blocklist, Caps: store},
		Classifier:      classifier,
		Clicks:          clicks,
		Geo:             geo,
		Unlocker:        keys.NewUnlocker(a.Config.Production()),
		Visitors:        keys.NewVisitors(),
		PasswordLimiter: ratelimit.New(10, 15*time.Minute),
		ProxyHops:       a.Config.TrustedProxyHops,
		Invalidator:     invalidator,
		Log:             a.Log,
		Now:             time.Now,
	}, nil
}
