package trust

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
	"pathmaxing/internal/ratelimit"
)

// API serves a link's trust history and forced re-scans.
type API struct {
	Guard     domain.AuthGuard
	Links     domain.LinkStore
	Repo      Repo
	Scheduler *Scheduler
	// RescanLimiter is keyed by user: 10 re-scans per hour.
	RescanLimiter *ratelimit.Limiter
	Log           *slog.Logger
}

// Register mounts GET /api/links/{id}/trust and POST /api/links/{id}/rescan.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/links/{id}/trust", a.trust)
	mux.HandleFunc("POST /api/links/{id}/rescan", a.rescan)
}

type checkJSON struct {
	CheckedAt   time.Time `json:"checked_at"`
	HTTPStatus  int       `json:"http_status"`
	FinalURL    string    `json:"final_url"`
	FinalDomain string    `json:"final_domain"`
	Hops        int       `json:"hops"`
	Verdict     string    `json:"verdict"`
	Sources     []string  `json:"sources"`
	Detail      string    `json:"detail"`
	Error       string    `json:"error"`
}

type trustJSON struct {
	State         domain.TrustState `json:"state"`
	LastCheckedAt *time.Time        `json:"last_checked_at"`
	NextCheckAt   *time.Time        `json:"next_check_at"`
	Checks        []checkJSON       `json:"checks"`
}

// link authenticates the request and loads a link the caller may see.
// Anyone else's link is a 404, never a 403, so links can't be probed.
func (a *API) link(w http.ResponseWriter, r *http.Request) (domain.Principal, domain.Link, bool) {
	p, err := a.Guard.Authenticate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "log in first")
		return p, domain.Link{}, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "link not found")
		return p, domain.Link{}, false
	}
	l, err := a.Links.GetLink(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && (!p.CanAccess(l.WorkspaceID) || l.Deleted())) {
		httpx.WriteError(w, http.StatusNotFound, "NOT_FOUND", "link not found")
		return p, domain.Link{}, false
	}
	if err != nil {
		a.Log.Error("load link", "link_id", id, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "INTERNAL", "could not load the link")
		return p, domain.Link{}, false
	}
	return p, l, true
}

func (a *API) trust(w http.ResponseWriter, r *http.Request) {
	_, l, ok := a.link(w, r)
	if !ok {
		return
	}
	hist, err := a.Repo.History(r.Context(), l.ID, 50)
	if err != nil {
		a.Log.Error("trust history", "link_id", l.ID, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "INTERNAL", "could not load the trust history")
		return
	}
	next, _ := a.Repo.NextCheckAt(r.Context(), l.ID)
	out := trustJSON{State: l.TrustState, LastCheckedAt: l.LastCheckedAt, NextCheckAt: next, Checks: []checkJSON{}}
	for _, c := range hist {
		sources := c.Sources
		if sources == nil {
			sources = []string{}
		}
		out.Checks = append(out.Checks, checkJSON{
			CheckedAt: c.CheckedAt, HTTPStatus: c.HTTPStatus, FinalURL: c.FinalURL, FinalDomain: c.FinalDomain,
			Hops: c.Hops, Verdict: c.Verdict, Sources: sources, Detail: c.Detail, Error: c.Error,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) rescan(w http.ResponseWriter, r *http.Request) {
	p, l, ok := a.link(w, r)
	if !ok {
		return
	}
	if allowed, wait := a.RescanLimiter.Allow(strconv.FormatInt(p.UserID, 10)); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		httpx.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many re-scans; try again later")
		return
	}
	if err := a.Repo.ScheduleNow(r.Context(), l.ID); err != nil {
		a.Log.Error("schedule rescan", "link_id", l.ID, "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "INTERNAL", "could not queue the re-scan")
		return
	}
	a.Scheduler.Kick()
	httpx.WriteJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}
