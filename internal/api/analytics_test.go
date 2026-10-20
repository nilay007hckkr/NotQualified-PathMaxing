package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"pathmaxing/internal/analytics"
	"pathmaxing/internal/domain"
)

func (e *env) clicks(l linkJSON, ws int64, at time.Time, class domain.TrafficClass, n int) {
	e.t.Helper()
	var batch []domain.ClickEvent
	for range n {
		batch = append(batch, domain.ClickEvent{
			EventID: domain.NewEventID(), LinkID: l.ID, WorkspaceID: ws, At: at,
			Outcome: domain.OutcomeRedirected, Class: class, Source: domain.SourceLink, Method: "GET",
			Device: domain.DeviceMobile, VisitorHash: "v" + strconv.Itoa(n),
		})
	}
	if _, err := e.store.InsertClicks(context.Background(), batch); err != nil {
		e.t.Fatal(err)
	}
}

func TestAnalyticsEndpoints(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	e.srv.Now = func() time.Time { return now }
	c, u := e.signup("an@example.com")
	other, _ := e.signup("other@example.com")
	camp := idOf(c.createOrg(t, "/api/campaigns", "campaign", map[string]any{"name": "Fest"}))
	l := c.create(t, map[string]any{"destination": "https://a.example", "campaign_id": camp})
	e.clicks(l, u.WorkspaceID, now.Add(-2*time.Hour), domain.ClassHuman, 3)
	e.clicks(l, u.WorkspaceID, now.Add(-time.Hour), domain.ClassLinkPreview, 2)
	e.clicks(l, u.WorkspaceID, now.Add(-10*24*time.Hour), domain.ClassHuman, 5) // before the default 7 days

	path := "/api/analytics/links/" + strconv.FormatInt(l.ID, 10)
	r := c.do("GET", path, nil)
	var rep analytics.LinkReport
	r.json(t, &rep)
	if r.status != http.StatusOK || rep.TZ != "Asia/Kolkata" || rep.Interval != "day" || rep.Totals.Human != 3 ||
		rep.Totals.LinkPreview != 2 || len(rep.Series) != 8 || rep.ByDevice["mobile"] != 3 || len(rep.Outcomes) != 9 {
		t.Fatalf("default report = %d %s", r.status, r.body)
	}
	if !strings.HasSuffix(rep.Series[0].T, "+05:30") {
		t.Fatalf("series times must carry the IST offset: %q", rep.Series[0].T)
	}
	r = c.do("GET", path+"?from=2026-10-01T00:00:00Z&to=2026-10-21T00:00:00Z&tz=UTC&interval=day", nil)
	r.json(t, &rep)
	if r.status != http.StatusOK || rep.Totals.Human != 8 || rep.TZ != "UTC" || len(rep.Series) != 20 {
		t.Fatalf("explicit period = %d, human %d, %d points", r.status, rep.Totals.Human, len(rep.Series))
	}
	for _, bad := range []string{"?tz=Mars/Base", "?interval=week", "?from=yesterday", "?from=2026-10-21T00:00:00Z&to=2026-10-01T00:00:00Z"} {
		if r := c.do("GET", path+bad, nil); r.status != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, r.status)
		}
	}
	if r := other.do("GET", path, nil); r.status != http.StatusNotFound {
		t.Fatalf("another user's analytics = %d", r.status)
	}

	var cr analytics.CampaignReport
	r = c.do("GET", "/api/analytics/campaigns/"+strconv.FormatInt(camp, 10), nil)
	r.json(t, &cr)
	if r.status != http.StatusOK || cr.CampaignID != camp || cr.Totals.Human != 3 || len(cr.TopLinks) != 1 || cr.TopLinks[0].Code != l.Code {
		t.Fatalf("campaign report = %d %s", r.status, r.body)
	}
	if r := other.do("GET", "/api/analytics/campaigns/"+strconv.FormatInt(camp, 10), nil); r.status != http.StatusNotFound {
		t.Fatalf("another user's campaign analytics = %d", r.status)
	}

	r = c.do("GET", "/api/analytics/overview", nil)
	var raw map[string]json.RawMessage
	r.json(t, &raw)
	if r.status != http.StatusOK || string(raw["alerts_open"]) != "null" {
		t.Fatalf("overview = %d %s", r.status, r.body)
	}
	var ov analytics.Overview
	r.json(t, &ov)
	if ov.Totals.Human != 3 || len(ov.TopLinks) != 1 || len(ov.Campaigns) != 1 || ov.Campaigns[0].Human != 3 {
		t.Fatalf("overview = %s", r.body)
	}
	other.do("GET", "/api/analytics/overview", nil).json(t, &ov)
	if ov.Totals.Human != 0 || len(ov.TopLinks) != 0 {
		t.Fatalf("another user's overview shows our clicks: %+v", ov)
	}

	// Analytics outlive a soft delete.
	c.do("DELETE", "/api/links/"+strconv.FormatInt(l.ID, 10), nil)
	if r := c.do("GET", path, nil); r.status != http.StatusOK {
		t.Fatalf("analytics of a deleted link = %d", r.status)
	}
}

func TestLiveStream(t *testing.T) {
	e := newEnv(t)
	c, u := e.signup("live@example.com")
	other, ou := e.signup("other@example.com")
	mine := c.create(t, map[string]any{"destination": "https://a.example"})
	theirs := other.create(t, map[string]any{"destination": "https://b.example"})

	if r := other.do("GET", "/api/analytics/live?link_id="+strconv.FormatInt(mine.ID, 10), nil); r.status != http.StatusNotFound {
		t.Fatalf("another user's live stream = %d", r.status)
	}

	req, _ := http.NewRequest("GET", e.http.URL+"/api/analytics/live", nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("live = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func() (string, bool) {
		select {
		case l, ok := <-lines:
			return l, ok
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the stream")
			return "", false
		}
	}
	if l, _ := next(); l != ": connected" {
		t.Fatalf("first line %q", l)
	}

	at := time.Date(2026, 10, 20, 6, 0, 0, 0, time.UTC)
	e.srv.Live.Publish([]domain.ClickEvent{
		{LinkID: theirs.ID, WorkspaceID: ou.WorkspaceID, Class: domain.ClassHuman, At: at},
		{LinkID: mine.ID, WorkspaceID: u.WorkspaceID, Class: domain.ClassLinkPreview, At: at},
	})
	var event, data string
	for event == "" || data == "" {
		l, ok := next()
		if !ok {
			t.Fatal("stream ended early")
		}
		switch {
		case strings.HasPrefix(l, "event: "):
			event = strings.TrimPrefix(l, "event: ")
		case strings.HasPrefix(l, "data: "):
			data = strings.TrimPrefix(l, "data: ")
		}
	}
	want := `{"link_id":` + strconv.FormatInt(mine.ID, 10) + `,"traffic_class":"link_preview","ts":"2026-10-20T06:00:00Z"}`
	if event != "click" || data != want {
		t.Fatalf("event %q data %s, want click %s (another workspace's click must not show)", event, data, want)
	}

	// Shutdown ends the stream instead of holding the server open.
	close(e.stopping)
	for {
		if _, ok := next(); !ok {
			break
		}
	}
}
