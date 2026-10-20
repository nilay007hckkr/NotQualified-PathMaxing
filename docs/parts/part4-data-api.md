# Part 4 owner guide: data, API and governance

Owner: Mayank. Commits: C07, C09, C11, C13, C16, C19, C22, C24, C26, C30, C35.

## 1. What Part 4 does and how it fits

Part 4 is everything behind the dashboard. It stores links, records clicks, handles accounts, and serves the management API under `/api/*`. It also adds analytics, QR codes, link-in-bio pages, CSV import and the admin tools.

PathMaxing is one Go binary. Each part plugs in through a small file in `cmd/server`. Part 4 has two:

- **`module_data.go`** (priority 40). If the db module (Part 2) opened a Postgres pool, this module:
  - replaces the in-memory defaults with the Postgres store;
  - starts the click queue and its batch writer;
  - starts the live-click broker and the analytics queries;
  - starts a loop that reloads the blocklist every 30 s.

  Without a database it does nothing, and the in-memory defaults stay.
- **`module_api.go`** (priority 50). If there is a database, this module:
  - replaces the deny-all auth guard with real sessions;
  - seeds or promotes the admin from `ADMIN_EMAIL` / `ADMIN_PASSWORD`;
  - mounts the API and the public `/bio/{slug}` pages.

Part 4 implements these shared contracts from `internal/domain` (Part 3, C02):

| Contract | Implemented by | Used by |
|---|---|---|
| `LinkStore` (`ResolveCode`, `ReserveClick`, `GetLink`) | `store.Store` | the redirect handler |
| `Blocklist` (`Blocked`) | `store.Store`, answered from memory | the rule engine, on every visit |
| `AuditLog` (`Append`) | `store.Store` | any part writing audit entries |
| `ClickSink` (`Record`) | `clicks.Queue` | the redirect handler, on every visit |
| `AuthGuard` (`Authenticate`) | `auth.Service` | every API route, Part 3's included |

It uses three contracts that other parts provide:

- `TrustChecker`: the threat check and probe before a link is published.
- `CacheInvalidator`: called after every committed change to a link.
- `AlertCounter`: fills `alerts_open` in the overview, when present.

**Hot path versus API.** A visit to `GET /{code}` is handled by Part 3's redirect handler. From Part 4 it touches only:

