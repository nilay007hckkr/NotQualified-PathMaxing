package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
	"pathmaxing/internal/shortcode"
	"pathmaxing/internal/store"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

func (s *Server) registerLinks(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/links/validate", s.authed(s.validateLink))
	mux.HandleFunc("POST /api/links", s.authed(s.createLink))
	mux.HandleFunc("GET /api/links", s.authed(s.listLinks))
	mux.HandleFunc("GET /api/links/{id}", s.authed(s.getLink))
	mux.HandleFunc("PATCH /api/links/{id}", s.authed(s.updateLink))
	mux.HandleFunc("POST /api/links/{id}/pause", s.authed(s.setStatus(domain.StatusPaused, "link.paused")))
	mux.HandleFunc("POST /api/links/{id}/resume", s.authed(s.setStatus(domain.StatusActive, "link.resumed")))
	mux.HandleFunc("POST /api/links/{id}/archive", s.authed(s.setStatus(domain.StatusArchived, "link.archived")))
	mux.HandleFunc("DELETE /api/links/{id}", s.authed(s.deleteLink))
}

// validateLink is step 1 of the creation wizard: every check, including the
// reachability probe and an early look at the alias, and nothing is saved.
func (s *Server) validateLink(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in linkInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "link")
		return
	}
	d, targets := fromInput(in)
	c := createChecks
	c.aliasTaken, c.probe = true, true
	httpx.WriteJSON(w, http.StatusOK, s.validate(r.Context(), p, &d, targets, c))
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in linkInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "link")
		return
	}
	d, targets := fromInput(in)
	v := s.validate(r.Context(), p, &d, targets, createChecks)
	if !v.ok() {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}
	l, err := s.insertLink(r.Context(), p, d, in.Password.value())
	if errors.Is(err, store.ErrAliasTaken) {
		// Validate said the alias was free, but someone else got it first:
		// the unique index decided.
		v.err("alias", codeAliasTaken, "alias is already taken")
		httpx.WriteJSON(w, http.StatusConflict, v)
		return
	}
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"link": s.linkJSON(l), "warnings": v.Warnings})
}

// insertLink saves a validated draft. Bulk import commits through it too.
func (s *Server) insertLink(ctx context.Context, p domain.Principal, d draft, password string) (domain.Link, error) {
	nl := store.NewLink{
		WorkspaceID: p.WorkspaceID, OwnerID: p.UserID, Alias: d.Alias, Destination: d.Destination,
		Title: d.Title, StartsAt: d.StartsAt, ExpiresAt: d.ExpiresAt, MaxClicks: d.MaxClicks, Targets: d.Targets,
	}
	if password != "" {
		hash, err := s.Auth.HashPassword(password)
		if err != nil {
			return domain.Link{}, err
		}
		nl.PasswordHash = hash
	}
	l, err := s.Store.CreateLink(ctx, nl)
	if err != nil {
		return domain.Link{}, err
	}
	// The code may have been cached as unknown by an earlier visit.
	s.Cache.Invalidate(l.Code)
	s.audit(ctx, p, "link.created", "link", l.ID, map[string]any{"code": l.Code, "destination": l.Destination})
	return l, nil
}

func (s *Server) listLinks(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	q := r.URL.Query()
	f := store.LinkFilter{WorkspaceID: p.WorkspaceID, All: p.IsAdmin(), Q: q.Get("q")}
	if st := q.Get("status"); st != "" {
		if !domain.LinkStatus(st).Valid() {
			httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "unknown status "+strconv.Quote(st))
			return
		}
		f.Status = st
	}
	if ts := q.Get("trust_state"); ts != "" {
		if !domain.TrustState(ts).Valid() {
			httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "unknown trust_state "+strconv.Quote(ts))
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

type page struct {
	before int64
	limit  int
}

// parsePage reads limit and the opaque cursor, which is the last id of the
// previous page in base64.
func parsePage(w http.ResponseWriter, r *http.Request) (page, bool) {
	p := page{limit: defaultPageSize}
	q := r.URL.Query()
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "limit must be a positive number")
			return p, false
		}
		p.limit = min(n, maxPageSize)
	}
	if c := q.Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(c)
		id, perr := strconv.ParseInt(string(b), 10, 64)
		if err != nil || perr != nil || id < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid cursor")
			return p, false
		}
		p.before = id
	}
	return p, true
}

// writePage answers {"items", "next_cursor"}. items holds up to limit+1
// entries: the extra one only shows that another page exists.
func writePage[T any](w http.ResponseWriter, items []T, limit int, idOf func(i int) int64) {
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(idOf(limit-1), 10)))
		next = &c
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// ownedLink loads a live link the principal may see. Anyone else's link, or
// a deleted one, is simply not found.
func (s *Server) ownedLink(ctx context.Context, p domain.Principal, r *http.Request) (domain.Link, error) {
	id, ok := pathID(r)
	if !ok {
		return domain.Link{}, domain.ErrNotFound
	}
	l, err := s.Store.GetLink(ctx, id)
	if err != nil {
		return domain.Link{}, err
	}
	if !p.CanAccess(l.WorkspaceID) || l.Deleted() {
		return domain.Link{}, domain.ErrNotFound
	}
	return l, nil
}

