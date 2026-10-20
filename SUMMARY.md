# SUMMARY.md: PathMaxing, the accountable short link

**Project:** PathMaxing (repository `NotQualified-PathMaxing`), built for the "LinkPulse: URL Shortener & Analytics Platform" problem statement.

**Team:** Not Qualified
- Nilay Joshi, Mayank Joshi, Karan Pandey (B.Tech, 2nd year)
- Mansi Pandey, Manas Singh Bisht (B.Tech, 3rd year)

**Event:** Tech Geeks hackathon, problem statement "LinkPulse: URL Shortener & Analytics Platform".
- Round 1: ideation PPT (done).
- Round 2: build and deploy a working prototype, then give a 5-minute demo of two scenarios.

## 1. The brief, in one paragraph

Build a platform that manages the full link lifecycle: creation, redirection, tracking, analytics, and expiry or takedown. The Link record is the hub that campaigns, redirect rules, click events and QR codes hang off.

Required:
- custom or auto-generated aliases, expiry, click caps, password protection and device targeting;
- click tracking (time, referrer, device, browser, location) with bot filtering;
- workspaces, folders, tags and campaigns;
- QR codes, CSV bulk import and link-in-bio pages;
- a live dashboard by period and campaign;
- roles: visitor, user (own links only), admin (everything, takedowns, domain blocklist);
- rate limiting on link creation.

Redirects must stay fast while clicks are recorded reliably. Duplicate aliases and invalid URLs must surface before publishing. Expired and disabled links must stay traceable in reports. Judges score unified workflow, business logic (code generation, rule order, click limits, bot filtering), architecture (role-based access, async processing, data relationships) and accurate behaviour. Minimum bar: a hosted link you can create, follow and see clicks for.

## 2. Problem we are solving (evidence)

Short links and QR codes are checked once, when they are created, and then trusted forever.

- **Destinations decay.** 38% of webpages that existed in 2013 were no longer accessible a decade later [1].
- **QR codes are attacked at scale.** Mimecast detected 716,306 unique malicious QR codes in Q3 2025 alone [2].
- **India's fraud losses are large.** Citizens lost ₹22,845.73 crore to cyber fraud in 2024, about 3× the 2023 figure [3].
- **Indian SMS rules leave a gap.** Since 1 Oct 2024, TRAI requires URLs in commercial SMS to be whitelisted [4]. Only the fixed part of a URL is whitelisted [5], so whoever controls the shortener decides where a "trusted" link finally goes.
- **Shorteners only checked at submission.** A two-year study of 24.9M short URLs (2010–12) found popular shorteners verified URLs only when they were submitted [6]. This is old evidence; today some services do check when a link is clicked.

## 3. Our solution

Everything the brief requires, plus three differentiators.

1. **Destination Trust.**
   - Every destination is re-checked on a schedule after publishing, not just once.
   - Dead or changed destinations alert the owner. A threat-feed hit quarantines the link automatically: the cache is cleared, visitors see a warning page, and the link appears in the admin abuse queue.
   - States: `ok`, `degraded`, `changed`, `quarantined`.
2. **Honest Clicks.**
   - Every click is labelled `human`, `email_scanner` (probable), `link_preview` or `crawler`, with a reason code.
   - Only humans use up click caps or one-time links.
   - Email security products scan links before delivery and when clicked [7]. Other shorteners report human vs bot totals [8]; we also show *why* each click got its label.
3. **India Mode (nice-to-have).** TRAI/DLT-style `domain/HEADER/slug` links, where one workspace owns each header. Such links already exist commercially [9]. Our addition is to keep re-verifying what sits behind each whitelisted header.

## 4. Feature scope

