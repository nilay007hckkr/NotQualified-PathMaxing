# Load tests (k6)

Two [k6](https://k6.io) scripts that drive a running PathMaxing server
through its real API and redirect endpoint. Both sign in as a load-test
account (created on first use) and make their own link.

```bash
# server: TRUSTED_PROXY_HOPS=1 is needed for cap_race.js (see below)
TRUSTED_PROXY_HOPS=1 DATABASE_URL=... go run ./cmd/server

k6 run -e PASSWORD=some-long-password loadtest/redirect.js
k6 run -e PASSWORD=some-long-password loadtest/cap_race.js
```

Options (all `-e NAME=value`): `BASE` (default `http://localhost:8080`),
`EMAIL`, `PASSWORD`; `VUS`, `DURATION`, `P95_MS` for `redirect.js`;
`CAP`, `VISITORS` for `cap_race.js`.

## redirect.js: redirect latency

Ramps to 50 virtual users following one short link for 30 s. Every request
stops at the 302 (`redirects: 0`), so it measures PathMaxing, not the
destination site. The run fails if p95 goes over `P95_MS` (100 ms by
default); that number is a target, not a promise.

**How to read it:** look at `http_req_duration` in the summary, especially
`p(95)`: 95% of redirects were at least that fast. The click events are
written by the background batch writer while this runs, so the number
includes that work happening alongside. `checks` must stay at 100%: every
answer a 302 with `Cache-Control: no-store`.

**What we measured** (on the build laptop, server and k6 on the same
machine over localhost, local Postgres, warm cache): p95 1.23 ms with 50
VUs over 15 s. Localhost hides network time, so a deployed server will be
slower; rerun against the deployed URL for the demo figure.

## cap_race.js: the click-cap race

1,000 virtual users each follow a link capped at 100 clicks at the same
moment. The thresholds require exactly 100 redirects and 900 refusals
(410), and teardown checks through the API that `cap_used` is exactly 100.
This is the conditional `UPDATE links SET cap_used = cap_used + 1 WHERE id =
$1 AND cap_used < $2` doing its job under contention.

Two details make the test honest:

- Each request carries its own `X-Forwarded-For` from a random /24, and the
  server must trust one proxy hop (`TRUSTED_PROXY_HOPS=1`). Otherwise 1,000
  requests from one machine in a second look like a mail scanner's burst,
  the classifier labels them bots, and bots never use up a cap.
- Connections the operating system refuses (a thousand opened in the same
  instant can overflow the TCP accept queue, which is small on Windows) are
  retried: they never reached the server, so they can't affect the race.

**What we measured** on the build laptop: three runs in a row each gave
`cap_redirected` 100, `cap_refused` 900 and `cap_used` 100.
