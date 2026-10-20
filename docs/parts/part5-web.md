# Part 5: Frontend and deployment (owner: Karan Pandey)

This guide explains the web app in `web/` and the deployment files in `deploy/`, so you can walk a judge through them in your own words.

## 1. What Part 5 does and how it fits

- Part 5 is the screen people use: log in, create links, watch clicks arrive, import CSVs, manage campaigns and bio pages, and (for admins) take down bad links.
- It is a React single-page app. `npm run build` turns it into plain static files in `web/dist`.
- The Go server serves those files itself under `/app/` when `WEB_DIST_DIR` points at them. There is no separate frontend server in production.
- Because the app and the API share one origin, the browser sends the `lp_session` cookie automatically and the server's Origin check passes. No CORS and no tokens in JavaScript.
- The app only ever talks to `/api/*`, exactly as written in `API.md`. If a response shape changes, `API.md`, `web/src/api/types.ts` and `web/src/api/client.ts` change in the same commit.
- Public pages that are not part of the app (`/{code}`, `/p/{code}`, `/bio/{slug}`) are rendered by the Go server. The app only links to them.

### One request, end to end (pressing "Publish link")

1. `NewLinkPage` calls `api.links.create(input)`.
2. `client.ts` sends `POST /api/links` as JSON. The browser adds the session cookie and our Origin.
3. The server answers `201 {link, warnings}`, or `422 {errors, warnings}`, `409` or `429`.
4. On success the page shows the short URL and the QR image (`/api/links/{id}/qr?format=svg`), and tells TanStack Query that the cached links and analytics are out of date, so the list and the dashboard refetch.
5. On an error, `ApiError` carries the field errors back, and the wizard returns to the step that owns the field.

## 2. Files and main pieces

Under `web/`:

- `index.html`: the page shell; loads the three Google Fonts with `display=swap`.
- `vite.config.ts`: build settings (base `/app/`, `/api` proxy for development, chunk splitting) and the test setup.
- `src/main.tsx`: starts React with the TanStack Query client and the router (basename `/app`).
- `src/App.tsx`: every route; the admin routes are wrapped in `RequireAuth adminOnly`.
- `src/api/types.ts`: TypeScript types for every request and response in `API.md`, written by hand.
- `src/api/client.ts`: one small `fetch` wrapper plus the `api` object (`api.links.create`, `api.analytics.overview`, …). Turns error responses into an `ApiError` that keeps the status, the code, 422 field errors and `Retry-After`.
- `src/api/hooks.ts`: shared queries for the campaign and tag pickers.
- `src/auth/useAuth.ts`, `src/auth/RequireAuth.tsx`: who is signed in, and the guard that sends visitors to the login page.
- `src/pages/DashboardPage.tsx`: the live overview (KPIs, human vs bot, top links, campaigns, open alerts).
- `src/pages/LinksPage.tsx`: the links list with filters and "Load more" paging.
- `src/pages/NewLinkPage.tsx`: the creation wizard (validate → options → review → publish → QR).
- `src/pages/LinkDetailsPage.tsx`: one link's facts, actions, QR, analytics and trust timeline.
- `src/pages/CampaignsPage.tsx`, `ImportPage.tsx`, `BioPagesPage.tsx`: campaigns and tags, CSV import, link-in-bio pages.
- `src/pages/AdminPages.tsx`: platform overview, abuse queue, all links, blocklist, users, audit log.
- `src/components/`: shared pieces: `Field` and `SelectField` (labelled inputs), `States` (loading, empty, error), `Badges` (status and trust pills), `charts.tsx` (Recharts bar charts with a table view), `Overview.tsx` (KPI tiles), `PeriodPicker.tsx`, `CopyButton.tsx`, `Layout.tsx` (top bar and nav).
- `src/lib/format.ts`: IST date formatting, Indian number grouping, the 24 h / 7 d / 30 d windows, and IST ↔ UTC conversion for the date inputs.
- `src/lib/issues.ts`: plain-English explanations for every validation code (`ALIAS_TAKEN`, `HTTP_NOT_HTTPS`, …).
- `src/styles.css` (+ `pages/bulk.css`, `pages/admin.css`): the PathMaxing design system, based on the TemplateMo 627 "HyperLuv" template: ink, bone, lime, orange and pink; Anton, Space Grotesk and VT323 fonts; 3px borders and hard shadows.
- `src/**/*.test.ts(x)`, `src/test/`: Vitest + Testing Library tests; `mockApi` fakes `fetch`, so no test needs a real server.
  - `api/client.test.ts`: the client (JSON, cookie, error envelope, 422, Retry-After).
  - `pages/auth.test.tsx`: login, signup, logout, rate limiting, server unreachable.
  - `pages/dashboard.test.tsx`: KPIs come from the API, the tz and window are sent, empty and error states.
  - `pages/links.test.tsx`: the wizard blocks on errors, sends UTC times, handles 422/409/429; the list sends its filters.
  - `pages/linkDetails.test.tsx`: analytics and trust come from the API, re-scan, pause, delete with confirm, 404.
  - `pages/bulk.test.tsx`: CSV preview with planted errors, 413, campaigns, bio pages.
  - `pages/admin.test.tsx`: non-admins are kept out, disable needs a reason, blocklist, audit paging.

