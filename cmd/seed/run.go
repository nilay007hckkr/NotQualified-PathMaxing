package main

import (
	"context"
	"fmt"
	"io"
	"time"
)

// demoCampaigns and demoLinks are what the seed creates. Destinations are
// public pages that answer 200, so the trust scanner's first checks come
// back ok (a 404 would be a real "failed" check and, twice, a dead link).
var demoCampaigns = []map[string]string{
	{"name": "Diwali Sale 2026", "utm_source": "instagram", "utm_medium": "social", "utm_campaign": "diwali-2026"},
	{"name": "October Newsletter", "utm_source": "newsletter", "utm_medium": "email", "utm_campaign": "oct-2026"},
	{"name": "Campus Fest", "utm_source": "poster", "utm_medium": "qr", "utm_campaign": "fest-2026"},
}

type demoLink struct {
	Alias, Title, Destination string
	Campaign                  int // index into demoCampaigns, -1 for none
	MaxClicks                 int
	MobileTarget              string
}

var demoLinks = []demoLink{
	{Alias: "diwali-sale", Title: "Diwali sale landing page", Destination: "https://example.com/?sale=diwali", Campaign: 0, MobileTarget: "https://example.org/?sale=diwali"},
	{Alias: "oct-newsletter", Title: "October newsletter", Destination: "https://www.wikipedia.org/", Campaign: 1},
	{Alias: "fest-poster", Title: "Campus fest poster QR", Destination: "https://go.dev/", Campaign: 2},
	{Alias: "fest-tickets", Title: "Fest tickets (first 25)", Destination: "https://example.net/?tickets=fest", Campaign: 2, MaxClicks: 25},
	{Alias: "team-repo", Title: "Our project page", Destination: "https://example.com/", Campaign: -1},
}

// run seeds the server at cfg.Base and returns a summary.
func run(ctx context.Context, cfg config, log io.Writer) (string, error) {
	c, err := newClient(cfg.Base)
	if err != nil {
		return "", err
	}
	if err := c.signIn(ctx, cfg.Email, cfg.Password, cfg.Name); err != nil {
		return "", fmt.Errorf("sign in as %s: %w", cfg.Email, err)
	}
	fmt.Fprintf(log, "signed in as %s\n", cfg.Email)

	var camps []campaign
	for _, in := range demoCampaigns {
		cp, err := c.ensureCampaign(ctx, in)
		if err != nil {
			return "", fmt.Errorf("campaign %q: %w", in["name"], err)
		}
		camps = append(camps, cp)
	}

	var links []link
	for _, d := range demoLinks {
		in := map[string]any{"destination": d.Destination, "alias": d.Alias, "title": d.Title}
		if d.Campaign >= 0 {
			in["campaign_id"] = camps[d.Campaign].ID
		}
		if d.MaxClicks > 0 {
			in["max_clicks"] = d.MaxClicks
		}
		if d.MobileTarget != "" {
			in["targets"] = []map[string]string{{"device": "mobile", "destination": d.MobileTarget}}
		}
		l, err := c.ensureLink(ctx, in)
		if err != nil {
			return "", err
		}
		links = append(links, l)
		fmt.Fprintf(log, "link %s\n", l.ShortURL)
	}

	start := time.Now()
	visits := plan(newRand(cfg.Seed), links, cfg.Visits)
	counts := send(ctx, visits, cfg.Workers)
	return fmt.Sprintf("sent %d visits in %s (profile and HTTP status):\n%s", len(visits), time.Since(start).Round(time.Millisecond), formatCounts(counts)), nil
}
