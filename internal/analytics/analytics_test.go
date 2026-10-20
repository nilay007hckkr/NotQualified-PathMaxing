package analytics

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"slices"
	"testing"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/migrate/pgtest"
	"pathmaxing/internal/store"
)

// fixture is a small, hand-counted set of visits. Link A and link B are in
// campaign C of workspace 1; link X belongs to workspace 2.
type fixture struct {
	an       *Store
	st       *store.Store
	a, b, x  domain.Link
	campaign int64
	from, to time.Time
}

func ev(link domain.Link, at string, class domain.TrafficClass, outcome domain.Outcome, mods ...func(*domain.ClickEvent)) domain.ClickEvent {
	ts, err := time.Parse(time.RFC3339, at)
	if err != nil {
		panic(err)
	}
	e := domain.ClickEvent{
		EventID: domain.NewEventID(), LinkID: link.ID, WorkspaceID: link.WorkspaceID, At: ts,
		Outcome: outcome, Class: class, Source: domain.SourceLink, Method: "GET",
	}
	for _, m := range mods {
		m(&e)
	}
	return e
}

func human(device domain.Device, browser, os, ref, country, visitor string) func(*domain.ClickEvent) {
	return func(e *domain.ClickEvent) {
		e.Device, e.Browser, e.OS, e.ReferrerHost, e.Country, e.VisitorHash = device, browser, os, ref, country, visitor
	}
}

