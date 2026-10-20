# PathMaxing

**The accountable short link.** PathMaxing is a URL shortener and link-analytics platform that keeps checking where a link goes after it is published, and tells you honestly who clicked it: a human, an email scanner, a chat-app link preview, or a crawler.

Built by team **Not Qualified** for the Tech Geeks hackathon, for the problem statement "LinkPulse: URL Shortener & Analytics Platform".

---

## The problem

Most short links and QR codes are checked once, when they are created, and then trusted forever.

- **Destinations decay.** 38% of webpages that existed in 2013 were no longer accessible a decade later (Pew Research Center, 2024).
- **QR codes are attacked at scale.** Mimecast detected 716,306 unique malicious QR codes in Q3 2025 alone (APWG Phishing Activity Trends Report, Q3 2025).
- **Click counts are inflated.** Email security products open every link in a message before a person does, and chat apps fetch a link the moment it is pasted. A normal shortener counts all of these as clicks, and a one-time link can be used up before anyone sees it.

## What makes PathMaxing different

1. **Destination trust.** Every destination is re-checked on a schedule after publishing (1 hour, 24 hours, then daily, or weekly when the link is idle). The checker follows the full redirect chain and looks it up in Google Safe Browsing, abuse.ch URLhaus and the admin blocklist. A threat hit quarantines the link at once: the cache entry is cleared, visitors see a warning page instead of the destination, the owner gets an alert and an audit entry is written.
2. **Honest clicks.** Every click is labelled `human`, `email_scanner` (probable), `link_preview` or `crawler`, together with the reason for the label. Only human clicks use up a click cap, so a WhatsApp preview cannot burn a one-time link.
3. **Transparency.** Anyone can open `/p/{code}` to see the destination host, its trust state and when it was last checked, before they click.

## Features

- **Two-step link creation.** Validate first (URL rules, alias available and not reserved, blocklist, threat-feed check, reachability probe), then set options: custom alias, start time, expiry, click cap, password, device targeting, campaign, tags and folder.
- **Fast redirects.** `GET /{code}` is served from an in-memory cache through the rule engine and answered with a 302. The click is queued and written in the background, so a redirect never waits on the database.
- **Analytics.** Per link, per campaign and overall, by period in Asia/Kolkata (any time zone works): human vs bot, refused visits, unique visitors per day, devices, browsers, operating systems, referrers, countries, link vs QR scans, and outcomes.
- **QR codes** as PNG or SVG. Scans are tracked separately from link clicks.
- **CSV bulk import** with a preview that gives the error for each bad row. Only the valid rows are imported.
- **Link-in-bio pages.**
- **Alerts** for traffic spikes, links near their cap, links about to expire, dead destinations and quarantined links.
- **Accounts and roles.** Users see only their own links. Admins can disable links, manage the domain blocklist (which also covers subdomains), work the abuse queue, read the audit log and see platform analytics.
- **Rate limits** on link creation, imports, login, password attempts and re-scans.

## How a redirect is decided

Each visit to `/{code}` goes through the rules in this order. The first rule that refuses wins.

| Order | Rule | Result when it refuses |
|---|---|---|
| 1 | Blocked: disabled by an admin, quarantined, or destination on the blocklist | 451 |
| 2 | Inactive: paused or archived | 410 |
| 3 | Schedule: not started yet / expired | 404 / 410 |
| 4 | Password: no valid unlock cookie | password form (403 on a wrong password) |
| 5 | Click cap: no clicks left | 410 |

If every rule passes, device targeting picks the destination and the server answers **302** with `Cache-Control: private, no-store`.

- **Why 302 and never 301:** browsers cache a 301 permanently. Repeat clicks would never reach the server, and a link that was taken down would keep working.
- **Why the click cap comes last:** it is the only rule that writes to the database. Putting it last means a refused visit never uses up a click. The cap is enforced with a single conditional `UPDATE`, so concurrent visitors cannot overshoot it.
- **Every visit is recorded**, allowed or refused, with its outcome, traffic class and reason. A code that does not exist gets a 404 and no event, because there is no link to report it under.

## Architecture

PathMaxing is one Go binary that runs the redirect service, the management API and the background workers.

