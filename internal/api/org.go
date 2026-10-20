package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
	"pathmaxing/internal/store"
)

// Campaigns, tags and folders: small per-workspace lists the web app uses as
// pickers. They are listed for the principal's own workspace (an admin's
// picker should not offer other people's campaigns); fetching one by id
// follows the usual rule, so admins can open anyone's.

const maxOrgName = 100

var colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Server) registerOrg(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/campaigns", s.authed(s.listCampaigns))
	mux.HandleFunc("POST /api/campaigns", s.authed(s.createCampaign))
	mux.HandleFunc("GET /api/campaigns/{id}", s.authed(s.getCampaign))
	mux.HandleFunc("PATCH /api/campaigns/{id}", s.authed(s.updateCampaign))
	mux.HandleFunc("DELETE /api/campaigns/{id}", s.authed(s.deleteOrg("campaigns", "campaign", s.ownedCampaignID)))

	mux.HandleFunc("GET /api/tags", s.authed(s.listTags))
	mux.HandleFunc("POST /api/tags", s.authed(s.createTag))
	mux.HandleFunc("GET /api/tags/{id}", s.authed(s.getTag))
	mux.HandleFunc("PATCH /api/tags/{id}", s.authed(s.updateTag))
	mux.HandleFunc("DELETE /api/tags/{id}", s.authed(s.deleteOrg("tags", "tag", s.ownedTagID)))

	mux.HandleFunc("GET /api/folders", s.authed(s.listFolders))
	mux.HandleFunc("POST /api/folders", s.authed(s.createFolder))
	mux.HandleFunc("GET /api/folders/{id}", s.authed(s.getFolder))
	mux.HandleFunc("PATCH /api/folders/{id}", s.authed(s.updateFolder))
	mux.HandleFunc("DELETE /api/folders/{id}", s.authed(s.deleteOrg("folders", "folder", s.ownedFolderID)))
}

// ---- campaigns ----

type campaignJSON struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	UTMSource   string    `json:"utm_source"`
	UTMMedium   string    `json:"utm_medium"`
	UTMCampaign string    `json:"utm_campaign"`
	LinkCount   int64     `json:"link_count"`
	CreatedAt   time.Time `json:"created_at"`
}

func toCampaignJSON(c store.Campaign) campaignJSON {
	return campaignJSON{ID: c.ID, Name: c.Name, UTMSource: c.UTMSource, UTMMedium: c.UTMMedium,
		UTMCampaign: c.UTMCampaign, LinkCount: c.LinkCount, CreatedAt: c.CreatedAt.UTC()}
}

type campaignInput struct {
	Name        opt[string] `json:"name"`
	UTMSource   opt[string] `json:"utm_source"`
	UTMMedium   opt[string] `json:"utm_medium"`
	UTMCampaign opt[string] `json:"utm_campaign"`
	// Read-only fields the web app may send back unchanged.
	ID        opt[int64]  `json:"id"`
	LinkCount opt[int64]  `json:"link_count"`
	CreatedAt opt[string] `json:"created_at"`
}

func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	cs, err := s.Store.ListCampaigns(r.Context(), p.WorkspaceID)
	if err != nil {
		s.fail(w, err, "campaigns")
		return
	}
	items := make([]campaignJSON, 0, len(cs))
	for _, c := range cs {
		items = append(items, toCampaignJSON(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (s *Server) ownedCampaign(ctx context.Context, p domain.Principal, r *http.Request) (store.Campaign, error) {
	id, ok := pathID(r)
	if !ok {
		return store.Campaign{}, domain.ErrNotFound
	}
	c, err := s.Store.GetCampaign(ctx, id)
	if err == nil && !p.CanAccess(c.WorkspaceID) {
		err = domain.ErrNotFound
	}
	return c, err
}

func (s *Server) ownedCampaignID(ctx context.Context, p domain.Principal, r *http.Request) (int64, error) {
	c, err := s.ownedCampaign(ctx, p, r)
	return c.ID, err
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	c, err := s.ownedCampaign(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "campaign")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"campaign": toCampaignJSON(c)})
}

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	s.saveCampaign(w, r, p, store.Campaign{WorkspaceID: p.WorkspaceID}, http.StatusCreated)
}

func (s *Server) updateCampaign(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	c, err := s.ownedCampaign(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "campaign")
		return
	}
	s.saveCampaign(w, r, p, c, http.StatusOK)
}

