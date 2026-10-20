package analytics

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pathmaxing/internal/domain"
)

// Store runs the analytics queries. All of the package's SQL is in this file.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a store over pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type scopeKind int

const (
	scopeLink scopeKind = iota
	scopeCampaign
	scopeWorkspace
	scopeAll
)

// Scope is the set of links a report covers. Callers check access first.
type Scope struct {
	kind scopeKind
	id   int64
}

// ForLink covers one link.
func ForLink(id int64) Scope { return Scope{scopeLink, id} }

// ForCampaign covers the links currently in a campaign.
func ForCampaign(id int64) Scope { return Scope{scopeCampaign, id} }

// ForWorkspace covers every link of a workspace.
func ForWorkspace(id int64) Scope { return Scope{scopeWorkspace, id} }

// ForAll covers the whole platform, for admins.
func ForAll() Scope { return Scope{kind: scopeAll} }

// args collects query parameters and hands out their placeholders.
type args []any

func (a *args) add(v any) string {
	*a = append(*a, v)
	return "$" + strconv.Itoa(len(*a))
}

// cond is the scope as a condition on click events aliased e.
func (sc Scope) cond(a *args) string {
	switch sc.kind {
	case scopeLink:
		return "e.link_id = " + a.add(sc.id)
	case scopeCampaign:
		return "e.link_id IN (SELECT id FROM links WHERE campaign_id = " + a.add(sc.id) + ")"
	case scopeWorkspace:
		return "e.workspace_id = " + a.add(sc.id)
	default:
		return "TRUE"
	}
}

// events is the FROM and WHERE for click events in scope and period, with
// optional joins.
func events(a *args, sc Scope, from, to time.Time, joins ...string) string {
	sql := " FROM click_events e"
	for _, j := range joins {
		sql += " " + j
	}
	return sql + " WHERE " + sc.cond(a) + " AND e.ts >= " + a.add(from.UTC()) + " AND e.ts < " + a.add(to.UTC())
}

const humanClick = " AND e.traffic_class = 'human' AND e.outcome = 'redirected'"

// countRow is one group of visits: a bucket (wall-clock start in the query's
// time zone), a class, an outcome and a source. Totals, the series, outcomes
// and sources are all sums over these rows.
type countRow struct {
	bucket                 time.Time
	class, outcome, source string
	n                      int64
}

// rollupStep is the width of a click_rollups bucket.
const rollupStep = 15 * time.Minute