func qr(e *domain.ClickEvent) { e.Source = domain.SourceQR }

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := pgtest.New(t)
	ctx := context.Background()
	st, err := store.New(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces (name) VALUES ('one'), ('two')`); err != nil {
		t.Fatal(err)
	}
	camp, err := st.SaveCampaign(ctx, store.Campaign{WorkspaceID: 1, Name: "C"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveCampaign(ctx, store.Campaign{WorkspaceID: 1, Name: "Quiet"}); err != nil {
		t.Fatal(err)
	}
	mk := func(ws int64, alias string, campaign *int64) domain.Link {
		l, err := st.CreateLink(ctx, store.NewLink{WorkspaceID: ws, OwnerID: ws, Alias: alias, Destination: "https://a.example", CampaignID: campaign})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	f := fixture{
		an: New(pool), st: st, campaign: camp.ID,
		a: mk(1, "linka", &camp.ID), b: mk(1, "linkb", &camp.ID), x: mk(2, "linkx", nil),
		// The IST days 18, 19 and 20 October 2026.
		from: time.Date(2026, 10, 17, 18, 30, 0, 0, time.UTC),
		to:   time.Date(2026, 10, 20, 18, 30, 0, 0, time.UTC),
	}
	a := f.a
	events := []domain.ClickEvent{
		// 18:29 UTC is 23:59 IST on the 18th; 18:31 UTC is 00:01 IST on the 19th.
		ev(a, "2026-10-18T18:29:00Z", domain.ClassHuman, domain.OutcomeRedirected, human("mobile", "Chrome", "Android", "instagram.com", "IN", "h1")),
		ev(a, "2026-10-18T18:31:00Z", domain.ClassHuman, domain.OutcomeRedirected, human("desktop", "Firefox", "Windows", "", "IN", "h1"), qr),
		ev(a, "2026-10-18T10:00:00Z", domain.ClassHuman, domain.OutcomeRedirected, human("mobile", "Chrome", "Android", "instagram.com", "US", "h2")),
		ev(a, "2026-10-18T10:05:00Z", domain.ClassHuman, domain.OutcomeRedirected, human("mobile", "Chrome", "Android", "instagram.com", "IN", "h2")),
		ev(a, "2026-10-18T11:00:00Z", domain.ClassEmailScanner, domain.OutcomeRedirected, human("", "", "", "", "", "h3")),
		ev(a, "2026-10-18T12:00:00Z", domain.ClassLinkPreview, domain.OutcomeRedirected),
		ev(a, "2026-10-19T05:00:00Z", domain.ClassCrawler, domain.OutcomeExpired),
		ev(a, "2026-10-19T06:00:00Z", domain.ClassHuman, domain.OutcomeCapReached, human("mobile", "Chrome", "Android", "", "IN", "h4")),
		ev(a, "2026-10-19T07:00:00Z", domain.ClassOtherBot, domain.OutcomeRedirected),
		// Outside the period on both sides.
		ev(a, "2026-10-17T18:29:59Z", domain.ClassHuman, domain.OutcomeRedirected),
		ev(a, "2026-10-20T18:30:00Z", domain.ClassHuman, domain.OutcomeRedirected),
		// Link B: two human clicks; link X in another workspace: five.
		ev(f.b, "2026-10-19T03:00:00Z", domain.ClassHuman, domain.OutcomeRedirected),
		ev(f.b, "2026-10-19T03:01:00Z", domain.ClassHuman, domain.OutcomeRedirected),
	}
	for range 5 {
		events = append(events, ev(f.x, "2026-10-20T01:00:00Z", domain.ClassHuman, domain.OutcomeRedirected))
	}
	if _, err := st.InsertClicks(ctx, events); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) query(tz, interval string) Query {
	return Query{From: f.from, To: f.to, TZ: tz, Interval: interval}
}

func TestLinkReport(t *testing.T) {
	f := newFixture(t)
	rep, err := f.an.LinkReport(context.Background(), f.a.ID, f.query("Asia/Kolkata", Day))
	if err != nil {
		t.Fatal(err)
	}
	wantTotals := Totals{Human: 4, EmailScanner: 1, LinkPreview: 1, Crawler: 0, OtherBot: 1, Refused: 2, UniqueVisitorDays: 4}
	if rep.Totals != wantTotals {
		t.Errorf("totals = %+v, want %+v", rep.Totals, wantTotals)
	}
	wantSeries := []Point{
		{T: "2026-10-18T00:00:00+05:30", Human: 3, Bots: 2},
		{T: "2026-10-19T00:00:00+05:30", Human: 1, Bots: 1, Refused: 2},
		{T: "2026-10-20T00:00:00+05:30"},
	}
	if !slices.Equal(rep.Series, wantSeries) {
		t.Errorf("series = %+v, want %+v", rep.Series, wantSeries)
	}
	checkMap(t, "by_device", rep.ByDevice, map[string]int64{"mobile": 3, "desktop": 1})
	checkMap(t, "by_browser", rep.ByBrowser, map[string]int64{"Chrome": 3, "Firefox": 1})
	checkMap(t, "by_os", rep.ByOS, map[string]int64{"Android": 3, "Windows": 1})
	checkMap(t, "by_source", rep.BySource, map[string]int64{"link": 3, "qr": 1})
	if !slices.Equal(rep.ByReferrer, []HostCount{{"instagram.com", 3}}) {
		t.Errorf("by_referrer = %+v", rep.ByReferrer)
	}
	if !slices.Equal(rep.ByCountry, []CountryCount{{"IN", 3}, {"US", 1}}) {
		t.Errorf("by_country = %+v", rep.ByCountry)
	}
	wantOutcomes := zeroOutcomes()
	wantOutcomes["redirected"], wantOutcomes["expired"], wantOutcomes["cap_reached"] = 7, 1, 1
	checkMap(t, "outcomes", rep.Outcomes, wantOutcomes)
	if len(rep.Outcomes) != 9 {
		t.Errorf("outcomes has %d keys, want all 9", len(rep.Outcomes))
	}
}

// The same two visits, a minute either side of IST midnight, land on
// different IST days and hours but on the same UTC day.
func TestTimeZoneBuckets(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hourly, err := f.an.LinkReport(ctx, f.a.ID, f.query("Asia/Kolkata", Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly.Series) != 72 {
		t.Fatalf("hourly series has %d points, want 72", len(hourly.Series))
	}
	byT := map[string]Point{}
	for _, p := range hourly.Series {
		byT[p.T] = p
	}
	if byT["2026-10-18T23:00:00+05:30"].Human != 1 || byT["2026-10-19T00:00:00+05:30"].Human != 1 {
		t.Errorf("IST hours around midnight: %+v, %+v", byT["2026-10-18T23:00:00+05:30"], byT["2026-10-19T00:00:00+05:30"])
	}

	utc, err := f.an.LinkReport(ctx, f.a.ID, Query{From: f.from, To: f.to, TZ: "UTC", Interval: Day})
	if err != nil {
		t.Fatal(err)
	}
	want := []Point{
		{T: "2026-10-17T00:00:00Z"},
		{T: "2026-10-18T00:00:00Z", Human: 4, Bots: 2},
		{T: "2026-10-19T00:00:00Z", Bots: 1, Refused: 2},
		{T: "2026-10-20T00:00:00Z"},
	}
	if !slices.Equal(utc.Series, want) {
		t.Errorf("UTC series = %+v, want %+v", utc.Series, want)
	}
	// h1 visited on two IST days but one UTC day.
	if utc.Totals.UniqueVisitorDays != 3 {
		t.Errorf("UTC unique visitor days = %d, want 3", utc.Totals.UniqueVisitorDays)
	}
}

func TestCampaignAndOverview(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	q := f.query("Asia/Kolkata", Day)

	camp, err := f.an.CampaignReport(ctx, f.campaign, q)
	if err != nil {
		t.Fatal(err)
	}
	if camp.Totals.Human != 6 || camp.Totals.Refused != 2 {
		t.Errorf("campaign totals = %+v", camp.Totals)
	}
	if !slices.Equal(camp.TopLinks, []TopLink{{f.a.ID, "linka", 4}, {f.b.ID, "linkb", 2}}) {
		t.Errorf("campaign top links = %+v", camp.TopLinks)
	}

	ws, err := f.an.Overview(ctx, 1, false, q)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Totals.Human != 6 || len(ws.TopLinks) != 2 || ws.AlertsOpen != nil {
		t.Errorf("workspace overview = %+v", ws)
	}
	if len(ws.Campaigns) != 2 || ws.Campaigns[0].Name != "C" || ws.Campaigns[0].Human != 6 || ws.Campaigns[1].Human != 0 {
		t.Errorf("workspace campaigns = %+v", ws.Campaigns)
	}

	all, err := f.an.Overview(ctx, 0, true, q)
	if err != nil {
		t.Fatal(err)
	}
	if all.Totals.Human != 11 || len(all.TopLinks) != 3 || all.TopLinks[0].LinkID != f.x.ID || all.TopLinks[0].Human != 5 {
		t.Errorf("platform overview = %+v", all)
	}
}

func TestQueryCheck(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		q    Query
		ok   bool
	}{
		{"defaults", Query{From: from, To: from.Add(time.Hour)}, true},
		{"unknown zone", Query{From: from, To: from.Add(time.Hour), TZ: "Mars/Olympus"}, false},
		{"empty range", Query{From: from, To: from}, false},
		{"hourly too long", Query{From: from, To: from.Add(40 * 24 * time.Hour), Interval: Hour}, false},
		{"bad interval", Query{From: from, To: from.Add(time.Hour), Interval: "week"}, false},
	}
	for _, tt := range tests {
		q := tt.q
		if err := q.Check(); (err == nil) != tt.ok {
			t.Errorf("%s: Check() = %v", tt.name, err)
		}
	}
	q := Query{From: from, To: from.Add(time.Hour)}
	_ = q.Check()
	if q.TZ != DefaultTZ || q.Interval != Day {
		t.Errorf("defaults = %q %q", q.TZ, q.Interval)
	}
}

func checkMap(t *testing.T, name string, got, want map[string]int64) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// The rollups must give exactly what the raw events give, for any period
// (aligned or not), time zone and interval.
func TestRollupsMatchRawEvents(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	periods := [][2]time.Time{
		{f.from, f.to},
		{f.from.Add(7 * time.Minute), f.to.Add(-3 * time.Minute)},
		{time.Date(2026, 10, 18, 18, 29, 30, 0, time.UTC), time.Date(2026, 10, 18, 18, 31, 30, 0, time.UTC)},
		{time.Date(2026, 10, 18, 10, 2, 0, 0, time.UTC), time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)},
	}
	for _, p := range periods {
		for _, tz := range []string{"Asia/Kolkata", "UTC", "Asia/Kathmandu", "America/New_York"} {
			for _, interval := range []string{Hour, Day} {
				for _, sc := range []Scope{ForLink(f.a.ID), ForCampaign(f.campaign), ForWorkspace(1), ForAll()} {
					q := Query{From: p[0], To: p[1], TZ: tz, Interval: interval}
					if err := q.Check(); err != nil {
						t.Fatal(err)
					}
					fast, err := f.an.counts(ctx, sc, q)
					if err != nil {
						t.Fatal(err)
					}
					raw, err := f.an.rawCounts(ctx, sc, q, q.From, q.To)
					if err != nil {
						t.Fatal(err)
					}
					got, want := summarize(q, fast), summarize(q, raw)
					if got.totals != want.totals || !slices.Equal(got.series, want.series) ||
						!maps.Equal(got.outcomes, want.outcomes) || !maps.Equal(got.bySource, want.bySource) {
						t.Fatalf("%v..%v %s %s scope %+v: rollups %+v, raw %+v", p[0], p[1], tz, interval, sc, got, want)
					}
				}
			}
		}
	}
	var rolled int64
	if err := f.st.Pool().QueryRow(ctx, `SELECT sum(n) FROM click_rollups`).Scan(&rolled); err != nil || rolled != 18 {
		t.Fatalf("rollups hold %d events, want all 18", rolled)
	}
}
