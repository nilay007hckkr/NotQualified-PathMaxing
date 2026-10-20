package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
	"pathmaxing/internal/qr"
	"pathmaxing/internal/shortcode"
	"pathmaxing/internal/store"
)

const maxBioLinks = 50

func (s *Server) registerQRBio(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/links/{id}/qr", s.authed(s.linkQR))
	mux.HandleFunc("GET /api/bio-pages", s.authed(s.listBioPages))
	mux.HandleFunc("POST /api/bio-pages", s.authed(s.createBioPage))
	mux.HandleFunc("GET /api/bio-pages/{id}", s.authed(s.getBioPage))
	mux.HandleFunc("PATCH /api/bio-pages/{id}", s.authed(s.updateBioPage))
}

// linkQR draws the link's QR code. It encodes ?s=qr so scans are counted as
// their own source.
func (s *Server) linkQR(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	l, err := s.ownedLink(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "link")
		return
	}
	q := r.URL.Query()
	size := qr.DefaultSize
	if raw := q.Get("size"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < qr.MinSize || n > qr.MaxSize {
			httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "size must be between 128 and 1024")
			return
		}
		size = n
	}
	content := s.BaseURL + "/" + l.Code + "?s=qr"
	var (
		img   []byte
		ctype string
		ext   string
	)
	switch q.Get("format") {
	case "", "png":
		img, err = qr.PNG(content, size)
		ctype, ext = "image/png", "png"
	case "svg":
		img, err = qr.SVG(content, size)
		ctype, ext = "image/svg+xml", "svg"
	default:
		httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "format must be png or svg")
		return
	}
	if err != nil {
		s.fail(w, err, "qr code")
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	// A link's code never changes, so neither does its QR code.
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Disposition", `inline; filename="`+l.Code+`-qr.`+ext+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(img)
}

type bioJSON struct {
	ID      int64   `json:"id"`
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	LinkIDs []int64 `json:"link_ids"`
}

func toBioJSON(b store.BioPage) bioJSON {
	ids := b.LinkIDs
	if ids == nil {
		ids = []int64{}
	}
	return bioJSON{ID: b.ID, Slug: b.Slug, Title: b.Title, LinkIDs: ids}
}

type bioInput struct {
	Slug    opt[string]  `json:"slug"`
	Title   opt[string]  `json:"title"`
	LinkIDs opt[[]int64] `json:"link_ids"`
	ID      opt[int64]   `json:"id"`
}

func (s *Server) listBioPages(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	pages, err := s.Store.ListBioPages(r.Context(), p.WorkspaceID)
	if err != nil {
		s.fail(w, err, "bio pages")
		return
	}
	items := make([]bioJSON, 0, len(pages))
	for _, b := range pages {
		items = append(items, toBioJSON(b))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (s *Server) ownedBioPage(ctx context.Context, p domain.Principal, r *http.Request) (store.BioPage, error) {
	id, ok := pathID(r)
	if !ok {
		return store.BioPage{}, domain.ErrNotFound
	}
	b, err := s.Store.GetBioPage(ctx, id)
	if err == nil && !p.CanAccess(b.WorkspaceID) {
		err = domain.ErrNotFound
	}
	return b, err
}

func (s *Server) getBioPage(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	b, err := s.ownedBioPage(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "bio page")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"bio_page": toBioJSON(b)})
}

func (s *Server) createBioPage(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	s.saveBioPage(w, r, store.BioPage{WorkspaceID: p.WorkspaceID}, http.StatusCreated)
}

func (s *Server) updateBioPage(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	b, err := s.ownedBioPage(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "bio page")
		return
	}
	s.saveBioPage(w, r, b, http.StatusOK)
}

func (s *Server) saveBioPage(w http.ResponseWriter, r *http.Request, b store.BioPage, status int) {
	var in bioInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		s.fail(w, err, "bio page")
		return
	}
	if in.Slug.Set || b.ID == 0 {
		b.Slug = strings.TrimSpace(in.Slug.value())
	}
	if in.Title.Set {
		b.Title = strings.TrimSpace(in.Title.value())
	}
	if in.LinkIDs.Set {
		b.LinkIDs = in.LinkIDs.value()
	}
	v := newValidation()
	// Slugs follow the alias rules, reserved words included, so a slug never
	// reads like one of the server's own paths.
	if code, msg := shortcode.CheckAlias(b.Slug); code != "" {
		v.err("slug", "SLUG_INVALID", strings.Replace(msg, "alias", "slug", 1))
	}
	if utf8.RuneCountInString(b.Title) > maxOrgName {
		v.err("title", "TITLE_TOO_LONG", "title must be at most 100 characters")
	}
	// Order matters on the page, so duplicates are refused rather than merged.
	sorted := slices.Clone(b.LinkIDs)
	slices.Sort(sorted)
	switch {
	case len(b.LinkIDs) > maxBioLinks:
		v.err("link_ids", "TOO_MANY_LINKS", "a bio page can list at most 50 links")
	case len(slices.Compact(sorted)) != len(b.LinkIDs):
		v.err("link_ids", "DUPLICATE_LINK", "a link can appear only once on a page")
	case len(b.LinkIDs) > 0:
		n, err := s.Store.LiveLinksInWorkspace(r.Context(), b.WorkspaceID, b.LinkIDs)
		if err != nil {
			s.fail(w, err, "bio page")
			return
		}
		if n != len(b.LinkIDs) {
			v.err("link_ids", "UNKNOWN_LINK", "one or more links were not found")
		}
	}
	if !v.ok() {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, v)
		return
	}
	saved, err := s.Store.SaveBioPage(r.Context(), b)
	if errors.Is(err, store.ErrSlugTaken) {
		v.err("slug", "SLUG_TAKEN", "this slug is already taken")
		httpx.WriteJSON(w, http.StatusConflict, v)
		return
	}
	if err != nil {
		s.fail(w, err, "bio page")
		return
	}
	httpx.WriteJSON(w, status, map[string]any{"bio_page": toBioJSON(saved)})
}