func (s *Server) getLink(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	l, err := s.ownedLink(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"link": s.linkJSON(l)})
}

func (s *Server) updateLink(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	l, err := s.ownedLink(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	var in linkInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "link")
		return
	}

	d := draft{
		Destination: l.Destination, Title: l.Title, StartsAt: l.StartsAt, ExpiresAt: l.ExpiresAt,
		MaxClicks: l.MaxClicks, HasPassword: l.HasPassword(), Targets: l.Targets,
		CampaignID: l.CampaignID, FolderID: l.FolderID, TagIDs: l.TagIDs,
	}
	var c checks
	var rawTargets []targetJSON
	if in.Destination.Set {
		d.Destination, c.destination = in.Destination.value(), true
	}
	if in.Title.Set {
		d.Title = in.Title.value()
	}
	if in.StartsAt.Set {
		d.StartsAt = in.StartsAt.V
	}
	if in.ExpiresAt.Set {
		d.ExpiresAt, c.expiry = in.ExpiresAt.V, true
	}
	if in.MaxClicks.Set {
		d.MaxClicks = in.MaxClicks.V
	}
	if in.Password.Set {
		d.HasPassword = in.Password.value() != ""
	}
	if in.Targets.Set {
		rawTargets, c.targets = in.Targets.value(), true
	}
	if in.CampaignID.Set {
		d.CampaignID = in.CampaignID.V
	}
	if in.TagIDs.Set {
		d.TagIDs = in.TagIDs.value()
	}
	if in.FolderID.Set {
		d.FolderID = in.FolderID.V
	}
	v := s.validate(r.Context(), p, &d, rawTargets, c)
	// Codes are permanent: shared links and printed QR codes point at them.
	if in.Alias.Set && !strings.EqualFold(in.Alias.value(), l.Code) {
		v.err("alias", shortcode.CodeAliasInvalid, "a link's code can't be changed; create a new link instead")
	}
	if !v.ok() {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}

	u := store.LinkUpdate{
		Destination: d.Destination, Title: d.Title, StartsAt: d.StartsAt, ExpiresAt: d.ExpiresAt,
		MaxClicks: d.MaxClicks, PasswordHash: l.PasswordHash, Targets: d.Targets,
	}
	if in.Password.Set {
		u.PasswordHash = nil
		if pw := in.Password.value(); pw != "" {
			if u.PasswordHash, err = s.Auth.HashPassword(pw); err != nil {
				s.fail(w, err, "link")
				return
			}
		}
	}
	updated, err := s.Store.UpdateLink(r.Context(), l.ID, u)
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	s.Cache.Invalidate(updated.Code)
	s.audit(r.Context(), p, "link.updated", "link", l.ID, map[string]any{"fields": changedFields(in)})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"link": s.linkJSON(updated)})
}

// changedFields lists what a PATCH touched, for the audit log; the password
// itself never goes there.
func changedFields(in linkInput) []string {
	var out []string
	for name, set := range map[string]bool{
		"destination": in.Destination.Set, "title": in.Title.Set, "starts_at": in.StartsAt.Set,
		"expires_at": in.ExpiresAt.Set, "max_clicks": in.MaxClicks.Set, "password": in.Password.Set,
		"targets": in.Targets.Set, "campaign_id": in.CampaignID.Set, "tag_ids": in.TagIDs.Set, "folder_id": in.FolderID.Set,
	} {
		if set {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func (s *Server) setStatus(status domain.LinkStatus, action string) authedFunc {
	return func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
		l, err := s.ownedLink(r.Context(), p, r)
		if err != nil {
			s.fail(w, err, "link")
			return
		}
		updated, err := s.Store.SetStatus(r.Context(), l.ID, status, "", false)
		if errors.Is(err, store.ErrDisabledByAdmin) {
			httpx.WriteError(w, http.StatusConflict, "LINK_DISABLED", "an admin disabled this link; it can't be changed until they enable it")
			return
		}
		if err != nil {
			s.fail(w, err, "link")
			return
		}
		s.Cache.Invalidate(updated.Code)
		s.audit(r.Context(), p, action, "link", l.ID, nil)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"link": s.linkJSON(updated)})
	}
}

func (s *Server) deleteLink(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	l, err := s.ownedLink(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	deleted, err := s.Store.DeleteLink(r.Context(), l.ID)
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	s.Cache.Invalidate(deleted.Code)
	s.audit(r.Context(), p, "link.deleted", "link", l.ID, map[string]any{"code": deleted.Code})
	w.WriteHeader(http.StatusNoContent)
}

// audit records who did what. A failed audit write is logged, not returned:
// the change it describes has already been committed.
func (s *Server) audit(ctx context.Context, p domain.Principal, action, entity string, id int64, detail map[string]any) {
	actor := p.UserID
	err := s.Store.Append(ctx, domain.AuditEntry{ActorID: &actor, Action: action, EntityType: entity, EntityID: id, Detail: detail})
	if err != nil {
		s.Log.Error("audit write failed", "action", action, "entity_id", id, "err", err)
	}
}
