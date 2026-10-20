package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"pathmaxing/internal/analytics"
	"pathmaxing/internal/clicks"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
)

const (
	defaultPeriod = 7 * 24 * time.Hour
	heartbeat     = 15 * time.Second
)

func (s *Server) registerAnalytics(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/analytics/links/{id}", s.authed(s.linkAnalytics))
	mux.HandleFunc("GET /api/analytics/campaigns/{id}", s.authed(s.campaignAnalytics))
	mux.HandleFunc("GET /api/analytics/overview", s.authed(s.overview(false)))
	mux.HandleFunc("GET /api/analytics/live", s.authed(s.live))
}

// analyticsQuery reads from, to, tz and interval. The period defaults to
// the last 7 days, the zone to Asia/Kolkata.
func (s *Server) analyticsQuery(w http.ResponseWriter, r *http.Request) (analytics.Query, bool) {
	v := r.URL.Query()
	q := analytics.Query{TZ: v.Get("tz"), Interval: v.Get("interval"), To: s.now()}
	for _, f := range []struct {
		name string
		dst  *time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if raw := v.Get(f.name); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", f.name+" must be an RFC 3339 time such as 2026-10-18T00:00:00+05:30")
				return q, false
			}
			*f.dst = t
		}
	}
	if q.From.IsZero() {
		q.From = q.To.Add(-defaultPeriod)
	}
	if err := q.Check(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return q, false
	}
	return q, true
}

func (s *Server) linkAnalytics(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	// Deleted links keep their analytics, so they are readable here.
	id, ok := pathID(r)
	if !ok {
		s.fail(w, domain.ErrNotFound, "link")
		return
	}
	l, err := s.Store.GetLink(r.Context(), id)
	if err == nil && !p.CanAccess(l.WorkspaceID) {
		err = domain.ErrNotFound
	}
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	q, ok := s.analyticsQuery(w, r)
	if !ok {
		return
	}
	rep, err := s.Analytics.LinkReport(r.Context(), l.ID, q)
	if err != nil {
		s.fail(w, err, "analytics")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (s *Server) campaignAnalytics(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	c, err := s.ownedCampaign(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "campaign")
		return
	}
	q, ok := s.analyticsQuery(w, r)
	if !ok {
		return
	}
	rep, err := s.Analytics.CampaignReport(r.Context(), c.ID, q)
	if err != nil {
		s.fail(w, err, "analytics")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

// overview is the dashboard summary: the principal's own workspace, or with
// platform set (the admin route) every workspace.
func (s *Server) overview(platform bool) authedFunc {
	return func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
		q, ok := s.analyticsQuery(w, r)
		if !ok {
			return
		}
		ov, err := s.Analytics.Overview(r.Context(), p.WorkspaceID, platform, q)
		if err != nil {
			s.fail(w, err, "analytics")
			return
		}
		if s.Alerts != nil {
			// Without a count the tile shows nothing rather than a wrong zero.
			if n, err := s.Alerts.CountOpenAlerts(r.Context(), p.WorkspaceID, platform); err != nil {
				s.Log.Warn("count open alerts", "err", err)
			} else {
				ov.AlertsOpen = &n
			}
		}
		httpx.WriteJSON(w, http.StatusOK, ov)
	}
}

// live streams the principal's clicks as Server-Sent Events, optionally for
// one link. Clients that can't keep a stream open poll the analytics
// endpoints instead.
func (s *Server) live(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var linkID int64
	if raw := r.URL.Query().Get("link_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "link_id must be a positive number")
			return
		}
		l, err := s.Store.GetLink(r.Context(), id)
		if err == nil && !p.CanAccess(l.WorkspaceID) {
			err = domain.ErrNotFound
		}
		if err != nil {
			s.fail(w, err, "link")
			return
		}
		linkID = l.ID
	}
	if s.Live == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "LIVE_UNAVAILABLE", "live updates are not available; poll instead")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "INTERNAL", "streaming is not supported")
		return
	}

	events, cancel := s.Live.Subscribe(func(c clicks.LiveClick) bool {
		if linkID != 0 {
			return c.LinkID == linkID
		}
		return p.IsAdmin() || c.WorkspaceID == p.WorkspaceID
	})
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // proxies must not hold events back
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case c := <-events:
			data, _ := json.Marshal(c)
			if _, err := fmt.Fprintf(w, "event: click\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-tick.C:
			// A comment line keeps proxies from closing an idle stream.
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.Stopping:
			// Shutdown waits for open requests, so the stream must end itself.
			return
		}
	}
}
