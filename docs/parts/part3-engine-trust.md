# Part 3: Redirect engine & trust layer (Nilay Joshi)

This part is what a visitor touches: the short link itself. It decides, in
a fixed order, whether a visit is allowed, sends the 302, labels who made
the visit, and keeps checking the destination after the link is published.
It also owns the shared contracts in `internal/domain` that let the five
parts be built and committed separately.

## 1. What it does and how it fits

```
visitor ─GET /{code}─▶ linkcache ─miss─▶ link store (Postgres, part 4)
                          │
                    classify (human? bot?) ──▶ rules engine ──▶ 302 / 451 / 410 / 404 / password form
                          │
                          └─▶ click sink (part 4's queue; never waits)

trust scheduler ─▶ SSRF-safe checker + threat feeds + blocklist ─▶ trust state
                    └─ quarantine: save, clear cache, audit entry, alert
```

- `cmd/server/module_core.go` (priority 10) provides an in-memory default for
  every contract, so the server starts with nothing else installed. Later
  modules replace them (Postgres store, click queue, real sessions). In the
  Wire phase it puts the cache in front of whichever link store is final, and
  in Routes it mounts `GET`/`HEAD`/`POST /{code}`.
- `cmd/server/module_trust.go` (priority 30) builds the threat feeds (each
  key optional), the SSRF-safe checker, the scanner, the scheduler, the
  alert evaluator and GeoLite2 (optional), and mounts `/api/links/{id}/trust`,
  `/api/links/{id}/rescan`, `/api/alerts` and `/p/{code}`.
- Lower parts never import higher ones. When part 3 needs something part 4
  implements (the store, the auth guard), the interface lives in
  `internal/domain` and the implementation arrives through `internal/app`.

## 2. Files and main types

| Package | What it holds |
|---|---|
| `internal/domain` | `Link`, `ClickEvent`, `Principal`, `Precheck`; the contracts `LinkStore`, `LinkResolver`, `CapReserver`, `Blocklist`, `ClickSink`, `TrafficClassifier`, `TrustChecker`, `CacheInvalidator`, `AuthGuard`, `GeoLookup`, `AuditLog`, `AlertCounter`; fail-safe defaults (`DenyAllGuard`, `SkipTrustChecker`, ...) |
| `internal/memstore` | the in-memory store and click log used before Postgres and in tests |
| `internal/rules` | `Engine.Decide`: the rule order and device targeting |
| `internal/redirect` | the HTTP handler for short links, the status pages (`page.html`) |
| `internal/linkcache` | `Cache`: sharded, TTL, negative entries, shared reads, generation counter |
| `internal/secret` | signed per-link unlock cookies, the daily visitor hash |
| `internal/ratelimit` | token-bucket `Limiter` (password tries, re-scans; part 4 uses it too) |
| `internal/httpx` | client IP behind proxies, JSON errors, strict JSON decoding |
| `internal/classify` | `Simple` (first default), `Honest` (the real one), user-agent parsing, burst detector, `testdata/user_agents.tsv` |
| `internal/threatintel` | Safe Browsing v4 and URLhaus clients, `Set` (parallel, with timeout), `Fake` |
| `internal/trust` | `Checker` (SSRF-safe fetch), `Evaluate` (state machine), `Scanner`, `Scheduler`, `NextCheck`, `PGRepo`/`MemRepo`, the trust API, the transparency page |
| `internal/geo` | GeoLite2 country lookup |
| `internal/alerts` | the alerts store, evaluator and `GET /api/alerts` |
| `migrations/C20_trust.sql`, `C29_alerts.sql` | `destination_checks`, trust bookkeeping on `links`, `alerts` |

## 3. Run and test

```bash
go run ./cmd/server                  # in-memory: links 404 until a store exists
DATABASE_URL=... go run ./cmd/server # the real thing (see README)
go test ./internal/rules/ ./internal/redirect/ ./internal/linkcache/ ./internal/classify/ ./internal/trust/
TEST_DATABASE_URL=... go test ./internal/trust/ ./internal/alerts/   # adds the Postgres tests
go test -v -run Accuracy ./internal/classify/                        # prints the classifier numbers
```

Run without `-race` on a laptop without gcc; CI runs `-race`. To try the
takedown by hand: log in as an admin, block a domain in the admin screen,
press "Re-scan now" on a link that ends on that domain, and visit it.

## 4. Key design decisions

- **Rule order, first refusal wins:** blocked (admin, quarantine, any
  destination on the blocklist) → 451; inactive (paused, archived, deleted)
  → 410; not started → 404, expired (`expires_at <= now`) → 410; password →
  form; click cap → 410. The cap is the only rule that writes, so it is last:
  a visit refused for any other reason never uses a click.
