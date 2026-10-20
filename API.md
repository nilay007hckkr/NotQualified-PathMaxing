# API.md: PathMaxing HTTP contract

This contract is shared by parts 1–4 (backend) and part 5 (frontend). Update it in the same change as any endpoint or response shape. The owning part is shown in brackets.

## Conventions

- **Format:** JSON in and out. Timestamps are RFC 3339 in UTC. IDs are integers.
- **Auth:** an `lp_session` cookie (HttpOnly, SameSite=Lax, Secure in production).
  - State-changing requests must send `Content-Type: application/json` (multipart for uploads) and come from our own `Origin`.
- **Ownership:** a user only ever sees their own resources. Anyone else's returns `404`, never `403`. Admins see everything.
- **Pagination:** `?limit=50&cursor=<opaque>`, returning `{"items": [...], "next_cursor": "..." | null}`.
- **Analytics query parameters:** `from`, `to` (RFC 3339), `tz` (IANA, default `Asia/Kolkata`), `interval` (`hour` | `day`).

### Errors

```json
{ "error": { "code": "NOT_FOUND", "message": "link not found" } }
```

### Validation results

Used by validate, create and update.

```json
{
  "errors":   [ { "field": "alias", "code": "ALIAS_TAKEN", "message": "alias is already taken" } ],
  "warnings": [ { "field": "destination", "code": "HTTP_NOT_HTTPS", "message": "destination uses http, not https" } ]
}
```

**Error codes:**