**Must-have (the demo depends on these):**
- Two-step creation wizard: validate first (URL rules, alias free and not reserved, blocklist, threat pre-check), then options (expiry, cap, password, device targets, campaign, tags), then publish.
- Redirect engine with the fixed rule order, a 302, async click logging, and a cache with safe invalidation.
- Accounts and roles. Users see only their own links (404 for anyone else's); admins see everything.
- Campaigns, tags, folders, one workspace per user.
- Analytics by period (timezone-aware) and campaign: totals, human vs bot, uniques, devices, browsers, referrers, countries, link vs QR source, top links, outcomes.
- QR codes (PNG and SVG) that encode `/{code}?s=qr`, so scans are tracked separately.
- CSV bulk import: upload, preview with per-row error codes, commit only the valid rows.
- Admin: disable or enable links, domain blocklist (which also blocks subdomains), audit log, abuse queue, platform analytics.
- Trust scanner, quarantine and honest-clicks classifier.
- Rate limits on creation, imports, login and password attempts.

**Should-have:** live dashboard updates (SSE or 5-second polling), alerts (traffic spike, link near its cap, expiring soon, dead link, quarantined), public transparency page `/p/{code}`, link-in-bio pages.

**Could-have:** India mode, a confirmed-human interstitial, GeoLite2 ASN lookup to spot data-centre traffic.

**Roadmap (slide only):** custom domains, public API with keys, webhooks, Google Web Risk (the commercial version of Safe Browsing), Redis for multi-instance scaling, ClickHouse.

## 5. Architecture

```
Visitor ─GET /{code}─▶ [cache] → [rule engine] → 302 ─▶ destination
                           │ (async) click event
                           ▼
                     [click queue] → [batch writer] ─▶ PostgreSQL
Owner/Admin ─/api/*─▶ [management API: auth, roles, rate limits] ─▶ PostgreSQL
[trust scanner] ─▶ Safe Browsing + URLhaus ─▶ trust state, quarantine, cache clear
[web/ React SPA] served at /app/ by the same Go binary
```

**Main tables:**
- people and organisation: `users`, `sessions`, `workspaces`, `folders`, `campaigns`, `tags`, `link_tags`;
- links: `links` (status, trust_state, last_checked_at, cap_used, soft delete), `redirect_rules` (device targets), `destination_checks`;
- traffic: `click_events` (outcome, traffic_class, class_reason, source, device, browser, os, referrer_host, country, visitor_hash), `click_rollups` (15-minute buckets);
- governance and bulk work: `blocked_domains`, `audit_log`, `bulk_import_jobs`, `bulk_import_rows`, `bio_pages`.

## 6. Key decisions (judges will ask)

- **302, not 301.** Browsers cache a 301. That hides repeat clicks and keeps sending people to a link after a takedown.
- **Rule order.** Blocked → Inactive → Schedule → Password → Click cap. The click cap is last because it is the only step that writes data, so a refused visit never uses up a click.
- **Atomic click cap.** One conditional `UPDATE`. A 1,000-concurrent-request race test must allow exactly the cap.
- **Bots never use up clicks.** Chat apps fetch a link as soon as it is pasted, which would otherwise burn a one-time link.
- **Case-insensitive, never-reused codes, with insert-and-retry.** Two people creating `Sale` and `sale` at the same moment can't both succeed.
- **Async clicks.** Redirects never wait for the database. Trade-off: buffered events are lost if the process crashes. The upgrade is Redis Streams.
- **Privacy.** No raw IPs are stored, and the visitor hash rotates daily. So "unique visitors" means unique visitors per day, added up.
- **15-minute rollups, not hourly.** India is UTC+05:30, so an IST day starts half-way through a UTC hour. Hourly UTC buckets can't be regrouped into IST days exactly; 15-minute buckets can, for every real timezone.
- **Honest heuristics.** Email scanners publish no signatures, so that label says "probable". We measure precision and recall on a labelled list of user agents.

## 7. Demo plan (5 minutes)

1. **Create → analytics.**
   - Wizard flags a taken alias (`Sale` vs `sale`) and an http warning; publish.
   - Download the QR code and scan it with a phone; the live counter moves.
   - Paste the link into WhatsApp. The preview shows up as "link preview", not a human click, and the cap doesn't move.
2. **Bulk import → takedown.**
   - CSV with planted errors; the preview shows the reasons and we import only the valid rows.
   - Change a demo destination to a blocklisted domain and force a re-scan. The link is quarantined, the phone shows the warning page, and `curl -I` returns 451.
   - The audit log shows the action, and the link's analytics are still there.
3. **Proof:** a short k6 run showing p95 redirect latency, and the cap race test allowing exactly the cap.

## 8. Success metrics (targets to measure, not results)

- p95 redirect latency under k6 load with the workers running.
- 0 click-cap overshoot in a 1,000-concurrent-request race test.
- Under 60 s from a malicious verdict to quarantine on a forced re-scan.
- 100% of clicks carry a traffic class and a reason code.
- 100% of invalid CSV rows rejected, each with a reason.

## 9. Claims to avoid in the pitch

- Saying "shorteners never re-check links": the evidence is from 2010–12.
- Quoting "51% of traffic is bots": that figure covers all web traffic, not short-link clicks.
- Saying "all goo.gl links died": Google kept actively used links.
- Saying "DPDP is fully in force": its core duties apply from May 2027.
- Calling scanner detection "accurate": it is heuristic.

## References

1. Pew Research Center (2024). When Online Content Disappears. https://www.pewresearch.org/data-labs/2024/05/17/when-online-content-disappears/
2. APWG (Dec 2025). Phishing Activity Trends Report, Q3 2025. https://docs.apwg.org/reports/apwg_trends_report_q3_2025.pdf
3. PTI via The Week (22 Jul 2025). MHA written reply in Lok Sabha on 2024 cyber-fraud losses. https://www.theweek.in/wire-updates/business/2025/07/22/del23-lsq-cyber-fraud.html
4. TRAI (30 Aug 2024). Direction on whitelisting of URLs/APKs/OTT links in SMS. https://trai.gov.in/sites/default/files/2024-09/Direction_30082024_0.pdf
5. Argus Partners (2024). Summary of the TRAI direction. https://www.argus-p.com/updates/updates/trai-issues-direction-to-curb-the-misuse-of-headers-and-content-templates/
6. Maggi F. et al. (2013). Two Years of Short URLs Internet Measurement. https://seclab.cs.ucsb.edu/publications/maggi2013two_years/
7. Microsoft Learn. Safe Links in Microsoft Defender for Office 365. https://learn.microsoft.com/en-us/defender-office-365/safe-links-about
8. Rebrandly Help. Why don't my numbers match other analytics tools? https://support.rebrandly.com/en/articles/755638-why-don-t-my-rebrandly-numbers-match-other-analytics-tools
9. Cuttly. TRAI SMS compliance URL shortener. https://cutt.ly/resources/tools/trai-url-shortener
10. Google. Safe Browsing API v4 usage restrictions. https://developers.google.com/safe-browsing/v4/usage-limits
11. abuse.ch. URLhaus API. https://urlhaus.abuse.ch/api/
12. MaxMind. GeoLite2 free geolocation data. https://dev.maxmind.com/geoip/geoip2/geolite2/
13. monperrus/crawler-user-agents. https://github.com/monperrus/crawler-user-agents