func (s *Server) saveCampaign(w http.ResponseWriter, r *http.Request, p domain.Principal, c store.Campaign, status int) {
	var in campaignInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "campaign")
		return
	}
	if in.Name.Set || c.ID == 0 {
		c.Name = strings.TrimSpace(in.Name.value())
	}
	for _, f := range []struct {
		in  opt[string]
		dst *string
	}{{in.UTMSource, &c.UTMSource}, {in.UTMMedium, &c.UTMMedium}, {in.UTMCampaign, &c.UTMCampaign}} {
		if f.in.Set {
			*f.dst = strings.TrimSpace(f.in.value())
		}
	}
	v := newValidation()
	checkName(&v, c.Name)
	if !v.ok() {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}
	saved, err := s.Store.SaveCampaign(r.Context(), c)
	if s.nameTaken(w, err) {
		return
	}
	if err != nil {
		s.fail(w, err, "campaign")
		return
	}
	httpx.WriteJSON(w, status, map[string]any{"campaign": toCampaignJSON(saved)})
}

// ---- tags ----

type tagJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type tagInput struct {
	Name  opt[string] `json:"name"`
	Color opt[string] `json:"color"`
	ID    opt[int64]  `json:"id"`
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	ts, err := s.Store.ListTags(r.Context(), p.WorkspaceID)
	if err != nil {
		s.fail(w, err, "tags")
		return
	}
	items := make([]tagJSON, 0, len(ts))
	for _, t := range ts {
		items = append(items, tagJSON{t.ID, t.Name, t.Color})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (s *Server) ownedTag(ctx context.Context, p domain.Principal, r *http.Request) (store.Tag, error) {
	id, ok := pathID(r)
	if !ok {
		return store.Tag{}, domain.ErrNotFound
	}
	t, err := s.Store.GetTag(ctx, id)
	if err == nil && !p.CanAccess(t.WorkspaceID) {
		err = domain.ErrNotFound
	}
	return t, err
}

func (s *Server) ownedTagID(ctx context.Context, p domain.Principal, r *http.Request) (int64, error) {
	t, err := s.ownedTag(ctx, p, r)
	return t.ID, err
}

func (s *Server) getTag(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	t, err := s.ownedTag(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "tag")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tag": tagJSON{t.ID, t.Name, t.Color}})
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	s.saveTag(w, r, store.Tag{WorkspaceID: p.WorkspaceID}, http.StatusCreated)
}

func (s *Server) updateTag(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	t, err := s.ownedTag(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "tag")
		return
	}
	s.saveTag(w, r, t, http.StatusOK)
}

func (s *Server) saveTag(w http.ResponseWriter, r *http.Request, t store.Tag, status int) {
	var in tagInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "tag")
		return
	}
	if in.Name.Set || t.ID == 0 {
		t.Name = strings.TrimSpace(in.Name.value())
	}
	if in.Color.Set {
		t.Color = strings.TrimSpace(in.Color.value())
	}
	v := newValidation()
	checkName(&v, t.Name)
	if t.Color != "" && !colorRE.MatchString(t.Color) {
		v.err("color", "COLOR_INVALID", "color must look like #1a2b3c")
	}
	if !v.ok() {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}
	saved, err := s.Store.SaveTag(r.Context(), t)
	if s.nameTaken(w, err) {
		return
	}
	if err != nil {
		s.fail(w, err, "tag")
		return
	}
	httpx.WriteJSON(w, status, map[string]any{"tag": tagJSON{saved.ID, saved.Name, saved.Color}})
}

// ---- folders ----

type folderJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
}

type folderInput struct {
	Name     opt[string] `json:"name"`
	ParentID opt[int64]  `json:"parent_id"`
	ID       opt[int64]  `json:"id"`
}

func (s *Server) listFolders(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	fs, err := s.Store.ListFolders(r.Context(), p.WorkspaceID)
	if err != nil {
		s.fail(w, err, "folders")
		return
	}
	items := make([]folderJSON, 0, len(fs))
	for _, f := range fs {
		items = append(items, folderJSON{f.ID, f.Name, f.ParentID})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (s *Server) ownedFolder(ctx context.Context, p domain.Principal, r *http.Request) (store.Folder, error) {
	id, ok := pathID(r)
	if !ok {
		return store.Folder{}, domain.ErrNotFound
	}
	f, err := s.Store.GetFolder(ctx, id)
	if err == nil && !p.CanAccess(f.WorkspaceID) {
		err = domain.ErrNotFound
	}
	return f, err
}

func (s *Server) ownedFolderID(ctx context.Context, p domain.Principal, r *http.Request) (int64, error) {
	f, err := s.ownedFolder(ctx, p, r)
	return f.ID, err
}

func (s *Server) getFolder(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	f, err := s.ownedFolder(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "folder")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"folder": folderJSON{f.ID, f.Name, f.ParentID}})
}

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	s.saveFolder(w, r, store.Folder{WorkspaceID: p.WorkspaceID}, http.StatusCreated)
}