// counts reads the whole 15-minute buckets inside the period from the
// rollups and only the ragged edges (a period rarely starts on a quarter
// hour) from the raw events, so the result is exact for any from and to.
func (s *Store) counts(ctx context.Context, sc Scope, q Query) ([]countRow, error) {
	lo := q.From.UTC().Truncate(rollupStep)
	if lo.Before(q.From) {
		lo = lo.Add(rollupStep)
	}
	hi := q.To.UTC().Truncate(rollupStep)
	if !lo.Before(hi) {
		return s.rawCounts(ctx, sc, q, q.From, q.To)
	}
	out, err := s.rollupCounts(ctx, sc, q, lo, hi)
	if err != nil {
		return nil, err
	}
	for _, edge := range [][2]time.Time{{q.From, lo}, {hi, q.To}} {
		if edge[0].Before(edge[1]) {
			rows, err := s.rawCounts(ctx, sc, q, edge[0], edge[1])
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
	}
	return out, nil
}

// rawCounts groups click events in [from, to).
func (s *Store) rawCounts(ctx context.Context, sc Scope, q Query, from, to time.Time) ([]countRow, error) {
	var a args
	interval, tz := a.add(q.Interval), a.add(q.TZ)
	sql := `SELECT date_trunc(` + interval + `::text, e.ts AT TIME ZONE ` + tz + `::text), e.traffic_class, e.outcome, e.source, count(*)` +
		events(&a, sc, from, to) + ` GROUP BY 1, 2, 3, 4`
	return s.scanCounts(ctx, sql, a)
}

// rollupCounts regroups the 15-minute rollups in [from, to), which must be
// aligned to rollupStep, into the query's buckets.
func (s *Store) rollupCounts(ctx context.Context, sc Scope, q Query, from, to time.Time) ([]countRow, error) {
	var a args
	interval, tz := a.add(q.Interval), a.add(q.TZ)
	var cond string
	switch sc.kind {
	case scopeLink:
		cond = "r.link_id = " + a.add(sc.id)
	case scopeCampaign:
		cond = "r.link_id IN (SELECT id FROM links WHERE campaign_id = " + a.add(sc.id) + ")"
	case scopeWorkspace:
		cond = "r.link_id IN (SELECT id FROM links WHERE workspace_id = " + a.add(sc.id) + ")"
	default:
		cond = "TRUE"
	}
	sql := `SELECT date_trunc(` + interval + `::text, r.bucket AT TIME ZONE ` + tz + `::text), r.traffic_class, r.outcome, r.source, sum(r.n)::bigint
		FROM click_rollups r WHERE ` + cond + ` AND r.bucket >= ` + a.add(from) + ` AND r.bucket < ` + a.add(to) + `
		GROUP BY 1, 2, 3, 4`
	return s.scanCounts(ctx, sql, a)
}

func (s *Store) scanCounts(ctx context.Context, sql string, a args) ([]countRow, error) {
	rows, err := s.pool.Query(ctx, sql, a...)
	if err != nil {
		return nil, fmt.Errorf("analytics: counts: %w", err)
	}
	defer rows.Close()
	var out []countRow
	for rows.Next() {
		var r countRow
		if err := rows.Scan(&r.bucket, &r.class, &r.outcome, &r.source, &r.n); err != nil {
			return nil, fmt.Errorf("analytics: counts: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// summary is everything derived from countRows.
type summary struct {
	totals   Totals
	series   []Point
	outcomes map[string]int64
	bySource map[string]int64
}

func summarize(q Query, rows []countRow) summary {
	sm := summary{outcomes: zeroOutcomes(), bySource: map[string]int64{}}
	points := map[string]Point{}
	for _, r := range rows {
		sm.totals.add(r.class, r.outcome, r.n)
		sm.outcomes[r.outcome] += r.n
		key := wallKey(r.bucket)
		p := points[key]
		switch {
		case r.outcome != string(domain.OutcomeRedirected):
			p.Refused += r.n
		case r.class == string(domain.ClassHuman):
			p.Human += r.n
			sm.bySource[r.source] += r.n
		default:
			p.Bots += r.n
		}
		points[key] = p
	}
	sm.series = fillSeries(q, points)
	return sm
}

func (s *Store) summary(ctx context.Context, sc Scope, q Query) (summary, error) {
	rows, err := s.counts(ctx, sc, q)
	if err != nil {
		return summary{}, err
	}
	sm := summarize(q, rows)
	sm.totals.UniqueVisitorDays, err = s.uniqueVisitorDays(ctx, sc, q)
	return sm, err
}

func (s *Store) uniqueVisitorDays(ctx context.Context, sc Scope, q Query) (int64, error) {
	var a args
	tz := a.add(q.TZ)
	sql := `SELECT count(*) FROM (SELECT DISTINCT date_trunc('day', e.ts AT TIME ZONE ` + tz + `::text), e.visitor_hash` +
		events(&a, sc, q.From, q.To) + ` AND e.traffic_class = 'human' AND e.visitor_hash <> '') v`
	var n int64
	if err := s.pool.QueryRow(ctx, sql, a...).Scan(&n); err != nil {
		return 0, fmt.Errorf("analytics: unique visitor days: %w", err)
	}
	return n, nil
}

// breakdownColumns are the columns a breakdown may group by; the column
// name never comes from a request.
var breakdownColumns = map[string]string{
	"device": "e.device", "browser": "e.browser", "os": "e.os",
	"referrer_host": "e.referrer_host", "country": "e.country",
}

type keyCount struct {
	key string
	n   int64
}

// breakdown counts human clicks by column, biggest first. With skipEmpty,
// visits without a value (no referrer, unknown country) are left out.
func (s *Store) breakdown(ctx context.Context, sc Scope, q Query, column string, limit int, skipEmpty bool) ([]keyCount, error) {
	col, ok := breakdownColumns[column]
	if !ok {
		return nil, fmt.Errorf("analytics: unknown breakdown %q", column)
	}
	var a args
	sql := `SELECT ` + col + `, count(*)` + events(&a, sc, q.From, q.To) + humanClick
	if skipEmpty {
		sql += ` AND ` + col + ` <> ''`
	}
	sql += ` GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT ` + a.add(limit)
	rows, err := s.pool.Query(ctx, sql, a...)
	if err != nil {
		return nil, fmt.Errorf("analytics: by %s: %w", column, err)
	}
	defer rows.Close()
	var out []keyCount
	for rows.Next() {
		var kc keyCount
		if err := rows.Scan(&kc.key, &kc.n); err != nil {
			return nil, fmt.Errorf("analytics: by %s: %w", column, err)
		}
		out = append(out, kc)
	}
	return out, rows.Err()
}

func (s *Store) breakdownMap(ctx context.Context, sc Scope, q Query, column, emptyAs string) (map[string]int64, error) {
	kcs, err := s.breakdown(ctx, sc, q, column, 10, false)
	if err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(kcs))
	for _, kc := range kcs {
		k := kc.key
		if k == "" {
			k = emptyAs
		}
		m[k] += kc.n
	}
	return m, nil
}

// topLinks ranks links in scope by human clicks.
func (s *Store) topLinks(ctx context.Context, sc Scope, q Query, limit int) ([]TopLink, error) {
	var a args
	sql := `SELECT l.id, l.code, count(*)` + events(&a, sc, q.From, q.To, "JOIN links l ON l.id = e.link_id") + humanClick +
		` GROUP BY l.id, l.code ORDER BY 3 DESC, l.id LIMIT ` + a.add(limit)
	rows, err := s.pool.Query(ctx, sql, a...)
	if err != nil {
		return nil, fmt.Errorf("analytics: top links: %w", err)
	}
	defer rows.Close()
	out := []TopLink{}
	for rows.Next() {
		var t TopLink
		if err := rows.Scan(&t.LinkID, &t.Code, &t.Human); err != nil {
			return nil, fmt.Errorf("analytics: top links: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// campaigns ranks a workspace's campaigns (or every campaign) by human
// clicks in the period, including campaigns without any.
func (s *Store) campaigns(ctx context.Context, workspaceID int64, all bool, q Query, limit int) ([]CampaignCount, error) {
	var a args
	from, to := a.add(q.From.UTC()), a.add(q.To.UTC())
	where := "TRUE"
	if !all {
		where = "c.workspace_id = " + a.add(workspaceID)
	}
	sql := `
		SELECT c.id, c.name, count(e.id)
		FROM campaigns c
		LEFT JOIN links l ON l.campaign_id = c.id
		LEFT JOIN click_events e ON e.link_id = l.id AND e.ts >= ` + from + ` AND e.ts < ` + to + humanClick + `
		WHERE ` + where + `
		GROUP BY c.id, c.name ORDER BY 3 DESC, c.id LIMIT ` + a.add(limit)
	rows, err := s.pool.Query(ctx, sql, a...)
	if err != nil {
		return nil, fmt.Errorf("analytics: campaigns: %w", err)
	}
	defer rows.Close()
	out := []CampaignCount{}
	for rows.Next() {
		var c CampaignCount
		if err := rows.Scan(&c.ID, &c.Name, &c.Human); err != nil {
			return nil, fmt.Errorf("analytics: campaigns: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LinkReport builds the full report for one link.
func (s *Store) LinkReport(ctx context.Context, linkID int64, q Query) (LinkReport, error) {
	if err := q.Check(); err != nil {
		return LinkReport{}, err
	}
	sc := ForLink(linkID)
	sm, err := s.summary(ctx, sc, q)
	if err != nil {
		return LinkReport{}, err
	}
	rep := LinkReport{
		LinkID: linkID, TZ: q.TZ, Interval: q.Interval, Totals: sm.totals, Series: sm.series,
		BySource: sm.bySource, Outcomes: sm.outcomes, ByReferrer: []HostCount{}, ByCountry: []CountryCount{},
	}
	if rep.ByDevice, err = s.breakdownMap(ctx, sc, q, "device", "unknown"); err != nil {
		return LinkReport{}, err
	}
	if rep.ByBrowser, err = s.breakdownMap(ctx, sc, q, "browser", "Other"); err != nil {
		return LinkReport{}, err
	}
	if rep.ByOS, err = s.breakdownMap(ctx, sc, q, "os", "Other"); err != nil {
		return LinkReport{}, err
	}
	refs, err := s.breakdown(ctx, sc, q, "referrer_host", 10, true)
	if err != nil {
		return LinkReport{}, err
	}
	for _, kc := range refs {
		rep.ByReferrer = append(rep.ByReferrer, HostCount{Host: kc.key, Clicks: kc.n})
	}
	countries, err := s.breakdown(ctx, sc, q, "country", 10, true)
	if err != nil {
		return LinkReport{}, err
	}
	for _, kc := range countries {
		rep.ByCountry = append(rep.ByCountry, CountryCount{Country: kc.key, Clicks: kc.n})
	}
	return rep, nil
}

// CampaignReport builds the report for the links in a campaign.
func (s *Store) CampaignReport(ctx context.Context, campaignID int64, q Query) (CampaignReport, error) {
	if err := q.Check(); err != nil {
		return CampaignReport{}, err
	}
	sc := ForCampaign(campaignID)
	sm, err := s.summary(ctx, sc, q)
	if err != nil {
		return CampaignReport{}, err
	}
	top, err := s.topLinks(ctx, sc, q, 10)
	if err != nil {
		return CampaignReport{}, err
	}
	return CampaignReport{CampaignID: campaignID, TZ: q.TZ, Interval: q.Interval, Totals: sm.totals, Series: sm.series, TopLinks: top}, nil
}

// Overview builds the dashboard summary for a workspace, or for the whole
// platform when all is set.
func (s *Store) Overview(ctx context.Context, workspaceID int64, all bool, q Query) (Overview, error) {
	if err := q.Check(); err != nil {
		return Overview{}, err
	}
	sc := ForWorkspace(workspaceID)
	if all {
		sc = ForAll()
	}
	sm, err := s.summary(ctx, sc, q)
	if err != nil {
		return Overview{}, err
	}
	top, err := s.topLinks(ctx, sc, q, 10)
	if err != nil {
		return Overview{}, err
	}
	camps, err := s.campaigns(ctx, workspaceID, all, q, 10)
	if err != nil {
		return Overview{}, err
	}
	return Overview{Totals: sm.totals, TopLinks: top, Campaigns: camps}, nil
}
