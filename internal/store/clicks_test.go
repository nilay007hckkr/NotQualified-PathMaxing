package store

import (
	"context"
	"testing"
	"time"

	"pathmaxing/internal/domain"
)

// A batch that is retried after a timeout may already have committed; the
// second insert must not count anything twice.
func TestInsertClicksIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	l, err := s.CreateLink(ctx, NewLink{WorkspaceID: 1, OwnerID: 1, Alias: "clicks", Destination: "https://a.example"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 18, 18, 30, 0, 0, time.UTC)
	batch := []domain.ClickEvent{
		{EventID: domain.NewEventID(), LinkID: l.ID, WorkspaceID: 1, At: at, Outcome: domain.OutcomeRedirected,
			Class: domain.ClassHuman, ClassReason: "default", Source: domain.SourceQR, Method: "GET",
			Device: domain.DeviceMobile, Browser: "Chrome", OS: "Android", ReferrerHost: "instagram.com",
			Country: "IN", VisitorHash: "abc"},
		{EventID: domain.NewEventID(), LinkID: l.ID, WorkspaceID: 1, At: at, Outcome: domain.OutcomeExpired,
			Class: domain.ClassCrawler, Source: domain.SourceLink, Method: "HEAD"},
	}
	for i, want := range []int64{2, 0} {
		n, err := s.InsertClicks(ctx, batch)
		if err != nil || n != want {
			t.Fatalf("insert %d: %d new, %v; want %d", i+1, n, err, want)
		}
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM click_events`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rows = %d, %v; want 2", count, err)
	}
	var source, country, device string
	var ts time.Time
	if err := s.pool.QueryRow(ctx, `SELECT source, country, device, ts FROM click_events WHERE event_id = $1`, batch[0].EventID).
		Scan(&source, &country, &device, &ts); err != nil {
		t.Fatal(err)
	}
	if source != "qr" || country != "IN" || device != "mobile" || !ts.Equal(at) {
		t.Fatalf("stored %q %q %q %v", source, country, device, ts)
	}
	if n, err := s.InsertClicks(ctx, nil); err != nil || n != 0 {
		t.Fatalf("empty batch = %d, %v", n, err)
	}
}
