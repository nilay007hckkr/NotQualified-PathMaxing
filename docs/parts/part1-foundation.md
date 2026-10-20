# Part 1: Foundation & tooling (Manas Singh Bisht)

This part is the skeleton everything else plugs into: the one server
binary and its module registry, configuration, health check and web-app
serving; the short-code and URL rules that decide what a valid link is; CI
and the Dockerfile; and the scripts that turned one laptop's work into
commits for five people.

## 1. What it does and how it fits

`cmd/server/main.go` reads the configuration, makes a logger, and runs every
module that the parts registered. It never changes when a part is added:
each part adds its own `cmd/server/module_<part>.go`, which calls
`app.Register` from `init()`.

The registry in `internal/app` runs every module's hooks phase by phase,
each phase in priority order (core 10, db 20, trust 30, data 40, api 50):

1. **Provide**: register implementations. A later module replaces an
   earlier one's (the Postgres store replaces the in-memory default).
2. **Wire**: build things that need other modules' services (the link cache
   in front of the final store, the trust scanner).
3. **Routes**: mount HTTP handlers; services are final now.
4. **Start**: launch background workers with `a.Go`.
5. **Stop**: after shutdown, in reverse order, so the database closes last.

Services are shared through a typed container (`app.Provide[T]`,
`app.Get[T]`, `app.Lookup[T]`). That is what lets part 4's store satisfy
part 3's `domain.LinkStore` without either importing the other.

## 2. Files and main types

| Path | What it is |
|---|---|
| `cmd/server/main.go` | entry point: config, logger, SIGINT/SIGTERM, `app.Run` |
| `cmd/server/main_test.go` | boots every registered module with no database and checks `/healthz`, so a step that breaks startup fails its own tests |
| `internal/app/app.go` | `Module`, `Register`, `App`, `Provide`/`Get`/`Lookup`, `Go`, `AddCheck`, `Stopping` |
| `internal/app/server.go` | `Build` (Provide, Wire, Routes), `Server.Run` (Start, serve, graceful shutdown), panic recovery |
| `internal/app/config.go` | `Config` from the environment, with typed accessors for each part's own variables |
| `internal/app/builtin.go` | `/healthz`, the web app under `/app/` with SPA fallback, `/` |
| `internal/shortcode` | random 8-character lowercase codes; alias rules and reserved words |
| `internal/urlcheck` | destination rules (`Check`), `IsPublicIP`, the subdomain-aware blocklist `DomainSet` |
| `.github/workflows/ci.yml`, `Dockerfile`, `.dockerignore`, `docker-compose.yml`, `Makefile` | CI, the server image, local Postgres |
| `scripts/` | `ownership.txt`, `lib.sh`, `snapshot-step.sh`, `verify-steps.sh`, `make-handoff.sh` |
| `.env.example`, `SUMMARY.md`, `PARTS.md`, `API.md`, `COMMIT_PLAN.md` | configuration reference and team docs |

## 3. Run and test

```bash
go run ./cmd/server              # http://localhost:8080/healthz
go build ./... && go vet ./... && go test ./...   # what every teammate runs
go test -race ./...              # CI (needs a C compiler locally)
go test ./internal/app/ ./internal/shortcode/ ./internal/urlcheck/ ./cmd/server/
```

Every variable is described in `.env.example`. In production `SECRET_KEY`
(32+ characters) and an https `BASE_URL` are required, and the server
refuses to start without them rather than run insecurely.

## 4. Key design decisions

- **One binary, modules in phases.** A part can be committed on its own
  because modules only add files, defaults keep the server working before the
  real parts arrive, and the boot test runs the whole registry every step.
- **Graceful shutdown order:** stop accepting HTTP and finish in-flight
  requests; then cancel the workers and wait (the click writer drains what
  those last requests queued); then the Stop hooks in reverse order. A test
  holds a request open and checks this order.
- **Fail closed when parts are missing.** The default auth guard refuses
  everything, and a skipped check says it was skipped.
- **`/healthz` names a failing check but not its error,** which could reveal
  internal hosts; the error goes to the log.
- **Short codes:** 8 characters from a–z and 0–9 with unbiased sampling from
  `crypto/rand` (36^8, about 2.8 trillion codes). Aliases are 3–50 letters,
  digits, `-` and `_`, can't be a reserved word (`api`, `app`, `p`, `bio`,
  `admin`, ...), and are compared ignoring case.
- **URL rules surface before publishing:** only http/https; no `user@`
  before the host (`https://bank.example@evil.example` goes to evil); no
  private, loopback, link-local, CGNAT or reserved addresses, no
  `localhost` or internal names; numeric host tricks like `0x7f.1` refused;
  not our own host; not blocklisted (subdomains included). http and `xn--`
  domains give warnings, not errors.
- **Commits for five people from one laptop, without git.** `snapshot-step.sh`
  records only the files a step changed (and refuses another member's files);
  `verify-steps.sh` replays every step in an empty folder and runs the checks
  after each; `make-handoff.sh` packs each member's steps with exact commands.
- **Toolchain floor:** `go.mod` says `go 1.24` and CI uses that version, so
  teammates with an older Go than the build laptop can still build.

## 5. Questions judges may ask

1. **How do five people commit one codebase without breaking the build?**
   Every file has one owner (`scripts/ownership.txt`), every step only adds or
   changes its owner's files, and `verify-steps.sh` replayed all the steps
   one by one, building and testing after each, before the zips were made.
2. **Why a module registry instead of wiring everything in `main`?** So a
   part plugs in with one new file. The server works at every step; later
   modules replace the defaults by priority instead of editing earlier code.
3. **How do you stop a short link from pointing somewhere dangerous or
   internal?** `urlcheck` refuses bad schemes, credentials in the URL,
   private and reserved IPs, internal names and blocklisted domains at
   creation, and part 3 re-checks after DNS and every redirect.
4. **What happens on deploy or Ctrl+C?** HTTP stops first, then workers drain
   (no queued click is dropped by a normal shutdown), then the database
   closes. Each stage has a timeout (`SHUTDOWN_TIMEOUT`, 10 s by default).
5. **What does CI check?** `go build`, `go vet`, `gofmt`, `go test -race`
   with a real Postgres service, and the web app's `npm ci`, build and tests
   on Node 20.
