# Part 2: Database setup & load testing (Mansi Pandey)

This part gives PathMaxing its database and proves the system under load.
It applies the schema automatically when the server starts, defines the
core tables every other part builds on, fills a demo database with real
traffic, and contains the k6 tests behind the "exactly the cap" and
"fast redirects" claims.

## 1. What it does and how it fits

- `cmd/server/module_db.go` (priority 20) runs when `DATABASE_URL` is set:
  it opens a pgx connection pool, waits up to 30 s for the database (Neon
  can take a few seconds to wake), applies every new migration, adds a
  `database` check to `/healthz`, and hands the pool to the modules that need
  it (the store, auth, trust, alerts). Without `DATABASE_URL` it does nothing
  and the in-memory defaults stay. It closes the pool last on shutdown,
  after the click writer has flushed.
- `migrations/` holds plain SQL files named after the commit that added them
  (`C03_core_schema.sql`, `C09_click_events.sql`, ...). Sorting by name is
  commit order. Part 2 owns `embed.go` and `C03`; each later file belongs to
  the person whose commit added it.
- `cmd/seed` and `loadtest/` talk to a running server over HTTP only, exactly
  like a browser or a visitor would.

## 2. Files and main pieces

| Path | What it is |
|---|---|
| `internal/migrate/migrate.go` | `Files` (sorted, name rule `C<nn>_<name>.sql`) and `Run`: one transaction per file, recorded in `schema_migrations` with a checksum, under an advisory lock |
| `internal/migrate/pgtest` | `New(t)` / `Empty(t)`: a throwaway schema per test on `TEST_DATABASE_URL`, dropped afterwards; skips the test when the variable is unset |
| `migrations/embed.go` | `go:embed *.sql`, so migrations ship inside the binary (embed can't reach a parent folder, hence the file lives here) |
| `migrations/C03_core_schema.sql` | `links`, `redirect_rules`, `blocked_domains`, `audit_log` |
| `cmd/server/module_db.go` | connect, migrate, provide the pool, health check |
| `cmd/seed` | the demo traffic generator (`main.go`, `client.go`, `traffic.go`, `run.go`) |
| `loadtest/redirect.js`, `cap_race.js`, `lib.js`, `README.md` | k6 scripts and how to read them |

### The core schema (C03)

- `links`: `code` with a **unique index on `lower(code)`** (so "Sale" and
  "sale" are one link), `status` (active, paused, archived, disabled_admin;
  expiry is not a status), `trust_state` and `last_checked_at` (here, not in
  the trust migration, because the store and the links API read them early),
  `starts_at`/`expires_at` with a check that start comes first, `max_clicks`
  (> 0) and `cap_used` (≥ 0), `password_hash`, and `deleted_at` for soft
  delete. Deleted rows keep their code, so codes are never reused.
- `redirect_rules`: one destination per device (mobile, tablet, desktop),
  deleted with their link.
- `blocked_domains`: lowercase domains; blocking one blocks its subdomains
  (the matching is in the app, which keeps the list in memory).
- `audit_log`: who did what to which entity, with a JSON `detail`; `actor_id`
  is empty when the system acted (for example a quarantine).

## 3. Run and test

```bash
docker compose up -d db                       # local Postgres on 5432
DATABASE_URL=postgres://pathmaxing:pathmaxing@localhost:5432/pathmaxing?sslmode=disable go run ./cmd/server
TEST_DATABASE_URL=<same URL> go test ./internal/migrate/   # runner and schema tests
go test ./cmd/seed/                            # seed tests (no database needed)

# demo data, through the real API and redirects
go run ./cmd/seed -base http://localhost:8080 -password <8+ characters> -visits 400

# load tests (k6); start the server with TRUSTED_PROXY_HOPS=1 for the race
k6 run -e PASSWORD=<8+ characters> loadtest/redirect.js
k6 run -e PASSWORD=<8+ characters> loadtest/cap_race.js
```

Seed flags: `-base`, `-email` (default `demo@pathmaxing.example`, created if
missing), `-password` (or `SEED_PASSWORD`), `-name`, `-visits`, `-workers`,
`-seed` (fixes the random mix). Running it twice adds traffic but no
duplicate campaigns or links.

## 4. Key design decisions

- **Migrations run at startup, inside the binary.** Deploying is "start the
  new version"; there is no separate migrate command to forget.
- **One transaction per file, recorded in the same transaction.** A file that
  fails leaves nothing behind and is retried on the next start. A test
  plants a failing file and checks that its first statement was rolled back.
- **Advisory lock per schema.** Two instances starting together take turns
  instead of applying a file twice (tested with four concurrent runners).
  The lock is per schema, so parallel test schemas don't wait for each other.
- **Never edit an applied migration.** The runner stores each file's checksum
  and logs a warning if an applied file changes; a fix is a new migration.
- **The database enforces the rules too**: the unique `lower(code)` index is
  what decides alias races (the store inserts and retries instead of
  checking first), and check constraints refuse impossible states.
- **Test isolation** comes from a fresh schema per test, so tests from every
  package can run at once against one database.
- **The seed only uses HTTP.** It signs in, creates campaigns and links
  through the API, and visits links through `/{code}` with a weighted mix of
  phones, desktops, tablets, QR scans, chat previews, HEAD-only mail
  scanners, crawlers and scripts. It never follows redirects, so it doesn't
  load the destination sites, and it never writes to the database itself:
  every dashboard number comes from the same code paths as real visitors.
- **The cap race test sends a random /24 per request** (with the server
  trusting one proxy hop). Otherwise 1,000 requests from one machine in a
  second look like a scanner burst, the classifier calls them bots, and bots
  never use up a cap; the test would no longer be testing people.

## 5. Questions judges may ask

1. **What happens if two people create "Sale" and "sale" at the same time?**
   Both inserts reach Postgres; the unique index on `lower(code)` lets one
   in and rejects the other, which the API reports as ALIAS_TAKEN. A store
   test races eight such inserts and exactly one wins.
2. **How do you know the cap holds under load?** `loadtest/cap_race.js` sends
   1,000 requests at a link capped at 100. With the final script, three runs
   in a row on the build laptop each gave 100 redirects, 900 refusals and
   `cap_used` = 100.
3. **How fast are redirects?** `loadtest/redirect.js` measured p95 1.23 ms
   with 50 virtual users on the build laptop over localhost. That leaves out
   network time, so we rerun it against the deployed URL for a real figure.
4. **Is the demo data fake?** No numbers are typed in. The seed sends real
   HTTP requests through the redirect endpoint; the server classifies and
   records them like any other visit. The visitor addresses are simulated,
   which is why we call it seeded demo traffic.
5. **What if the database is down when the server starts?** It waits up to
   30 s, then exits with a clear error (without printing the password), and
   the host restarts it. While running, `/healthz` reports the database as
   unavailable, so the platform can see it.
