# PARTS.md: 5-part team split, ownership and handoff (PathMaxing)

The code is split into 5 parts, one per team member. Each part was built as a series of real build steps (C01–C40, used below and in the migration file names) and committed as the 15 commits in `COMMIT_PLAN.md`.

Every member gets a zip named after them (`Nilay.zip`, `Mayank.zip`, `Karan.zip`, `Mansi.zip`, `Manas.zip`). It contains their code and an `INSTRUCTIONS.txt`. Each member then commits their own steps from their own GitHub account.

## Owners and workload

The split follows availability. Nilay and Mayank write most of the code, Karan a medium share, and Mansi and Manas the least.

| Part | Name | Owner | Commits | Share |
|---|---|---|---|---|
| 1 | Foundation & tooling | Manas Singh Bisht | 2 | light |
| 2 | Database setup & load testing | Mansi Pandey | 1 | light |
| 3 | Redirect engine & trust layer | Nilay Joshi | 5 | heavy |
| 4 | Data, API & governance | Mayank Joshi | 5 | heavy |
| 5 | Frontend & deployment | Karan Pandey | 2 | medium |

If you change who owns what, update this table and the members table in `COMMIT_PLAN.md` together.

The "C..." numbers in the part sections below are build steps. `COMMIT_PLAN.md` shows which commit contains each of them.

## Rules that make the split work

1. **Every file belongs to exactly one part.** The source of truth is `scripts/ownership.txt`: one line per path glob, in the form `<part-number> <glob>`, where `*` also matches `/`. Every tracked file must match exactly one line, and `scripts/verify-steps.sh` fails on any file that is unowned or matched twice. The only exception is `go.mod`/`go.sum` (part `*` in the file), which are shared and travel with whichever commit adds a dependency.
2. **Commits follow the global order in `COMMIT_PLAN.md`** (C01 → C38), which interleaves the five members. Code may depend only on earlier commits. A part's Go packages may import only from parts with a lower number.
3. **Every commit prefix builds.** Applying C01..Ck in order must pass `go build ./...`, `go vet ./...` and `go test ./...`, plus `npm run build` and `npm test` once `web/` exists. `scripts/verify-steps.sh` checks every k. Teammates run these checks without `-race` (it needs a C compiler); CI adds `-race`. `go.mod` stays at `go 1.24` and the web app supports Node 20, so older toolchains on teammates' laptops still work.
4. **Shared interfaces and the registry.**
   - Shared types and interfaces live in `internal/domain` (Nilay, C02): link store, click sink, traffic classifier, trust checker, cache invalidator, auth guard, geo lookup.
   - C02 also ships safe defaults so the server runs early: an in-memory store, a simple user-agent classifier and a deny-all auth guard.
   - The module registry with start/stop hooks lives in `internal/app` (Manas, C01). Later commits register real implementations from their own `cmd/server/module_<name>.go` in `init()`.
5. **Migrations are named after the commit that adds them:** `migrations/C<nn>_<name>.sql`. File order equals commit order. A later commit may `ALTER` an earlier table but never edits an earlier migration file. The runner records which files it has already applied.
6. **Each part has an owner guide** at `docs/parts/partN-<slug>.md`. It covers:
   - what the part does and how it fits in;
   - the files in it and the main types;
   - how to run and test it;
   - the key design decisions;
   - 5 questions judges are likely to ask, with short answers.

   The owner must be able to explain their code.

## Part 1: Foundation & tooling (Manas, 4 commits, light)

**Owns:**
- top-level files: `go.mod`/`go.sum` (shared), `README.md`, `.gitignore`, `.gitattributes` (LF line endings, so the shell scripts survive a Windows checkout), `.env.example`, `.dockerignore`, `Makefile`, `Dockerfile`, `docker-compose.yml`
- tooling: `.github/workflows/ci.yml`, `scripts/` (including `ownership.txt`, `lib.sh`, `snapshot-step.sh`, `verify-steps.sh`, `make-handoff.sh`)
- docs: `SUMMARY.md`, `PARTS.md`, `API.md`, `COMMIT_PLAN.md`
- `cmd/server/main.go` and its boot test, `internal/app` (registry, config, health check, static serving), `internal/shortcode`, `internal/urlcheck`
- `docs/parts/part1-foundation.md`

**Builds:**
- **C01 (bootstrap):**
  - A server entrypoint whose module registry has start/stop hooks.
  - Config read from the environment.
  - `/healthz`.
  - Graceful shutdown that stops HTTP first, then drains the workers.
  - Serving `web/dist` under `/app/` when `WEB_DIST_DIR` is set.
  - A boot test that starts every registered module with no database, so any later step that breaks startup fails its own checks.
  - `docker-compose.yml` with Postgres, `.env.example` listing every variable, and all team docs and scripts.