| Area | Codes |
|---|---|
| Destination URL | `INVALID_URL`, `SCHEME_NOT_ALLOWED`, `USERINFO_NOT_ALLOWED`, `SELF_LOOP`, `NOT_PUBLIC`, `BLOCKED_DOMAIN`, `THREAT_MATCH` |
| Alias | `ALIAS_TAKEN`, `ALIAS_RESERVED`, `ALIAS_INVALID` |
| Rules and options | `EXPIRY_IN_PAST`, `EXPIRY_BEFORE_START`, `CAP_INVALID`, `TARGET_DEVICE_INVALID` |
| Labels (link input and CSV) | `UNKNOWN_CAMPAIGN`, `UNKNOWN_TAG`, `UNKNOWN_FOLDER` (the id or name is not in the link's workspace) |
| CSV import only | `DUP_IN_FILE`, `EXPIRY_INVALID` (unparsable date) |

**Warning codes:** `HTTP_NOT_HTTPS`, `PUNYCODE_DOMAIN`, `LONG_REDIRECT_CHAIN`, `DESTINATION_UNREACHABLE`, `ONE_CLICK_NO_PASSWORD`.

### Destination and alias rules

Checked by `internal/urlcheck` and `internal/shortcode` [1]; the threat check and the reachability probe come from the trust layer [3].

| Rule | Code |
|---|---|
| Destination is required, at most 2,048 characters, with no spaces or control characters, and parses as a URL with a host | `INVALID_URL` |
| Host is a valid DNS name whose top-level domain is letters (or `xn--`), or an IP literal; internationalized names must be written in `xn--` form | `INVALID_URL` |
| Scheme is `http` or `https` (no `javascript:`, `data:`, `ftp:`, `file:`, `mailto:`) | `SCHEME_NOT_ALLOWED` |
| No `user:password@` before the host (`https://yourbank.example@evil.example` goes to `evil.example`) | `USERINFO_NOT_ALLOWED` |
| Not a private, loopback, link-local, CGNAT, documentation or reserved IP (IPv4-mapped IPv6 included), and not `localhost`, a single-label name, or a name under `.localhost`, `.local`, `.internal`, `.lan`, `.home.arpa`, `.intranet`, `.corp` | `NOT_PUBLIC` |
| Not this server's own host (no chains of short links) | `SELF_LOOP` |
| Host, or any parent domain of it, is not on the admin blocklist | `BLOCKED_DOMAIN` |
| A threat feed (Safe Browsing, URLhaus) doesn't list it | `THREAT_MATCH` |
| Alias is 3–50 characters of letters, digits, `-` and `_`, starting and ending with a letter or digit | `ALIAS_INVALID` |
| Alias isn't a reserved word (see Public routes), in any letter case | `ALIAS_RESERVED` |
| No other link, including a deleted one, has the alias in any letter case; decided by the unique index at insert | `ALIAS_TAKEN` |

Warnings never block publishing: `HTTP_NOT_HTTPS` for `http://`, `PUNYCODE_DOMAIN` when any label starts with `xn--`.

### HTTP status codes

`200`, `201`, `204`, `400` bad input, `401` not logged in, `404`, `409` conflict (alias taken at insert), `413` file too large, `422` validation failed, `429` rate limited (with `Retry-After`), `5xx`.

## Public routes

| Method & path | Owner | Behaviour |
|---|---|---|
| `GET` / `HEAD /{code}` | 3 | Rule engine. 302 to the destination (`Cache-Control: private, no-store`); 451/410/404 pages; password form; warning page if quarantined. `?s=qr` records a QR scan. HEAD always counts as a bot. |
| `POST /{code}` | 3 | Password form submit. 303 back to `/{code}` with a signed unlock cookie; 403 on wrong password; 429 when rate limited. |
| `GET /p/{code}` | 3 | Public transparency page: destination host, last check time, current trust state. |
| `GET /bio/{slug}` | 4 | Link-in-bio page. |
| `GET /app/*` | 1 (serving), 5 (content) | The React app. Unknown paths under `/app/` get `index.html`, so client-side routes survive a reload; missing files under `/app/assets/` get `404`. |
| `GET /` | 1 | `302` to `/app/` when the web app is served; otherwise a one-line text page. |
| `GET /healthz` | 1 | `200 ok` when every registered check (the database, once configured) passes; `503` listing the failing checks otherwise. |

Reserved codes, which can't be used as aliases: `api`, `app`, `p`, `bio`, `admin`, `login`, `logout`, `signup`, `static`, `assets`, `health`, `healthz`.

## Auth [4]

- `POST /api/auth/signup` `{email, password, name}` → `201 {user}`, sets the cookie (a new account is signed in); `409` if the email is taken
- `POST /api/auth/login` `{email, password}` → `200 {user}`, sets the cookie (rate-limited)
- `POST /api/auth/logout` → `204`
- `GET /api/auth/me` → `200 {user}` or `401`

`user` = `{id, email, name, role: "user" | "admin", workspace_id}`

## Links [4]

- `POST /api/links/validate` (link input) → `200 {errors, warnings}`. Step 1 of the wizard; saves nothing.
- `POST /api/links` (link input) → `201 {link, warnings}` | `422 {errors, warnings}` | `409` | `429`
- `GET /api/links?q=&status=&trust_state=&campaign_id=&tag_id=&folder_id=&limit=&cursor=`
- `GET /api/links/{id}` → `{link}`
- `PATCH /api/links/{id}` (partial link input) → `{link}` or `422`
- `POST /api/links/{id}/pause` | `resume` | `archive` → `{link}`
- `DELETE /api/links/{id}` → `204` (soft delete; analytics are kept)
- `GET /api/links/{id}/qr?format=png|svg&size=512` → an image that encodes `{BASE_URL}/{code}?s=qr`

### Link input

```json
{
  "destination": "https://shop.example/sale",
  "alias": "diwali",
  "title": "Diwali sale",
  "starts_at": null,
  "expires_at": "2026-11-05T18:29:59Z",
  "max_clicks": 100,
  "password": "optional",
  "targets": [ { "device": "mobile", "destination": "https://m.shop.example/sale" } ],
  "campaign_id": 3,
  "tag_ids": [1, 2],
  "folder_id": null
}
```

`alias` may be omitted for a random 8-character lowercase code. `device` is one of `mobile`, `tablet`, `desktop`.

### Link object

```json
{
  "id": 42, "code": "diwali", "short_url": "https://lp.example/diwali",
  "destination": "https://shop.example/sale", "title": "Diwali sale",
  "status": "active", "trust_state": "ok",
  "starts_at": null, "expires_at": "2026-11-05T18:29:59Z", "is_expired": false,
  "max_clicks": 100, "cap_used": 12, "has_password": true,
  "targets": [ { "device": "mobile", "destination": "https://m.shop.example/sale" } ],
  "campaign_id": 3, "tag_ids": [1, 2], "folder_id": null,
  "last_checked_at": "2026-10-19T09:00:00Z",
  "created_at": "2026-10-18T10:00:00Z", "updated_at": "2026-10-18T10:00:00Z"
}
```

`status` is one of `active` | `paused` | `archived` | `disabled_admin`. `trust_state` is one of `ok` | `degraded` | `changed` | `quarantined`.

## Campaigns, tags, folders [4]

- `GET` / `POST /api/campaigns`, `GET` / `PATCH` / `DELETE /api/campaigns/{id}`
  - Campaign: `{id, name, utm_source, utm_medium, utm_campaign, link_count, created_at}`
- Same pattern for `/api/tags` (`{id, name, color}`) and `/api/folders` (`{id, name, parent_id}`).

## Bulk CSV import [4]

- `POST /api/imports` (multipart field `file`; at most 1,000 rows and 1 MB) → `201`
  ```json
  {"id": 7, "status": "preview", "total": 50, "valid": 43, "invalid": 7,
   "rows": [{"row": 2, "alias": "promo", "destination": "...", "status": "invalid", "errors": ["ALIAS_TAKEN"]}]}
  ```
- `POST /api/imports/{id}/commit` → `{"created": 43, "skipped": [{"row": 9, "errors": ["ALIAS_TAKEN"]}]}`. Rows that conflict between preview and commit are reported as skipped.
- `GET /api/imports/{id}` → same shape as the preview.
- CSV columns:
  - required: `destination`
  - optional: `alias`, `title`, `campaign` (name), `tags` (separated by `;`), `expires_at`, `max_clicks`

## Analytics [4]

- `GET /api/analytics/links/{id}?from&to&tz&interval` →
  ```json
  {"link_id": 42, "tz": "Asia/Kolkata", "interval": "day",
   "totals": {"human": 120, "email_scanner": 30, "link_preview": 14, "crawler": 5, "other_bot": 2,
              "refused": 9, "unique_visitor_days": 98},
   "series": [{"t": "2026-10-18T00:00:00+05:30", "human": 10, "bots": 3, "refused": 1}],
   "by_device": {"mobile": 80, "desktop": 35, "tablet": 5},
   "by_browser": {"Chrome": 70}, "by_os": {"Android": 60},
   "by_referrer": [{"host": "instagram.com", "clicks": 40}],
   "by_country": [{"country": "IN", "clicks": 110}],
   "by_source": {"link": 100, "qr": 20},
   "outcomes": {"redirected": 171, "expired": 0, "cap_reached": 3, "blocked_admin": 0,
                "blocked_domain": 0, "quarantined": 0, "inactive": 0, "not_started": 0,
                "password_required": 6}}
  ```
  - `by_*` breakdowns count human clicks only. `totals` counts every class.
- `GET /api/analytics/campaigns/{id}?from&to&tz&interval`: the same totals and series, plus `top_links: [{link_id, code, human}]`.
- `GET /api/analytics/overview?from&to&tz`: `{totals, top_links, campaigns: [{id, name, human}], alerts_open}`.
- `GET /api/analytics/live?link_id=` (Server-Sent Events): `event: click` with `{link_id, traffic_class, ts}`. Clients may poll every 5 s instead.

## Trust [3]

- `GET /api/links/{id}/trust` →
  ```json
  {"state": "ok", "last_checked_at": "...", "next_check_at": "...",
   "checks": [{"checked_at": "...", "http_status": 200, "final_url": "...", "final_domain": "shop.example",
               "hops": 1, "verdict": "ok", "sources": ["safe_browsing", "urlhaus"]}]}
  ```
- `POST /api/links/{id}/rescan` → `202 {"queued": true}` (rate-limited)
- `GET /api/alerts?status=open` → `{"items": [{"id", "link_id", "kind": "spike|near_cap|expiring|dead_link|quarantined", "message", "created_at"}]}`

## Link-in-bio [4]

- `GET` / `POST /api/bio-pages`, `PATCH /api/bio-pages/{id}`
  - `{id, slug, title, link_ids: [...]}`

## Admin [4], role `admin` only

- `GET /api/admin/links?status=&trust_state=&q=`: all users' links.
- `POST /api/admin/links/{id}/disable` `{reason}` → `{link}`. Takes effect on the next visit, and an audit entry is written.
- `POST /api/admin/links/{id}/enable` → `{link}`
- `GET /api/admin/abuse`: links that are `quarantined`, `changed` or `degraded`, plus admin-disabled links.
- `GET` / `POST /api/admin/blocklist` `{domain, reason}`, `DELETE /api/admin/blocklist/{domain}`
  - A domain also blocks its subdomains. Matching links are refused from the next visit.
- `GET /api/admin/audit?limit&cursor` → `{items: [{id, actor_id, action, entity_type, entity_id, detail, ts}]}`
- `GET /api/admin/users`
- `GET /api/admin/analytics/overview`: the overview across the whole platform.

## As built: details API.md left open

These are the choices the implementation made; the frontend relies on them.

**Errors.** `401 UNAUTHENTICATED` when the session is missing or the request's Origin is not ours (`403 ORIGIN_NOT_ALLOWED` on the auth endpoints themselves); `400 BAD_REQUEST` for unknown JSON fields, malformed JSON or bad query parameters; `429 RATE_LIMITED` with `Retry-After`.

**Auth.** Signup validation is `422 {errors, warnings}` with `EMAIL_INVALID`, `PASSWORD_TOO_SHORT` (under 8), `PASSWORD_TOO_LONG` (over 72 bytes, bcrypt's limit) or `NAME_TOO_LONG`; a taken email is `409 EMAIL_TAKEN`. Login failures are `401 BAD_CREDENTIALS`, the same for an unknown email and a wrong password. `ADMIN_EMAIL`/`ADMIN_PASSWORD` create or promote the admin at startup.

**Links.**
- `409` on an alias lost at insert carries the validation shape (`errors: [{field: "alias", code: "ALIAS_TAKEN"}]`).
- Validate also looks the alias up early. `THREAT_MATCH` blocks validate and create. `DESTINATION_UNREACHABLE` and `LONG_REDIRECT_CHAIN` (3 or more hops) are warnings on validate only.
- Target errors use the fields `targets[i].device` and `targets[i].destination`.
- PATCH re-checks only the fields it sends and can't change the alias (`422 ALIAS_INVALID`). An owner can't pause, resume or archive an admin-disabled link (`409 LINK_DISABLED`).
- Admins see every workspace's links.

**Campaigns, tags, folders.**
- Single resources are wrapped: `{campaign}`, `{tag}`, `{folder}`. Lists are pages. `GET` exists for one tag or folder too.
- Codes: `NAME_REQUIRED`, `NAME_TOO_LONG`, `NAME_TAKEN` (409; names are unique per workspace in any case), `COLOR_INVALID` (`#rrggbb`), `FOLDER_CYCLE`.
- Deleting a label keeps its links.

**Analytics.**
- **How visits are counted:**
  - A class's count in `totals` is its redirected visits.
  - `refused` counts every refused visit of any class, so the totals add up to all visits.
  - `series` reports `human` and `bots` (redirected) and `refused`.
  - Breakdowns count redirected human clicks. A missing device shows as `unknown`, a missing browser or OS as `Other`. Referrers, countries, browsers and OS are the top 10.
  - `unique_visitor_days` is distinct (local day, visitor hash) pairs among human visits.
- **Series:** zero-filled; hourly covers up to 31 days, daily up to 400.
- **Overview:** `/api/analytics/overview` is the caller's own workspace. `alerts_open` is `null` when alerts are not available.
- **Live stream:** `/api/analytics/live` sends `: connected`, `event: click` and a 15 s heartbeat, and answers `503 LIVE_UNAVAILABLE` without a broker. The web app polls every 5 s instead.

**QR and bio pages.**
- QR images are `private, max-age=86400`. A bad `size` (allowed: 128–1024) or `format` is a 400.
- `GET /api/bio-pages/{id}` exists. Bio codes: `SLUG_INVALID` (alias rules), `SLUG_TAKEN` (409), `TITLE_TOO_LONG`, `TOO_MANY_LINKS` (over 50), `DUPLICATE_LINK`, `UNKNOWN_LINK`. The public page lists only links a visitor could open now.

**Imports.**
- **Whole-file errors:** `413 FILE_TOO_LARGE`, `413 TOO_MANY_ROWS`, `400 BAD_CSV`, and `400 BAD_REQUEST` without a multipart `file` field.
- **Rows:** row numbers are file lines (header = 1). Missing tags are created at commit.
- **Commit:** `200 {created, skipped}`; a second commit is `409 ALREADY_COMMITTED`.

**Trust and alerts.**
- `GET /api/links/{id}/trust` checks also carry `detail` and `error`.
- Both trust endpoints return 404 for anyone else's link.
- `GET /api/alerts?status=open|all` (default `open`); there is one open alert per link and kind, and alerts clear when their condition does.

**Admin.**
- Disabling needs a reason (`422 REASON_REQUIRED`).
- Enabling a quarantined link also releases it (trust back to `ok`, audit `link.released`).
- `POST /api/admin/blocklist` is an upsert (`201 {blocked_domain}`, `422 DOMAIN_INVALID`). Unblocking an unknown domain is a 404. A blocklist change applies from the next visit.
- Users and audit take `limit` and `cursor`.

## Rate limits

| What | Limit |
|---|---|
| Link creation | 30 per minute per user |
| Imports | 5 per hour per user |
| Login | 10 per 15 minutes per IP |
| Password attempts | 10 per 15 minutes per IP + link |
| Re-scan | 10 per hour per user |
