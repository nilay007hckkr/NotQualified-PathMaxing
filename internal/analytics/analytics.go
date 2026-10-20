// Package analytics answers the dashboard's questions from click events:
// totals by traffic class, a time series in the viewer's time zone,
// breakdowns of human clicks, and rankings of links and campaigns.
//
// Counting rules, so the numbers add up:
//   - A class count (human, email_scanner, ...) counts visits of that class
//     that were redirected. "refused" counts every refused visit, whatever its
//     class. Together they add up to every recorded visit, and for a capped
//     link "human" equals the clicks that used up the cap.
//   - Breakdowns (device, browser, ...) and rankings count redirected human
//     visits only.
//   - unique_visitor_days counts distinct (day, visitor) pairs among human
//     visits. Visitor hashes rotate daily by design (no long-term tracking),
//     so "unique visitors" over a week can't be computed honestly.
package analytics

import (
	"fmt"
	"time"
	_ "time/tzdata" // the IANA database, so time zones work in minimal containers

	"pathmaxing/internal/domain"
)

// DefaultTZ is the dashboard's default time zone.
const DefaultTZ = "Asia/Kolkata"

// Intervals for the time series.
const (
	Hour = "hour"
	Day  = "day"
)

// Range limits keep a series small enough to draw.
const (
	maxHourRange = 31 * 24 * time.Hour
	maxDayRange  = 400 * 24 * time.Hour
)

// Query is a period and how to bucket it.
type Query struct {
	From, To time.Time // [From, To)
	TZ       string    // IANA name
	Interval string    // Hour or Day
	loc      *time.Location
}

// Check validates q and loads its time zone.
func (q *Query) Check() error {
	if q.TZ == "" {
		q.TZ = DefaultTZ
	}
	loc, err := time.LoadLocation(q.TZ)
	if err != nil || q.TZ == "Local" {
		return fmt.Errorf("unknown time zone %q", q.TZ)
	}
	q.loc = loc
	if q.Interval == "" {
		q.Interval = Day
	}
	if !q.To.After(q.From) {
		return fmt.Errorf("to must be after from")
	}
	switch q.Interval {
	case Hour:
		if q.To.Sub(q.From) > maxHourRange {
			return fmt.Errorf("an hourly series can cover at most 31 days")
		}
	case Day:
		if q.To.Sub(q.From) > maxDayRange {
			return fmt.Errorf("a daily series can cover at most 400 days")
		}
	default:
		return fmt.Errorf("interval must be hour or day")
	}
	return nil
}

// Totals is the headline row.
type Totals struct {
	Human             int64 `json:"human"`
	EmailScanner      int64 `json:"email_scanner"`
	LinkPreview       int64 `json:"link_preview"`
	Crawler           int64 `json:"crawler"`
	OtherBot          int64 `json:"other_bot"`
	Refused           int64 `json:"refused"`
	UniqueVisitorDays int64 `json:"unique_visitor_days"`
}

func (t *Totals) add(class string, outcome string, n int64) {
	if outcome != string(domain.OutcomeRedirected) {
		t.Refused += n
		return
	}
	switch domain.TrafficClass(class) {
	case domain.ClassHuman:
		t.Human += n
	case domain.ClassEmailScanner:
		t.EmailScanner += n
	case domain.ClassLinkPreview:
		t.LinkPreview += n
	case domain.ClassCrawler:
		t.Crawler += n
	default:
		t.OtherBot += n
	}
}

// Point is one bucket of the series. T is the bucket's start in the
// requested time zone, with its offset, e.g. 2026-10-18T00:00:00+05:30.
type Point struct {
	T       string `json:"t"`
	Human   int64  `json:"human"`
	Bots    int64  `json:"bots"`
	Refused int64  `json:"refused"`
}

// HostCount is one referrer.
type HostCount struct {
	Host   string `json:"host"`
	Clicks int64  `json:"clicks"`
}

// CountryCount is one country.
type CountryCount struct {
	Country string `json:"country"`
	Clicks  int64  `json:"clicks"`
}

// TopLink is one link in a ranking.
type TopLink struct {
	LinkID int64  `json:"link_id"`
	Code   string `json:"code"`
	Human  int64  `json:"human"`
}

// CampaignCount is one campaign in the overview.
type CampaignCount struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Human int64  `json:"human"`
}

// LinkReport is GET /api/analytics/links/{id}.
type LinkReport struct {
	LinkID     int64            `json:"link_id"`
	TZ         string           `json:"tz"`
	Interval   string           `json:"interval"`
	Totals     Totals           `json:"totals"`
	Series     []Point          `json:"series"`
	ByDevice   map[string]int64 `json:"by_device"`
	ByBrowser  map[string]int64 `json:"by_browser"`
	ByOS       map[string]int64 `json:"by_os"`
	ByReferrer []HostCount      `json:"by_referrer"`
	ByCountry  []CountryCount   `json:"by_country"`
	BySource   map[string]int64 `json:"by_source"`
	Outcomes   map[string]int64 `json:"outcomes"`
}

// CampaignReport is GET /api/analytics/campaigns/{id}.
type CampaignReport struct {
	CampaignID int64     `json:"campaign_id"`
	TZ         string    `json:"tz"`
	Interval   string    `json:"interval"`
	Totals     Totals    `json:"totals"`
	Series     []Point   `json:"series"`
	TopLinks   []TopLink `json:"top_links"`
}

// Overview is GET /api/analytics/overview.
type Overview struct {
	Totals    Totals          `json:"totals"`
	TopLinks  []TopLink       `json:"top_links"`
	Campaigns []CampaignCount `json:"campaigns"`
	// AlertsOpen is null until the alerts module can count them.
	AlertsOpen *int `json:"alerts_open"`
}

// buckets lists every bucket start in [from, to), so days and hours without
// clicks show as zeros instead of gaps in the chart.
func buckets(q Query) []time.Time {
	start := truncate(q.From.In(q.loc), q.Interval)
	var out []time.Time
	seen := map[time.Time]bool{}
	for t := start; t.Before(q.To); {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
		switch q.Interval {
		case Hour:
			// Step in absolute time and re-truncate, so a DST change neither
			// skips nor repeats a wall-clock hour.
			t = truncate(t.Add(time.Hour), Hour)
		default:
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, q.loc)
		}
	}
	return out
}

func truncate(t time.Time, interval string) time.Time {
	if interval == Hour {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// wallKey identifies a bucket by its wall-clock start, which is what
// date_trunc on "ts AT TIME ZONE tz" returns.
func wallKey(t time.Time) string { return t.Format("2006-01-02T15") }

// fillSeries turns sparse rows keyed by wall-clock bucket into a full series.
func fillSeries(q Query, rows map[string]Point) []Point {
	bs := buckets(q)
	out := make([]Point, 0, len(bs))
	for _, b := range bs {
		p := rows[wallKey(b)]
		p.T = b.Format(time.RFC3339)
		out = append(out, p)
	}
	return out
}

func zeroOutcomes() map[string]int64 {
	m := make(map[string]int64, 9)
	for _, o := range domain.Outcomes() {
		m[string(o)] = 0
	}
	return m
}