### Screens and the endpoints they use

| Route | Screen | Main endpoints |
|---|---|---|
| `/login`, `/signup` | Log in, create account | `/api/auth/*` |
| `/` | Overview dashboard | `GET /api/analytics/overview`, `GET /api/alerts?status=open` |
| `/links` | Links list | `GET /api/links`, `GET /api/campaigns` |
| `/links/new` | Creation wizard | `POST /api/links/validate`, `POST /api/links` |
| `/links/:id` | Link details | `GET /api/links/{id}`, `GET /api/analytics/links/{id}`, `GET /api/links/{id}/trust`, `POST …/rescan`, pause/resume/archive/delete |
| `/campaigns`, `/campaigns/:id` | Campaigns and tags | `/api/campaigns`, `/api/tags`, `GET /api/analytics/campaigns/{id}` |
| `/import` | Bulk CSV import | `POST /api/imports`, `POST /api/imports/{id}/commit` |
| `/bio` | Link-in-bio pages | `/api/bio-pages` |
| `/admin/*` | Admin area | `/api/admin/*` |

Under `deploy/`:

- `Dockerfile.full`: three stages: Node 20 builds the web app, Go 1.24 builds a static server binary, a small Alpine image runs both as a non-root user.
- `Dockerfile.full.dockerignore`: keeps `.env`, `node_modules` and build output out of the Docker build.
- `render.yaml`: a Render Blueprint for the Go server (one free Docker web service with a `/healthz` health check).
- `web/vercel.json`: the Vercel project for the web app. It serves the built app under `/app/` and rewrites everything else (`/api`, short links, `/p`, `/bio`, `/healthz`) to the Render service.

## 3. How to run and test

You need Node 20 or newer.

```sh
cd web
npm ci            # installs the exact versions from package-lock.json
npm run dev       # http://localhost:5173/app/
npm run build     # type-check, then write web/dist
npm test          # run every test once
```

- `npm run dev` proxies `/api` to the Go server on `http://localhost:8080`. Start it first (`docker compose up -d db`, then `go run ./cmd/server`).
- Because the dev server is a different origin, add `ALLOWED_ORIGINS=http://localhost:5173` to your local environment, or the server rejects state-changing requests.
- To try the production setup locally: `npm run build`, then start the server with `WEB_DIST_DIR=web/dist` and open `http://localhost:8080/app/`.
- To run one test file: `npx vitest run src/pages/links.test.tsx`.

### When something goes wrong

