package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"pathmaxing/internal/admin"
	"pathmaxing/internal/auth"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
	"pathmaxing/internal/store"
)

func (s *Server) registerAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/links", s.adminOnly(s.adminLinks(false)))
	mux.HandleFunc("GET /api/admin/abuse", s.adminOnly(s.adminLinks(true)))
	mux.HandleFunc("POST /api/admin/links/{id}/disable", s.adminOnly(s.disableLink))
	mux.HandleFunc("POST /api/admin/links/{id}/enable", s.adminOnly(s.enableLink))
	mux.HandleFunc("GET /api/admin/blocklist", s.adminOnly(s.listBlocklist))
	mux.HandleFunc("POST /api/admin/blocklist", s.adminOnly(s.blockDomain))
	mux.HandleFunc("DELETE /api/admin/blocklist/{domain}", s.adminOnly(s.unblockDomain))
	mux.HandleFunc("GET /api/admin/audit", s.adminOnly(s.listAudit))
	mux.HandleFunc("GET /api/admin/users", s.adminOnly(s.listUsers))
	mux.HandleFunc("GET /api/admin/analytics/overview", s.adminOnly(s.overview(true)))
}

// adminOnly runs h for admins and answers 403 for everyone else. Admin
// routes manage the platform, not anyone's own resources, so a 403 gives
// nothing away.
func (s *Server) adminOnly(h authedFunc) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
		if !p.IsAdmin() {
			httpx.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only admins can do this")
			return
		}
		h(w, r, p)
	})
}

// adminLinks lists every user's links; with abuse set, only those waiting
// for review.
func (s *Server) adminLinks(abuse bool) authedFunc {
	return func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
		q := r.URL.Query()
		f := store.LinkFilter{All: true, Abuse: abuse, Q: q.Get("q")}
		if st := q.Get("status"); st != "" {
			if !domain.LinkStatus(st).Valid() {
				httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "unknown status")
				return
			}
			f.Status = st
		}
		if ts := q.Get("trust_state"); ts != "" {
			if !domain.TrustState(ts).Valid() {
				httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "unknown trust_state")
				return
			}
			f.TrustState = ts
		}
		page, ok := parsePage(w, r)
		if !ok {
			return
		}
		f.BeforeID, f.Limit = page.before, page.limit+1
		links, err := s.Store.ListLinks(r.Context(), f)
		if err != nil {
			s.fail(w, err, "links")
			return
		}
		items := make([]linkJSON, 0, len(links))
		for _, l := range links {
			items = append(items, s.linkJSON(l))
		}
		writePage(w, items, page.limit, func(i int) int64 { return items[i].ID })
	}
}

func (s *Server) disableLink(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	id, ok := pathID(r)
	if !ok {
		s.fail(w, domain.ErrNotFound, "link")
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "link")
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		v := newValidation()
		v.err("reason", "REASON_REQUIRED", "say why the link is being taken down; the owner and the audit log see it")
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}
	l, err := s.Admin.Disable(r.Context(), p.UserID, id, in.Reason)
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"link": s.linkJSON(l)})
}

func (s *Server) enableLink(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	id, ok := pathID(r)
	if !ok {
		s.fail(w, domain.ErrNotFound, "link")
		return
	}
	l, err := s.Admin.Enable(r.Context(), p.UserID, id)
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"link": s.linkJSON(l)})
}

type blockedJSON struct {
	Domain    string    `json:"domain"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) listBlocklist(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	list, err := s.Store.ListBlocked(r.Context())
	if err != nil {
		s.fail(w, err, "blocklist")
		return
	}
	items := make([]blockedJSON, 0, len(list))
	for _, b := range list {
		items = append(items, blockedJSON{b.Domain, b.Reason, b.CreatedAt.UTC()})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (s *Server) blockDomain(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in struct {
		Domain string `json:"domain"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "blocklist")
		return
	}
	b, err := s.Admin.Block(r.Context(), p.UserID, in.Domain, in.Reason)
	if errors.Is(err, admin.ErrInvalidDomain) {
		v := newValidation()
		v.err("domain", "DOMAIN_INVALID", "enter a domain such as evil.example")
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}
	if err != nil {
		s.fail(w, err, "blocklist")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"blocked_domain": blockedJSON{b.Domain, b.Reason, b.CreatedAt.UTC()}})
}

func (s *Server) unblockDomain(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	if err := s.Admin.Unblock(r.Context(), p.UserID, r.PathValue("domain")); err != nil {
		s.fail(w, err, "blocked domain")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type auditJSON struct {
	ID         int64           `json:"id"`
	ActorID    *int64          `json:"actor_id"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   int64           `json:"entity_id"`
	Detail     json.RawMessage `json:"detail"`
	TS         time.Time       `json:"ts"`
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	page, ok := parsePage(w, r)
	if !ok {
		return
	}
	recs, err := s.Store.ListAudit(r.Context(), page.before, page.limit+1)
	if err != nil {
		s.fail(w, err, "audit log")
		return
	}
	items := make([]auditJSON, 0, len(recs))
	for _, a := range recs {
		items = append(items, auditJSON{a.ID, a.ActorID, a.Action, a.EntityType, a.EntityID, a.Detail, a.TS.UTC()})
	}
	writePage(w, items, page.limit, func(i int) int64 { return items[i].ID })
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ domain.Principal) {
	page, ok := parsePage(w, r)
	if !ok {
		return
	}
	users, err := s.Auth.ListUsers(r.Context(), page.before, page.limit+1)
	if err != nil {
		s.fail(w, err, "users")
		return
	}
	if users == nil {
		users = []auth.AccountSummary{}
	}
	writePage(w, users, page.limit, func(i int) int64 { return users[i].ID })
}