```
Visitor --GET /{code}--> [cache] -> [rule engine] -> 302 --> destination
                              |
                              | click event (async)
                              v
                      [click queue] -> [batch writer + 15-minute rollups] --> PostgreSQL

Owner / Admin --/api/*--> [management API: sessions, roles, rate limits] --> PostgreSQL

[trust scheduler] --> SSRF-safe checker + Safe Browsing + URLhaus + blocklist
                  --> trust state, quarantine, cache invalidation, alerts

[React web app] --> served under /app/ (by the Go server, or by Vercel with rewrites to it)
```

- **Cache with safe invalidation.** The cache is cleared after every database commit. A generation counter stops a lookup that read a row before a change from storing that stale row afterwards.
- **Batched click writes.** Events are written in batches. Each event has a unique `event_id`, so a retried insert cannot count a click twice.
- **15-minute rollups.** Click counts are rolled up into 15-minute UTC buckets in the same transaction as the batch insert. India is UTC+05:30, so hourly UTC buckets could not be regrouped into IST days exactly. 15-minute buckets can, for every real time zone.
- **SSRF protection.** The trust checker refuses private, loopback and link-local addresses, after DNS resolution and again on every redirect hop. It follows at most 5 redirects, with timeouts and a response-size limit.
- **Privacy.** Raw IP addresses are never stored. Each visitor is stored as a daily-rotating HMAC hash plus the country.

## Tech stack

| Layer | Technology |
|---|---|
| Backend | Go 1.24+, standard library `net/http` |
| Database | PostgreSQL 16 through `pgx/v5`, plain SQL migrations embedded in the binary |
| Frontend | React, Vite, TypeScript, TanStack Query, Recharts |
| Threat intelligence | Google Safe Browsing API v4, abuse.ch URLhaus |
| Geolocation | MaxMind GeoLite2 Country |
| Bot detection | `monperrus/crawler-user-agents`, plus our own rules |
| Other libraries | `golang.org/x/crypto` (bcrypt), `skip2/go-qrcode` |
| Hosting | Render (Go server), Vercel (web app), Neon (PostgreSQL) |
| Testing and CI | Go tests, Vitest, k6 load tests, GitHub Actions |

## Project layout

```
cmd/server        the single binary: redirects, API, background workers
cmd/seed          demo data, sent as real HTTP requests through the API and redirects
internal/         one small package per concern:
  app             module registry, config, health check, static file serving
  domain          shared types and the interfaces the parts plug into
  redirect, rules the redirect handler and the rule engine
  linkcache       the sharded link cache
  classify        honest-clicks classifier
  trust           trust states, scanner, scheduler, transparency page
  threatintel     Safe Browsing and URLhaus clients
  store           PostgreSQL store for links and click events
  clicks          click queue and batch writer
  analytics       analytics queries and the analytics API
  api, auth       management API, accounts, sessions and roles
  admin, alerts   admin governance and owner alerts
  bulk, bio, qr   CSV import, link-in-bio pages, QR codes
migrations/       SQL migrations, applied automatically on start
web/              React web app
loadtest/         k6 scripts for redirect latency and the click-cap race
deploy/           Render blueprint and the full Dockerfile
```

## Run it locally

You need Go 1.24+, Node 20+ and PostgreSQL 16. `docker compose up -d db` starts a local database.

```bash
# 1. Database
docker compose up -d db

# 2. Server (migrations apply on start; health check at http://localhost:8080/healthz)
DATABASE_URL="postgres://pathmaxing:pathmaxing@localhost:5432/pathmaxing?sslmode=disable" \
ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD=change-me-please \
  go run ./cmd/server

# 3. Web app, in a second terminal (http://localhost:5173/app/, proxies /api to :8080)
cd web
npm ci
npm run dev

# 4. Optional: demo traffic, sent as real requests
go run ./cmd/seed -base http://localhost:8080 -password <8+ characters>
```

To let the Go server serve the web app itself, run `npm run build` in `web/`, then start the server with `WEB_DIST_DIR=web/dist`.

## Configuration

All configuration comes from environment variables. Every variable is listed and explained in [`.env.example`](.env.example). The most important ones:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string. Without it the server runs on in-memory defaults and the API is disabled. |
| `BASE_URL` | Public origin. Short links and QR codes are built from it. |
| `SECRET_KEY` | At least 32 random characters. Signs password-unlock cookies and keys the daily visitor hash. Required in production. |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | Admin account created or promoted at startup. |
| `TRUSTED_PROXY_HOPS` | Number of reverse proxies in front of the server (0 locally, 2 behind Vercel and Render). |
| `SAFE_BROWSING_API_KEY` | Optional. Without it the Safe Browsing check is skipped and the skip is logged. |
| `URLHAUS_AUTH_KEY` | Optional. Without it the URLhaus check is skipped and the skip is logged. |
| `GEOLITE2_DB_PATH` | Optional path to `GeoLite2-Country.mmdb`. Without it no country is stored. |