- **Saving anything fails in `npm run dev`, but reading works.** `ALLOWED_ORIGINS` doesn't include `http://localhost:5173`, so the server refuses state-changing requests from the dev server.
- **The server won't start and mentions `WEB_DIST_DIR`.** That folder has no `index.html`; run `npm run build` first (the Docker image does this for you).
- **`/healthz` lists a failing check.** Usually `DATABASE_URL` is wrong. At startup the server waits up to 30 seconds for the database, because a sleeping Neon database needs a moment to wake up.
- **The fonts look plain.** Google Fonts didn't load (offline, or blocked); the system fallbacks are working as designed.

### Deploy: Vercel (web app) + Render (Go server) + Neon (database)

People only ever see the Vercel address. Vercel serves the React app and
forwards every other path to the Go server on Render, so to the browser it is
one origin: cookies, the Origin check and short links all use the Vercel
domain. The Go server can't run on Vercel itself, because it keeps background
workers (the click writer, the trust scanner, alerts) running between requests.

1. **Database.** Create a project at neon.tech. Copy its connection string; it must end with `sslmode=require`.
2. **Code.** Push the repository to GitHub (all commits up to C40).
3. **Render (backend).** New → Blueprint, pick the repository, and set the Blueprint file path to `deploy/render.yaml`. **Keep the service name `pathmaxing`**: `web/vercel.json` sends requests to `https://pathmaxing.onrender.com`. Fill in the variables marked `sync: false`:
   - `DATABASE_URL`: the Neon connection string.
   - `BASE_URL`: the Vercel address, for example `https://pathmaxing.vercel.app` (use the real one from step 4).
   - `ADMIN_EMAIL` and `ADMIN_PASSWORD` (8+ characters): the admin account created at startup.
   - `SAFE_BROWSING_API_KEY` and `URLHAUS_AUTH_KEY` are optional; without them those checks are skipped and logged.
   - `SECRET_KEY` is generated by Render; `APP_ENV=production` and `TRUSTED_PROXY_HOPS=2` (Vercel, then Render) are already set.
   The first deploy builds `deploy/Dockerfile.full`, connects to Neon and applies the migrations. Check `https://pathmaxing.onrender.com/healthz` says `ok`.
4. **Vercel (frontend).** Add New → Project, import the repository, and set **Root Directory** to `web`. `web/vercel.json` already sets the install, build and output settings. Name the project `pathmaxing` so the address is `https://pathmaxing.vercel.app`, and deploy.
5. **Connect them.** If Vercel's address differs from what you put in `BASE_URL`, change `BASE_URL` on Render and redeploy it. If Render's address is not `https://pathmaxing.onrender.com`, change the last rewrite in `web/vercel.json`; that edit is a new commit.
6. **Check.** Open `https://pathmaxing.vercel.app/healthz` (proxied to Render, `ok`), then `/app/`, sign up or log in as the admin, create a link and open its short URL.
7. **Demo data.** From a laptop: `go run ./cmd/seed -base https://pathmaxing.vercel.app -password <demo password> -visits 400`. It sends real requests through the API and the redirect endpoint.
8. **Before the demo.** Render's free instance sleeps when idle, and the first request afterwards can take up to a minute. Open the app a few minutes before presenting.

## 4. Key design decisions, and why

- **TanStack Query holds all server state.** Every screen asks the API through `useQuery` / `useMutation`; there is no Redux or global store. Caching, retries, refetching and "is it loading?" come for free, and after a change we invalidate the affected queries instead of syncing state by hand.
- **A typed client written by hand from `API.md`.** It is about 200 lines, readable, and has no code generator to set up. TypeScript then catches a wrong field name at build time. The cost: it must be updated whenever `API.md` changes.
- **No fake numbers.** Every number on screen comes from an API response.
  - When the API returns nothing, the screen says so ("No clicks in this period yet.").
  - When a request fails, it shows the error and a "Try again" button.
  - Shares with a zero total show "—" instead of "0%".
  - Tests check that the numbers on screen are the ones the mocked API returned.
