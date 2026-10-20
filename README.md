# PathMaxing

**The accountable short link.** PathMaxing is a URL shortener and link-analytics platform that keeps re-checking destinations after a link is published, and labels every click honestly as human, email scanner (probable), link preview or crawler, with the reason.

Built by team **Not Qualified** (Nilay Joshi, Mayank Joshi, Karan Pandey, Mansi Pandey, Manas Singh Bisht) for the Tech Geeks hackathon, answering the "LinkPulse: URL Shortener & Analytics Platform" problem statement.

## What it does

- **Create links in two steps.** Validate first: URL rules, alias free and not reserved, blocklist, threat-feed check and a reachability probe. Then set options: expiry, start time, click cap, password, device targets, campaign, tags and folder. Then publish, with a QR code (PNG/SVG).
- **Fast, honest redirects.** `GET /{code}` goes through the cache, then the rule engine (blocked → inactive → schedule → password → click cap), then a 302 that is never cached. The click is queued, not written inline.
- **Honest clicks.** HEAD requests, link previews, crawlers (search engines verified by DNS), scripts and scanner bursts are labelled, and only humans use up click caps or one-time links.
- **Destination trust.** Every destination is re-checked after publishing (1 h, 24 h, then daily or weekly) through an SSRF-safe checker, against Safe Browsing, URLhaus and the admin blocklist. A threat hit quarantines the link: visitors get a warning page, the owner gets an alert and an audit entry is written.
- **Analytics.** Per link, per campaign and overview, by period in Asia/Kolkata (any time zone works): human vs bot, refused visits, unique visitor-days, devices, browsers, OS, referrers, countries (with GeoLite2), link vs QR, and outcomes. Totals come from 15-minute rollups.
- **Organisation:** workspaces, campaigns (UTM), tags, folders, CSV bulk import with a per-row error preview, and link-in-bio pages.
- **Admin:** takedowns, a domain blocklist that also covers subdomains, the abuse queue, the audit log, users and platform analytics. Creation, imports, logins, password tries and re-scans are rate limited.
- **Transparency:** `/p/{code}` shows anyone the destination host, trust state and last check before they click.

## Architecture

```
Visitor ─GET /{code}─▶ [cache] → [rule engine] → 302 ─▶ destination
                           │ (async) click event
                           ▼
                     [click queue] → [batch writer + 15-min rollups] ─▶ PostgreSQL
Owner/Admin ─/api/*─▶ [management API: sessions, roles, rate limits] ─▶ PostgreSQL
[trust scheduler] ─▶ SSRF-safe checker + Safe Browsing + URLhaus + blocklist ─▶ trust state, quarantine
[React app] served at /app/ by the Go server, or by Vercel with rewrites to it
```

It is one Go binary (`cmd/server`): standard-library `net/http`, PostgreSQL through pgx. Each of the five parts plugs in through a module registry (`internal/app`) and shared contracts (`internal/domain`). The frontend is in `web/` (React, Vite, TypeScript, TanStack Query, Recharts). Its visual design is based on the free TemplateMo 627 "HyperLuv" template.

## Run it locally

You need Go 1.24+, Node 20+ and Postgres 16 (`docker compose up -d db` gives you one).

```bash
cp .env.example .env    # every variable is explained there; set DATABASE_URL
DATABASE_URL=postgres://pathmaxing:pathmaxing@localhost:5432/pathmaxing?sslmode=disable \
ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD=change-me-please \
  go run ./cmd/server    # migrations apply on start; http://localhost:8080/healthz

cd web && npm ci && npm run dev    # http://localhost:5173/app/ (proxies /api to :8080)
# or build it and let Go serve it: npm run build, then WEB_DIST_DIR=web/dist go run ./cmd/server

go run ./cmd/seed -base http://localhost:8080 -password <8+ chars>   # demo traffic, real requests
```

## Tests

```bash
go build ./... && go vet ./... && go test ./...            # every commit
TEST_DATABASE_URL=<postgres url> go test ./...            # adds the database tests
go test -race ./...                                        # CI (needs a C compiler)
cd web && npm test
k6 run -e PASSWORD=... loadtest/redirect.js                # see loadtest/README.md
k6 run -e PASSWORD=... loadtest/cap_race.js
```

Measured on the build laptop, over localhost with local Postgres: redirect p95 1.23 ms at 50 virtual users, and 1,000 simultaneous visitors on a link capped at 100 gave exactly 100 redirects, three runs in a row. Numbers on the deployed site will differ, because they include network time.

## Deploy

- **Backend:** Render, using `deploy/render.yaml`. One Docker service, named `pathmaxing`, runs the API, redirects and workers, with Neon Postgres as the database.
- **Frontend:** Vercel, using `web/vercel.json`. It rewrites `/api`, short links, `/p` and `/bio` to the Render service, so the browser sees one origin.
- **Step by step:** see `docs/parts/part5-web.md`.

## Team docs

- [`SUMMARY.md`](SUMMARY.md): problem, solution, decisions, references
- [`API.md`](API.md): the HTTP contract
- [`PARTS.md`](PARTS.md) and [`COMMIT_PLAN.md`](COMMIT_PLAN.md): who owns what, and the commit order
- `docs/parts/part1-foundation.md` … `part5-web.md`: one owner guide per part

## Credits

- Country data: GeoLite2 by MaxMind, https://www.maxmind.com.
- Crawler list: [monperrus/crawler-user-agents](https://github.com/monperrus/crawler-user-agents).
- Threat feeds: Google Safe Browsing (non-commercial use) and abuse.ch URLhaus.
- UI design reference: TemplateMo 627 HyperLuv.