Never commit a `.env` file. Only `.env.example` belongs in the repository.

## Tests

```bash
go build ./... && go vet ./... && go test ./...      # build and unit tests
TEST_DATABASE_URL=<postgres url> go test ./...      # adds the database tests
go test -race ./...                                  # race detector (needs a C compiler; CI runs it)
cd web && npm test                                   # web app tests
k6 run -e PASSWORD=... loadtest/redirect.js          # redirect latency
k6 run -e PASSWORD=... loadtest/cap_race.js          # click-cap race (start the server with TRUSTED_PROXY_HOPS=1)
```

Tests never call real third-party services. Safe Browsing, URLhaus and GeoLite2 sit behind interfaces with in-memory fakes.

### Measured results

These were measured on one laptop over localhost with a local PostgreSQL and a warm cache. Numbers on the deployed site will be different, because they include network time.

- **Redirect latency:** p95 of 1.23 ms with 50 virtual users (`loadtest/redirect.js`).
- **Click-cap race:** 1,000 simultaneous visitors on a link capped at 100 got exactly 100 redirects, three runs in a row (`loadtest/cap_race.js`).
- **Classifier:** on a labelled list of 51 human and 54 bot user agents, bot precision and recall were both 1.000. The team wrote that list, so this measures rule coverage, not accuracy on real traffic.

## Deployment

- **Database:** Neon PostgreSQL. Use the connection string with `sslmode=require`.
- **Backend:** Render, from the blueprint in [`deploy/render.yaml`](deploy/render.yaml). Set the blueprint path to `deploy/render.yaml`. The service is one Docker web service named `pathmaxing`. Paste `DATABASE_URL`, `BASE_URL`, `ADMIN_EMAIL` and `ADMIN_PASSWORD` in the Render dashboard. `SECRET_KEY` is generated by Render.
- **Frontend:** Vercel, with `web/` as the root directory. [`web/vercel.json`](web/vercel.json) rewrites `/api`, short links, `/p` and `/bio` to the Render service, so the browser sees a single origin.

## API

The main routes:

| Route | Purpose |
|---|---|
| `GET /{code}` | Follow a short link (302, or 451 / 410 / 404 / password form) |
| `GET /p/{code}` | Public transparency page: destination host, trust state, last check |
| `GET /healthz` | Health check |
| `/api/*` | Management API: auth, links, campaigns, tags, folders, bulk import, analytics, trust, alerts, bio pages, admin |

## Team

| Part | Area | Owner |
|---|---|---|
| 1 | Foundation and tooling | Manas Singh Bisht |
| 2 | Database setup and load testing | Mansi Pandey |
| 3 | Redirect engine and trust layer | Nilay Joshi |
| 4 | Data, API and governance | Mayank Joshi |
| 5 | Frontend and deployment | Karan Pandey |

## Known limitations

- **Click events can be lost in a crash.** They are buffered in memory before the batch write, so events waiting in the queue are lost if the process crashes. The upgrade path is a durable queue such as Redis Streams.
- **Single instance only.** The cache and the click queue live in one process. Running several instances would need a shared cache and queue.
- **Bot labels are heuristics.** Email scanners publish no signatures, so the `email_scanner` label is marked "probable".
- **Unique visitors are counted per day.** The visitor hash rotates daily for privacy, so "unique visitors" over a period means unique visitors per day, added up.

## Credits

- Country data: GeoLite2 by MaxMind, https://www.maxmind.com
- Crawler list: [monperrus/crawler-user-agents](https://github.com/monperrus/crawler-user-agents)
- Threat feeds: Google Safe Browsing (non-commercial use) and abuse.ch URLhaus
- UI design reference: TemplateMo 627 "HyperLuv"
- Problem evidence: Pew Research Center, "When Online Content Disappears" (2024), https://www.pewresearch.org/data-labs/2024/05/17/when-online-content-disappears/ ; APWG Phishing Activity Trends Report, Q3 2025, https://docs.apwg.org/reports/apwg_trends_report_q3_2025.pdf