- **C05 (links):** short-code generation (random 8-character lowercase codes, case-insensitive), alias rules including reserved words, and URL validation with the blocklist matcher. Table-driven tests for all of it.
- **C28 (CI):** GitHub Actions running `go vet`, `go test -race` and the web build and tests, plus the production `Dockerfile`.
- **C38 (docs):** the owner guide and a finished README.

**Done when:** the server starts with only C01, `/healthz` works, and the shortcode and urlcheck tests cover every rule in `API.md`.

## Part 2: Database setup & load testing (Mansi, 4 commits, light)

**Owns:**
- `internal/migrate` (including `internal/migrate/pgtest`, which gives each database test its own throwaway schema), `cmd/server/module_db.go`, `migrations/embed.go` (`go:embed` can't reach a parent directory, so the embedding lives next to the files), `migrations/C03_*.sql`
- `cmd/seed/`, `loadtest/`
- `docs/parts/part2-database.md`

**Builds:**
- **C03 (database):**
  - An embedded migration runner. It applies unapplied files in name order at startup through a registry hook.
  - The core schema: `links` (case-insensitive unique index on `lower(code)`, soft delete, `cap_used`, plus `trust_state` and `last_checked_at`, which the store and the links API read long before C20), `redirect_rules`, `blocked_domains`, `audit_log`.
- **C18 (seed):** sends realistic traffic through the *real* API and redirect endpoint:
  - human and bot user agents;
  - QR scans (`?s=qr`);
  - several campaigns and referrers.
- **C31 (load tests):** k6 scripts for p95 redirect latency and a cap race with 1,000 concurrent requests, plus notes on how to read the results.
- **C37 (docs):** the owner guide.

**Done when:** migrations apply cleanly to an empty database, the seed run fills the dashboard, and the k6 cap race shows exactly `max_clicks` redirects.

## Part 3: Redirect engine & trust layer (Nilay, 11 commits, heavy)

**Owns:**
- `internal/domain`, `internal/memstore`, `internal/rules`, `internal/redirect`, `internal/linkcache`, `internal/secret`, `internal/ratelimit`, `internal/httpx`
- `internal/threatintel`, `internal/trust`, `internal/classify`, `internal/geo`, `internal/alerts`
- `cmd/server/module_core.go`, `cmd/server/module_trust.go`, and the migrations from Nilay's commits
- `docs/parts/part3-engine-trust.md`

The SQL for the tables Part 3 owns (`destination_checks`, `alerts`) lives in store files inside `internal/trust` and `internal/alerts`, so Nilay never edits Mayank's `internal/store`. Those files only read `links` and `click_events`.

**Builds:**
- **C02 (domain):** shared types and interfaces, plus the defaults described in rule 4.
- **C06 (rule engine):** blocked/quarantined → inactive → schedule → password → click cap, then device targeting. Table tests for every rule, and a race test with 1,000 goroutines.
- **C08 (redirect):**
  - `GET`/`HEAD /{code}` and the password unlock (`POST /{code}`).
  - Pages for not found, unavailable, warning and the password form.
  - Signed unlock cookies, the daily visitor hash and the rate limiter.
- **C12 (cache):** the link cache, with generation-safe invalidation.
- **C14 (threat intel):** Safe Browsing and URLhaus clients, each with a fake for tests.
- **C17 (checker):** an SSRF-safe destination checker:
  - blocks private IPs after DNS resolution and after every redirect;
  - follows at most 5 redirects;
  - uses timeouts and a size limit.
- **C20 (trust states):**
  - The `destination_checks` table, plus the `next_check_at` column and the check bookkeeping on `links` (`trust_state` and `last_checked_at` already exist from C03).
  - The state machine: two 4xx/5xx in a row → `degraded`; the final domain changed → `changed`; a threat-feed hit → `quarantined`, which clears the cache and writes an audit entry.
- **C23 (scheduler):** checks at creation, then +1 h, +24 h, daily while active, weekly when idle. Plus `POST /api/links/{id}/rescan` and `GET /api/links/{id}/trust`.
- **C25 (classifier):** the honest-clicks classifier.
  - Signals: known-bot list, crawler IP verification, bursts from one IP or subnet, HEAD and prefetch requests.
  - Labels: `human`, `email_scanner` (probable), `link_preview`, `crawler`, `other_bot`, each with a reason code.
  - Includes a labelled list of user agents that reports precision and recall.
- **C29 (alerts):** GeoLite2 country lookup, alerts (spike, near cap, expiring, dead link, quarantined), `GET /api/alerts`, and the public transparency page `GET /p/{code}`.
- **C34 (docs):** the owner guide.
- **C39 (fix):** only bursts that open several different links count as a probable mail scanner, and private client addresses are ignored, so a crowd scanning one QR code on shared Wi-Fi stays human.

**Done when:**
- The rule-engine tests cover every rule.
- The race test passes with `-race`.
- SSRF tests block `127.0.0.1`, `10.0.0.0/8`, `169.254.169.254` and a redirect into a private range.
- A forced re-scan of a blocklisted destination quarantines the link in under 60 s.
- The classifier reports precision and recall on at least 50 human and 50 bot user agents.

## Part 4: Data, API & governance (Mayank, 11 commits, heavy)

**Owns:**
- `internal/store`, `internal/clicks`, `internal/analytics`, `internal/auth`, `internal/api`, `internal/bulk`, `internal/qr`, `internal/bio`, `internal/admin`
- `cmd/server/module_data.go`, `cmd/server/module_api.go`, and the migrations from Mayank's commits
- `docs/parts/part4-data-api.md`

**Builds:**
- **C07 (store):** the Postgres link store, with the atomic cap `UPDATE`, insert-and-retry for codes, and soft delete.
- **C09 (clicks):** the `click_events` table (with `traffic_class` and `class_reason`), a non-blocking queue with a counter for dropped events, a batch writer (500 rows or 1 s) and idempotent inserts on `event_id`.
- **C11 (auth):**
  - `users`, `sessions` and `workspaces` tables.
  - Signup, login and logout using bcrypt and HttpOnly session cookies, with an Origin check on state-changing requests.
  - The real auth guard and roles; admin seeded from env.
- **C13 (links API):** the two-step `validate` and `create`, plus list, get, update, pause, resume, archive and soft delete. Every query is scoped to the owner; another user's link returns 404.
- **C16 (organisation):** campaigns, tags and folders.
- **C19 (analytics queries):** queries by period (timezone-aware) and by campaign; breakdowns by device, browser, OS, referrer, country and source; human vs bot; unique visitors per day; outcomes.
- **C22 (analytics API):** the `/api/analytics/*` endpoints, 15-minute rollups (they regroup exactly into IST hours and days, which hourly UTC buckets can't) and live updates over SSE.
- **C24 (QR and bio):** QR codes (PNG and SVG) encoding `?s=qr`, and link-in-bio pages.
- **C26 (bulk import):** CSV preview, then commit, with per-row error codes; at most 1,000 rows and 1 MB.
- **C30 (admin):**
  - disable or enable links;
  - blocklist CRUD;
  - audit log, abuse queue and the users list;
  - rate limits on creation, imports and login.
- **C35 (docs):** the owner guide.
- India mode (`/{HEADER}/{slug}`) was optional and is not part of this build.

**Done when:**
- API tests cover the role checks and every validation code in `API.md`.
- Store tests pass against a real Postgres when `TEST_DATABASE_URL` is set.
- Analytics numbers match hand-counted fixtures.
- The CSV preview reports each planted error.

## Part 5: Frontend & deployment (Karan, 8 commits, medium)

**Owns:** `web/` (everything), `deploy/` (`Dockerfile.full`, which builds the web app and then Go, and `render.yaml`), `docs/parts/part5-web.md`.

**Builds:**
- **C04 (scaffold):** Vite + React + TypeScript, routing, a typed API client that follows `API.md`, and the login and signup pages.
- **C10 (dashboard):** KPIs, human vs bot, top links and alerts.
- **C15 (links):** the links list with filters, and the two-step creation wizard (validate → options and review → publish with the QR code).
- **C21 (link details):** charts by period, breakdowns, link vs QR, the trust timeline with "re-scan now", and actions.
- **C27 (campaigns and bulk):** the campaigns screen, the bulk-import screen (upload → preview table with row errors → commit) and the bio-page editor.
- **C32 (admin):** the abuse queue, blocklist, users, audit log and platform analytics.
- **C33 (deploy):** `Dockerfile.full` and `render.yaml`.
- **C36 (docs):** the owner guide.
- **C40 (deploy):** `web/vercel.json` for the Vercel frontend, which rewrites the API, short links, `/p` and `/bio` to the Render backend, plus `render.yaml` trusting both proxy hops.

**Done when:** `npm run build` and `npm test` pass, the app works on mobile, there is no fake data in production builds, and both demo scenarios from `SUMMARY.md` can be clicked through end to end.

## Handoff process

The full mechanics are in `COMMIT_PLAN.md`. In short:

1. The steps are built in the global order on one laptop. After each step passes its checks, `scripts/snapshot-step.sh` saves exactly the files that step changed.
2. `scripts/verify-steps.sh` rebuilds the repository commit by commit in a temp folder and runs the checks after each one.
3. `scripts/make-handoff.sh <REPO_URL>` writes `handoff/share/`:
   - `Nilay.zip`, `Mayank.zip`, `Karan.zip`, `Mansi.zip`, `Manas.zip`
   - `ORDER.txt` for the WhatsApp group

   Each zip contains that member's commit folders and an `INSTRUCTIONS.txt`.
4. Create the GitHub repository `NotQualified-PathMaxing` **empty** (no README, `.gitignore` or licence). Manas clones it and pushes C01 first; everyone else clones after that. Everyone then follows `ORDER.txt` and posts "Cxx pushed" in WhatsApp so the next person knows it's their turn.
5. Fixes after handout become new commits by the owner of the files changed. Never rewrite pushed history.
