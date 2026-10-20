package store

import (
	"context"
	"time"

	"pathmaxing/internal/domain"
)

// InsertClicks writes a batch of click events in one statement and returns
// how many were new. Each column travels as one array and unnest turns them
// back into rows, so a 500-row batch is a single round trip. ON CONFLICT on
// event_id makes a retried batch harmless: rows that already committed are
// skipped instead of counted twice.
func (s *Store) InsertClicks(ctx context.Context, events []domain.ClickEvent) (int64, error) {
	if len(events) == 0 {
		return 0, nil
	}
	n := len(events)
	var (
		ids        = make([]string, n)
		links      = make([]int64, n)
		workspaces = make([]int64, n)
		ts         = make([]time.Time, n)
		outcomes   = make([]string, n)
		classes    = make([]string, n)
		reasons    = make([]string, n)
		sources    = make([]string, n)
		methods    = make([]string, n)
		devices    = make([]string, n)
		browsers   = make([]string, n)
		oses       = make([]string, n)
		referrers  = make([]string, n)
		countries  = make([]string, n)
		visitors   = make([]string, n)
	)
	for i, e := range events {
		ids[i], links[i], workspaces[i], ts[i] = e.EventID, e.LinkID, e.WorkspaceID, e.At.UTC()
		outcomes[i], classes[i], reasons[i] = string(e.Outcome), string(e.Class), e.ClassReason
		sources[i], methods[i], devices[i] = string(e.Source), e.Method, string(e.Device)
		browsers[i], oses[i], referrers[i] = e.Browser, e.OS, e.ReferrerHost
		countries[i], visitors[i] = e.Country, e.VisitorHash
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO click_events (event_id, link_id, workspace_id, ts, outcome, traffic_class, class_reason,
		                          source, method, device, browser, os, referrer_host, country, visitor_hash)
		SELECT * FROM unnest($1::uuid[], $2::bigint[], $3::bigint[], $4::timestamptz[], $5::text[], $6::text[],
		                     $7::text[], $8::text[], $9::text[], $10::text[], $11::text[], $12::text[],
		                     $13::text[], $14::text[], $15::text[])
		ON CONFLICT (event_id) DO NOTHING`,
		ids, links, workspaces, ts, outcomes, classes, reasons, sources, methods, devices,
		browsers, oses, referrers, countries, visitors)
	if err != nil {
		return 0, wrap("insert clicks", err)
	}
	return tag.RowsAffected(), nil
}