func (s *Server) updateFolder(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	f, err := s.ownedFolder(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "folder")
		return
	}
	s.saveFolder(w, r, f, http.StatusOK)
}

func (s *Server) saveFolder(w http.ResponseWriter, r *http.Request, f store.Folder, status int) {
	var in folderInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "folder")
		return
	}
	if in.Name.Set || f.ID == 0 {
		f.Name = strings.TrimSpace(in.Name.value())
	}
	if in.ParentID.Set {
		f.ParentID = in.ParentID.V
	}
	v := newValidation()
	checkName(&v, f.Name)
	if f.ParentID != nil {
		parent, err := s.Store.GetFolder(r.Context(), *f.ParentID)
		switch {
		case errors.Is(err, domain.ErrNotFound) || (err == nil && parent.WorkspaceID != f.WorkspaceID):
			v.err("parent_id", "UNKNOWN_FOLDER", "parent folder not found")
		case err != nil:
			s.fail(w, err, "folder")
			return
		case f.ID != 0:
			// Moving a folder under itself or one of its own subfolders
			// would cut the branch off from the top level.
			up, err := s.Store.FolderAncestors(r.Context(), *f.ParentID)
			if err != nil {
				s.fail(w, err, "folder")
				return
			}
			if slices.Contains(up, f.ID) {
				v.err("parent_id", "FOLDER_CYCLE", "a folder can't be moved inside itself")
			}
		}
	}
	if !v.ok() {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}
	saved, err := s.Store.SaveFolder(r.Context(), f)
	if err != nil {
		s.fail(w, err, "folder")
		return
	}
	httpx.WriteJSON(w, status, map[string]any{"folder": folderJSON{saved.ID, saved.Name, saved.ParentID}})
}

// ---- shared ----

func (s *Server) deleteOrg(table, what string, owned func(context.Context, domain.Principal, *http.Request) (int64, error)) authedFunc {
	return func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
		id, err := owned(r.Context(), p, r)
		if err == nil {
			err = s.Store.DeleteOrg(r.Context(), table, id)
		}
		if err != nil {
			s.fail(w, err, what)
			return
		}
		s.audit(r.Context(), p, what+".deleted", what, id, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func checkName(v *Validation, name string) {
	switch {
	case name == "":
		v.err("name", "NAME_REQUIRED", "name is required")
	case utf8.RuneCountInString(name) > maxOrgName:
		v.err("name", "NAME_TOO_LONG", "name must be at most 100 characters")
	}
}

func (s *Server) nameTaken(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, store.ErrNameTaken) {
		return false
	}
	v := newValidation()
	v.err("name", "NAME_TAKEN", "this name is already used in your workspace")
	httpx.WriteJSON(w, http.StatusConflict, v)
	return true
}

// checkOrganisation makes sure a link only refers to its own workspace's
// campaign, tags and folder.
func (s *Server) checkOrganisation(ctx context.Context, d *draft, v *Validation) {
	ws := d.WorkspaceID
	check := func(table, field, code, msg string, ids []int64) {
		if len(ids) == 0 {
			return
		}
		n, err := s.Store.CountInWorkspace(ctx, table, ws, ids)
		if err != nil {
			s.Log.Error("organisation check", "table", table, "err", err)
			v.err(field, code, msg)
			return
		}
		if n != len(ids) {
			v.err(field, code, msg)
		}
	}
	if d.CampaignID != nil {
		check("campaigns", "campaign_id", "UNKNOWN_CAMPAIGN", "campaign not found", []int64{*d.CampaignID})
	}
	slices.Sort(d.TagIDs)
	d.TagIDs = slices.Compact(d.TagIDs)
	check("tags", "tag_ids", "UNKNOWN_TAG", "one or more tags were not found", d.TagIDs)
	if d.FolderID != nil {
		check("folders", "folder_id", "UNKNOWN_FOLDER", "folder not found", []int64{*d.FolderID})
	}
}