- **302 with `Cache-Control: private, no-store`, never 301.** Browsers cache
  a 301 forever, hiding repeat clicks and outliving a takedown.
- **Only humans use up a cap.** Chat apps fetch a link the moment it is
  pasted; counting that would burn one-time links. Bots only check whether
  the cap is full, using the cached copy, so for up to the cache TTL a bot
  can still be redirected after the last human click (the handler drops the
  cached copy as soon as a human is refused, which shortens this).
- **Cache safety.** Every change invalidates after its commit. A per-shard
  generation counter stops a lookup that read the old row before a change
  from storing it after the change. A test reproduces exactly that race.
- **Unknown codes get a 404 and no click event**: there is no link to report
  them under. Paths that can't be codes (`favicon.ico`) never reach the cache.
- **Privacy.** No raw IP is stored: the visitor hash is an HMAC of IP and
  user agent with a key that changes every UTC day, so uniques are "per day".
  Only the referrer's host is kept. `X-Forwarded-For` is ignored unless
  `TRUSTED_PROXY_HOPS` says a proxy is in front.
- **Password unlock.** bcrypt check, 10 tries per 15 minutes per IP and link,
  and a signed cookie tied to a fingerprint of the current password hash, so
  changing the password locks everyone out again.
- **SSRF protection.** The checker refuses private, loopback, link-local,
  CGNAT and other non-public addresses in the dialer's `Control` hook, which
  runs after DNS for every connection, including each redirect hop. Proxies
  are disabled, redirects capped at 5, 5 s to connect, 15 s total, 256 KB read.
- **Trust states.** A threat-feed hit, or a blocklisted host anywhere on the
  redirect chain, quarantines (sticky until an admin releases it); two failed
  checks in a row degrade; ending on a different host than the first good
  check means changed. The feeds are asked about both the written URL and
  where it really ends.
- **Schedule.** New links are due at once, then after 1 h, 24 h, then daily
  while live and weekly when paused, archived or expired; a failed check is
  retried after 30 min. A forced re-scan wakes the scheduler immediately.
- **Honest clicks.** HEAD with a browser user agent → probable email scanner;
  prefetch, HTTP libraries → other bot; chat preview agents → link preview;
  the community crawler list → crawler, and claimed Googlebot/Bingbot/Applebot/
  Yandex/Baidu visits are verified by reverse and forward DNS (impostors
  become other bot); three or more different links opened from one public
  /24 within 2 s → probable scanner burst (a crowd on one Wi-Fi scanning the
  same QR code opens one link, so it stays human; private addresses are
  ignored). Every event stores its reason code.
- **Classifier accuracy, and what it means.** On the labelled list in
  `internal/classify/testdata/user_agents.tsv` (51 human, 54 bot user agents,
  GET requests, no DNS, no bursts) the test measured: bot precision 1.000,
  bot recall 1.000, exact bot class 49 of 54 (0.907). We wrote that list
  ourselves from published user-agent formats, alongside the rules, so these
  numbers show the rules cover the list. They are not accuracy on real
  traffic, which we have not measured. "Email scanner" is always "probable".
- **Visitor pages** use the PathMaxing design but system fonts: loading web
  fonts on the redirect path would tell a font host about every visitor.

## 5. Questions judges may ask

1. **Why can't the click cap be exceeded when 1,000 people click at once?**
   Each human visit runs one statement, `UPDATE links SET cap_used = cap_used
   + 1 WHERE id = $1 AND cap_used < $2`. Postgres locks the row, so the
   updates queue and each re-checks the condition. Our tests race 1,000
   goroutines on the in-memory store and 1,000 on Postgres, and the k6 test
   sends 1,000 HTTP requests; each time exactly the cap got through.
2. **How fast is a redirect, and does it wait for the database?** On a cache
   hit it reads nothing from Postgres; the click is queued, not written.
   Only capped links do one `UPDATE`. On the build laptop over localhost, k6
   measured p95 1.23 ms; a deployed server adds network time.
3. **How do you stop someone using your scanner to attack your own network
   (SSRF)?** The address check happens when the socket connects, after DNS,
   for every hop, so `http://169.254.169.254/`, a public name pointing at
   `10.0.0.5`, or a redirect into a private range are all refused. Tests
   cover each case.
4. **What happens when a link turns malicious after it was shared?** The
   next scan (or a forced re-scan) quarantines it: the state is saved, the
   cache entry is cleared so the very next visit gets the 451 warning page,
   an audit entry and an alert are written, and the click history stays.
5. **How sure are you that a click is human?** Not certain, and we say so.
   Signals are heuristics; we store the reason with every event, verify
   search crawlers by DNS, and label scanners "probable". The accuracy
   figures above describe our test list, not real traffic.
