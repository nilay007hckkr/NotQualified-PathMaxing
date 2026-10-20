package alerts

import (
	"net/http"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
)

// API serves GET /api/alerts.
type API struct {
	Guard domain.AuthGuard
	Store *Store
}

// Register mounts the route.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alerts", a.list)
}

// list answers ?status=open (the default) or ?status=all, for the caller's
// workspace; admins see every workspace's.
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	p, err := a.Guard.Authenticate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "log in first")
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "open" && status != "all" {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "status must be open or all")
		return
	}
	items, err := a.Store.List(r.Context(), p.WorkspaceID, p.IsAdmin(), status == "all", 100)
	if err != nil {
		a.Store.Log.Error("list alerts", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "INTERNAL", "could not load alerts")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