- the link lookup (with Part 3's cache in front);
- the one-statement cap reservation;
- the in-memory blocklist;
- `Record`, which drops the event into a queue and returns at once.

A visit never waits for a click to be written. Everything under `/api/` is ordinary request/response work that goes straight to Postgres.

## 2. Files and main types

Packages (all under `internal/`):

- `store`: all SQL for the link-side tables.
  - `links.go`: create, resolve, reserve, soft delete.
  - `manage.go`: list, update, status.
  - `org.go`: campaigns, tags, folders.
  - `clicks.go`: the batch insert.
  - `bio.go`, `imports.go`, `admin.go`, `governance.go`: bio pages, CSV imports, admin queries, and the blocklist and audit log.
  - Main types: `Store`, `NewLink`, `LinkFilter`, `LinkUpdate`.
- `clicks`
  - `queue.go`: `Queue`, the bounded click queue and batch writer (implements `ClickSink`).
  - `broker.go`: `Broker`, which fans committed clicks out to live viewers.
- `analytics`
  - `analytics.go`: the report types (`LinkReport`, `CampaignReport`, `Overview`, `Totals`, `Point`) and `Query` (from, to, tz, interval).
  - `store.go`: the SQL.
- `auth`: `auth.go` has `Service` (signup, login, logout, the guard, admin seeding, the users list) and `User`.
- `api`: the HTTP handlers. `Server` holds the dependencies.
  - `validate.go`: the shared link checks, used by validate, create, PATCH and import.
  - `links.go`, `org.go`, `analytics.go`, `qrbio.go`, `imports.go`, `admin.go`: one file per area.
- `bulk`: `csv.go` turns a CSV into `Row`s. It only parses; the checks are the same ones as for normal links.
- `qr`: `qr.go` makes a PNG with `github.com/skip2/go-qrcode`, or an SVG drawn from that library's bitmap.
- `bio`: `bio.go` is the public `/bio/{slug}` page (html/template, strict CSP).
- `admin`: `admin.go` has `Service`, which disables and enables links and blocks and unblocks domains. Each action invalidates the cache and writes an audit entry.

Migrations (plain SQL, applied in name order by Part 2's runner):

- `C09_click_events.sql`: `click_events`, with a unique `event_id` and `(link_id, ts)` and `(workspace_id, ts)` indexes.
- `C11_auth.sql`: `workspaces`, `users` (unique `lower(email)`, role `user` or `admin`) and `sessions` (keyed by the token's hash).
- `C16_organisation.sql`: `campaigns`, `tags`, `folders` and `link_tags`, plus `links.campaign_id` and `links.folder_id`.
- `C22_click_rollups.sql`: `click_rollups` in 15-minute buckets, backfilled from existing events.
- `C24_bio_pages.sql`: `bio_pages` (unique `lower(slug)`) and `bio_page_links` (ordered).
- `C26_bulk_import.sql`: `bulk_import_jobs` and `bulk_import_rows`.

The `links` table itself comes from Part 2's `C03_core_schema.sql`.

## 3. How to run and test it

Start Postgres, then the server:

```sh
docker compose up -d db
DATABASE_URL='postgres://pathmaxing:pathmaxing@localhost:5432/pathmaxing?sslmode=disable' \
ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD='change me please' \
go run ./cmd/server
```

In PowerShell, set the variables first, for example `$env:DATABASE_URL = "postgres://..."`, then run `go run ./cmd/server`. Without `DATABASE_URL` the server still starts, but there are no accounts and `/api/` is not mounted.

Try it with curl. State-changing requests must send JSON and our own `Origin`:

```sh
B=http://localhost:8080
curl -i -c jar.txt -H "Origin: $B" -H 'Content-Type: application/json' \
  -d '{"email":"asha@example.com","password":"correct horse","name":"Asha"}' $B/api/auth/signup
curl -b jar.txt -H "Origin: $B" -H 'Content-Type: application/json' \
  -d '{"destination":"https://example.com/sale","alias":"diwali","max_clicks":100}' $B/api/links
curl -i $B/diwali                     # 302 to the destination
curl -b jar.txt "$B/api/analytics/overview?tz=Asia/Kolkata"
```

Tests:

```sh
export TEST_DATABASE_URL='postgres://pathmaxing:pathmaxing@localhost:5432/pathmaxing?sslmode=disable'
go test ./...
```

- **Database tests** are in `store`, `analytics` and `api`. Each test makes its own throwaway schema with every migration applied (`internal/migrate/pgtest`), so `TEST_DATABASE_URL` can point at your normal local database. Without it, those tests are skipped.
- **Tests that never need a database** are the click queue tests (a fake writer), the CSV parser and QR.
- **`-race`.** Teammates run the tests without `-race`, because it needs a C compiler. CI adds `-race`.
- **Speed.** API tests use the lowest bcrypt cost, so they run fast.

## 4. Key design decisions, and why

- **Atomic click cap.** One statement: `UPDATE links SET cap_used = cap_used + 1 WHERE id = $1 AND cap_used < $2`. Postgres locks the row, so concurrent visits queue up and each one re-checks the condition, and the cap can never be overshot. A store test sends 1,000 concurrent reservations at a cap of 50 and gets exactly 50.
- **Insert and retry for codes, never "check, then insert".** Two people checking "is `sale` free?" at the same moment would both see yes. Instead we insert and let the unique index decide:
  - a custom alias that loses gets `ALIAS_TAKEN` (409);
  - a random 8-character code that collides is replaced and retried, up to 5 times.

  The validate step does look the alias up early, but only so the wizard can warn sooner.
- **Case-insensitive codes.** The unique index is on `lower(code)`, so `Sale` and `sale` are the same link.
- **Soft delete.** Deleting sets `deleted_at` and nothing else, so:
  - the code stays taken forever, and a printed QR code never starts pointing at someone else's link;
  - the analytics survive;
  - the redirect can answer 410.
- **Owner scoping, answered with 404.** Every handler loads the resource and checks `Principal.CanAccess(workspace_id)`. Someone else's link looks exactly like one that doesn't exist, so nobody can probe for other users' links. Admins can see everything. Admin-only routes answer 403 instead, because they are not anybody's own resources.
- **Login cookie and CSRF.**
  - The session cookie `lp_session` is HttpOnly, SameSite=Lax, and Secure in production.
  - On top of that, every state-changing request (anything but GET, HEAD or OPTIONS) must carry an `Origin` header (or, failing that, a `Referer`) from our own origin, and JSON bodies must be `application/json`.
  - A cross-site form can't meet either condition.
- **Hashed session tokens.** The cookie holds 32 random bytes, and the `sessions` table stores only their SHA-256. A leaked copy of the table can't be used to log in, and logout deletes the row.
- **Passwords.**
  - Passwords are hashed with bcrypt.
  - Login runs a dummy comparison for unknown emails, so both kinds of failure take about the same time.
  - Both kinds of failure give the same error.
- **Clicks are written asynchronously, in batches, idempotently.**
  - `Record` puts the event into a bounded queue (`CLICK_QUEUE_SIZE`, default 10,000). If the queue is full, the event is dropped and counted, and the redirect never waits.
  - A worker writes batches of 500 rows, or whatever it has after 1 s, in one `INSERT ... SELECT FROM unnest(...) ON CONFLICT (event_id) DO NOTHING`.
  - Each visit has its own `event_id`. If a write times out but actually committed, the retry inserts nothing twice.
  - A failed batch is retried up to 5 times with backoff, then dropped and counted.
  - On shutdown the worker drains the queue. The app stops HTTP first, so no new clicks arrive while it drains.
- **15-minute rollups, not hourly.** India is UTC+05:30, so an IST hour starts at :30 UTC. An hourly UTC bucket would be split across two IST hours, and the numbers could not be regrouped exactly. A 15-minute bucket always fits inside one IST hour, and inside any zone whose offset is a multiple of 15 minutes (Nepal is +05:45).
  - Totals, the series, outcomes and sources read whole buckets from the rollups.
  - Only the ragged edges of the period come from the raw events, so the answer is exact for any `from` and `to`.
  - A test checks that the rollups give the same numbers as the raw events, across several time zones.
- **Rollups are updated in the same INSERT statement.** The insert is `WITH ins AS (INSERT ... RETURNING ...) INSERT INTO click_rollups ... ON CONFLICT DO UPDATE SET n = n + EXCLUDED.n`. Only rows that were really inserted get counted, so a retried batch can't count twice. Events and rollups commit or fail together.
- **Counting rules.**
  - A class count (human, email_scanner, ...) counts that class's redirected visits.
  - `refused` counts every refused visit, so the numbers add up to all visits.
  - Breakdowns count human clicks only.
  - Visitor hashes rotate daily (privacy), so we report unique visitor-days, not unique visitors.
- **Live updates over SSE.**
  - The writer publishes each batch to an in-process `Broker` after it commits, and `/api/analytics/live` streams those clicks.
  - A slow viewer skips clicks rather than holding the writer up.
  - A heartbeat goes out every 15 s.
  - The stream ends when shutdown starts, because the HTTP server waits for open requests.
- **CSV import in two steps: preview, then commit.**
  - Upload checks every row with the same validators as the wizard, plus `DUP_IN_FILE` and `UNKNOWN_CAMPAIGN`, and saves the result. Nothing is created yet.
  - Commit flips the job to `committed` in one conditional UPDATE, so a double click creates nothing twice (the second commit gets 409).
  - Each row is then created through the same code as `POST /api/links`. A row that conflicts now (say, its alias was taken meanwhile) is reported as skipped.
  - Files are limited to 1 MB and 1,000 rows (413).
- **Rate limits.** These use Part 3's token-bucket limiter:
  - link creation: 30 per minute per user;
  - imports: 5 per hour per user;
  - login: 10 per 15 minutes per IP address.

  Going over gives 429 with `Retry-After`.
- **No foreign keys from `links` to `users` or `workspaces`.** Part 2's schema test from C03 inserts links for workspace 1 without creating that workspace, so adding these keys in C11 would have broken a test that isn't ours. Every link the API creates takes both ids from the logged-in session. A later migration can add the keys once that test creates its rows.

## 5. Questions judges are likely to ask

**1. If the server crashes, do you lose clicks?**
Yes, some. Events waiting in the in-memory queue, and the batch being written, are lost on a crash or a hard kill. A normal shutdown or deploy (SIGTERM) drains the queue first. When the queue is full, new events are dropped and counted rather than slowing visitors down. That was our trade-off: redirects stay fast and never depend on the database being quick. A durable queue would remove the loss, but it is another service to run.

**2. How do you stop two people getting the same alias, or a capped link going over its limit?**
Postgres decides both, in one statement each. The unique index on `lower(code)` lets only one insert win. The conditional `UPDATE ... WHERE cap_used < max` can't pass the cap, even under heavy concurrency. Both have concurrency tests.

**3. Can you run more than one server instance?**
The database parts are safe across instances: codes, caps, idempotent click inserts and rollup upserts. Two things live in each process's memory, though:
- **Rate limits:** with two instances, each enforces its own limit.
- **The live-click broker:** a viewer only sees clicks written by the instance they are connected to.

For one instance on Render that is fine. Sharing them would need something like Redis, or Postgres LISTEN/NOTIFY.

**4. How do you know the analytics numbers are right, especially in IST?**
- Tests insert a small hand-counted set of clicks and check every total, series point and breakdown.
- That set includes two clicks at 18:29 and 18:31 UTC. In IST they fall on different days; in UTC, on the same day.
- Another test checks that the rollup-based numbers equal the raw-event numbers across several time zones.

**5. How do you protect users and their data?**
- Users only ever reach their own workspace (anyone else's resource is 404).
- Passwords use bcrypt, and session tokens are stored only as hashes.
- State-changing requests must come from our own origin.
- Admin takedowns and blocklist changes are written to an audit log.
- Raw IP addresses are never stored. Click events keep only a daily-rotating visitor hash and the country.
