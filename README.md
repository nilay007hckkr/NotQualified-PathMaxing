# PathMaxing

**The accountable short link.** PathMaxing is a URL shortener and link-analytics platform that keeps re-checking destinations after a link is published, and labels every click honestly as human, email scanner, link preview or crawler.

Built by team **Not Qualified** for the Tech Geeks hackathon, answering the "LinkPulse: URL Shortener & Analytics Platform" problem statement.

> This README grows with the project; the finished version arrives with the part 1 owner guide.

## Run it locally

You need Go 1.24 or newer.

```bash
go run ./cmd/server          # http://localhost:8080
curl http://localhost:8080/healthz
```

Without `DATABASE_URL` the server runs on in-memory defaults. For Postgres, start the local container with `docker compose up -d db` and copy `.env.example` to `.env` (every variable is described there).

Checks before every commit:

```bash
go build ./... && go vet ./... && go test ./...
```

CI also runs `go test -race ./...`, which needs a C compiler.

## Team docs

- [`SUMMARY.md`](SUMMARY.md): the problem, the solution and the key decisions
- [`PARTS.md`](PARTS.md): who owns which part of the code
- [`API.md`](API.md): the HTTP contract between backend and frontend
- [`COMMIT_PLAN.md`](COMMIT_PLAN.md): the 38 commits, in order
- [`CLAUDE.md`](CLAUDE.md): rules and decisions for anyone (or any agent) changing the code

## Layout

```
cmd/server/      the one binary: redirects, API, background workers
internal/app/    module registry, config, health check, web app serving
internal/shortcode/  random 8-character codes and alias rules
internal/urlcheck/   destination rules and the blocklist matcher
scripts/         step snapshots, step verification, handoff zips
```