- **"Live" means polling every 5 seconds.** The dashboard and analytics use `refetchInterval: 5000`. The time window (for example the last 7 days) is computed when each request is sent, so it slides forward. The API also offers Server-Sent Events, but polling is simpler and survives proxies and sleeping laptops. The "Live · updated" label shows when data last arrived.
- **India time everywhere.** All times are shown in Asia/Kolkata with `Intl.DateTimeFormat`, and analytics requests send `tz=Asia/Kolkata`. Date inputs are typed in IST and sent as UTC. IST is always UTC+05:30 (India has no daylight saving), so the conversion is exact.
- **The wizard validates before saving.** Step 1 calls `POST /api/links/validate` and you can only continue with no errors. Errors from publishing (422, 409 alias taken, 429 with Retry-After) are sent back to the step that owns the field.
- **Accessibility.**
  - Every input has a label; errors are tied to their field with `aria-describedby` and marked `aria-invalid`.
  - Every interactive element shows a 3px focus ring.
  - There is a "Skip to content" link, and the wizard moves keyboard focus to each new step's heading.
  - The period picker is a set of radio buttons, so arrow keys work.
  - Each chart has a "Show as table" view; with "reduce motion" turned on, buttons don't move and the live dot doesn't blink.
- **Contrast, calculated with the WCAG formula:**
  - Ink on bone is 17.1:1. Muted text `#4a463d` is 8.2:1 on bone and 9.4:1 on white.
  - In dark mode, bone on `#161616` is 15.7:1 and muted `#bdb7a6` is 9.0:1.
  - Ink on lime is 15.7:1, on orange 6.3:1, on pink 5.8:1.
  - Error text is `#b0003a` (6.3:1 on bone) in light mode and pink (5.4:1 on `#161616`) in dark mode.
  - Lime on white is only 1.2:1 and orange on bone 2.7:1, so lime and orange are never used as text on light backgrounds.
- **Chart colours.** Human is lime, bots orange, refused ink (bone in dark mode). They were checked with a colour-blindness simulator. Lime is pale on white, so every bar has a 2px outline and every chart also has a table.
- **Chunk splitting.** Recharts and the React libraries are built into their own files (`charts`, `vendor`). They change less often than our code, so browsers keep them cached across deploys.
- **Fonts with fallbacks.** If Google Fonts is slow or blocked, the page renders at once in system fonts (`display=swap`) and switches when the fonts arrive. Fallbacks: Arial Narrow/Impact for Anton, the system UI font for Space Grotesk, Consolas/monospace for VT323.

## 5. Questions judges are likely to ask

**Why not Next.js?**
We don't need server rendering: every app page is behind a login. A static Vite build is served by the same Go binary, so there is one deploy, one origin, no CORS, and cookies just work. Next.js would add a second server to run and secure.

**How do live updates work?**
TanStack Query re-asks the API every 5 seconds on the dashboard and analytics screens, and the "updated" time on screen shows the last answer. The backend also has an SSE stream; we chose polling because it is simpler and more robust for a demo.

**How do you make sure you never show fake data?**
There are no hard-coded metrics anywhere in the app. If a request returns nothing we show an empty state, and if it fails we show an error with a retry. The tests feed the screens known API responses and check that exactly those numbers appear. The demo data itself comes from `cmd/seed` sending real requests.

**How does deployment work?**
The web app is on Vercel and the Go server on Render. `web/vercel.json` serves the app under `/app/` and rewrites the API, short links, `/p` and `/bio` to Render, so the browser sees one origin. `deploy/Dockerfile.full` builds the server with Go 1.24 (and the web app too, so Render can serve it as a fallback) into a small Alpine image that runs as a non-root user. Render builds it from `deploy/render.yaml`, keeps secrets in its dashboard and checks `/healthz`. The database is Neon Postgres, and migrations run automatically when the server starts.

**What happens on a phone?**
Layouts use CSS grids that collapse to one column. The nav moves to its own row and scrolls sideways, wide tables scroll inside their card, and main buttons are 44px tall (small ones 36px). We checked the screens at about 500px wide in headless Edge (the narrowest width it supports) in light and dark mode. Try it on a real phone before the demo.
