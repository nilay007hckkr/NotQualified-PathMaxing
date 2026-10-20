package main

import (
	"context"
	"time"

	"pathmaxing/internal/app"
	"pathmaxing/internal/domain"
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
	})
}
