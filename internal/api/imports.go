package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"pathmaxing/internal/bulk"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/httpx"
	"pathmaxing/internal/shortcode"
	"pathmaxing/internal/store"
)

const (
	codeDupInFile       = "DUP_IN_FILE"
	codeUnknownCampaign = "UNKNOWN_CAMPAIGN"

	// multipartSlack is room for the multipart boundaries and headers around
	// a file of up to bulk.MaxBytes.
	multipartSlack = 64 << 10
	// Threat lookups for a whole file run a few at a time within a budget.
	// Rows not checked in time are imported anyway: the trust scanner checks
	// every new link soon after it is published.
	bulkPrecheckWorkers = 8
	bulkPrecheckBudget  = 20 * time.Second
	statusValid         = "valid"
	statusInvalid       = "invalid"
)

func (s *Server) registerImports(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/imports", s.authed(s.uploadImport))
	mux.HandleFunc("GET /api/imports/{id}", s.authed(s.getImport))
	mux.HandleFunc("POST /api/imports/{id}/commit", s.authed(s.commitImport))
}

type importRowJSON struct {
	Row         int      `json:"row"`
	Alias       string   `json:"alias"`
	Destination string   `json:"destination"`
	Status      string   `json:"status"`
	Errors      []string `json:"errors"`
}

type importJSON struct {
	ID      int64           `json:"id"`
	Status  string          `json:"status"`
	Total   int             `json:"total"`
	Valid   int             `json:"valid"`
	Invalid int             `json:"invalid"`
	Rows    []importRowJSON `json:"rows"`
}

func toImportJSON(j store.ImportJob) importJSON {
	out := importJSON{ID: j.ID, Status: j.Status, Total: len(j.Rows), Rows: make([]importRowJSON, 0, len(j.Rows))}
	for _, r := range j.Rows {
		if r.Status == statusValid {
			out.Valid++
		} else {
			out.Invalid++
		}
		errs := r.Errors
		if errs == nil {
			errs = []string{}
		}
		out.Rows = append(out.Rows, importRowJSON{Row: r.Line, Alias: r.Alias, Destination: r.Destination, Status: r.Status, Errors: errs})
	}
	return out
}

func (s *Server) uploadImport(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	if limited(w, s.ImportLimit, userKey(p)) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, bulk.MaxBytes+multipartSlack)
	if err := r.ParseMultipartForm(bulk.MaxBytes + multipartSlack); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpx.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "the file is larger than 1 MB")
			return
		}
		httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "send the CSV as the multipart field \"file\"")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "send the CSV as the multipart field \"file\"")
		return
	}
	defer f.Close()
	if hdr.Size > bulk.MaxBytes {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "the file is larger than 1 MB")
		return
	}
	rows, err := bulk.Parse(io.LimitReader(f, bulk.MaxBytes))
	switch {
	case errors.Is(err, bulk.ErrTooManyRows):
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "TOO_MANY_ROWS", "the file has more than 1,000 rows; split it into smaller files")
		return
	case err != nil:
		httpx.WriteError(w, http.StatusBadRequest, "BAD_CSV", err.Error())
		return
	}

	preview, err := s.previewRows(r.Context(), p, rows)
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	id, err := s.Store.CreateImport(r.Context(), p.WorkspaceID, p.UserID, preview)
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toImportJSON(store.ImportJob{ID: id, Status: "preview", Rows: preview}))
}

// rowDraft is a CSV row as a link draft.
func rowDraft(workspaceID int64, r store.ImportRow) draft {
	return draft{WorkspaceID: workspaceID, Destination: r.Destination, Alias: r.Alias, Title: r.Title,
		ExpiresAt: r.ExpiresAt, MaxClicks: r.MaxClicks}
}

var rowChecks = checks{destination: true, alias: true, expiry: true, noTrust: true}

// previewRows checks every row with the link validators, plus what only a
// file can get wrong: an alias used twice, and campaigns named that don't
// exist.
func (s *Server) previewRows(ctx context.Context, p domain.Principal, rows []bulk.Row) ([]store.ImportRow, error) {
	var aliases []string
	for _, r := range rows {
		if r.Alias != "" {
			aliases = append(aliases, r.Alias)
		}
	}
	taken, err := s.Store.TakenCodes(ctx, aliases)
	if err != nil {
		return nil, err
	}
	campaigns := map[string]bool{}
	seen := map[string]bool{}
	out := make([]store.ImportRow, len(rows))
	dests := make([]string, len(rows))
	for i, r := range rows {
		row := store.ImportRow{Line: r.Line, Destination: r.Destination, Alias: r.Alias, Title: r.Title,
			Campaign: r.Campaign, Tags: r.Tags, ExpiresAt: r.ExpiresAt, MaxClicks: r.MaxClicks}
		errs := slices.Clone(r.Errors)
		d := rowDraft(p.WorkspaceID, row)
		errs = append(errs, issueCodes(s.validate(ctx, &d, nil, rowChecks).Errors)...)
		dests[i] = d.Destination
		if code, _ := shortcode.CheckAlias(r.Alias); r.Alias != "" && code == "" {
			key := strings.ToLower(r.Alias)
			switch {
			case seen[key]:
				errs = append(errs, codeDupInFile)
			case taken[key]:
				errs = append(errs, codeAliasTaken)
			}
			seen[key] = true
		}
		if r.Campaign != "" {
			key := strings.ToLower(r.Campaign)
			ok, known := campaigns[key]
			if !known {
				_, err := s.Store.CampaignByName(ctx, p.WorkspaceID, r.Campaign)
				if err != nil && !errors.Is(err, domain.ErrNotFound) {
					return nil, err
				}
				ok = err == nil
				campaigns[key] = ok
			}
			if !ok {
				errs = append(errs, codeUnknownCampaign)
			}
		}
		row.Errors = compactCodes(errs)
		row.Status = statusValid
		if len(row.Errors) > 0 {
			row.Status = statusInvalid
		}
		out[i] = row
	}
	s.bulkPrecheck(ctx, out, dests)
	return out, nil
}

// bulkPrecheck runs the threat lookup for the valid rows, a few at a time.
func (s *Server) bulkPrecheck(ctx context.Context, rows []store.ImportRow, dests []string) {
	ctx, cancel := context.WithTimeout(ctx, bulkPrecheckBudget)
	defer cancel()
	sem := make(chan struct{}, bulkPrecheckWorkers)
	var wg sync.WaitGroup
	for i := range rows {
		if rows[i].Status != statusValid {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			v := newValidation()
			s.precheck(ctx, dests[i], false, &v)
			if !v.ok() {
				// Each goroutine owns row i, so no lock is needed.
				rows[i].Errors = append(rows[i].Errors, issueCodes(v.Errors)...)
				rows[i].Status = statusInvalid
			}
		}()
	}
	wg.Wait()
}

func compactCodes(codes []string) []string {
	var out []string
	for _, c := range codes {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) ownedImport(ctx context.Context, p domain.Principal, r *http.Request) (store.ImportJob, error) {
	id, ok := pathID(r)
	if !ok {
		return store.ImportJob{}, domain.ErrNotFound
	}
	j, err := s.Store.GetImport(ctx, id)
	if err == nil && !p.CanAccess(j.WorkspaceID) {
		err = domain.ErrNotFound
	}
	return j, err
}

func (s *Server) getImport(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	j, err := s.ownedImport(r.Context(), p, r)
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toImportJSON(j))
}

type skippedRow struct {
	Row    int      `json:"row"`
	Errors []string `json:"errors"`
}

// commitImport creates the rows that were valid in the preview, each through
// the same path as POST /api/links. Anything that changed since the preview
// (an alias someone else took, a domain blocked meanwhile) skips that row.
func (s *Server) commitImport(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	ctx := r.Context()
	j, err := s.ownedImport(ctx, p, r)
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	if err := s.Store.MarkImportCommitted(ctx, j.ID); errors.Is(err, store.ErrAlreadyCommitted) {
		httpx.WriteError(w, http.StatusConflict, "ALREADY_COMMITTED", "this import was already committed")
		return
	} else if err != nil {
		s.fail(w, err, "import")
		return
	}
	// The links belong to whoever uploaded the file, even if an admin commits.
	owner := domain.Principal{UserID: j.OwnerID, WorkspaceID: j.WorkspaceID, Role: domain.RoleUser}
	created, skipped := 0, []skippedRow{}
	for i, row := range j.Rows {
		if row.Status != statusValid {
			continue
		}
		errs, linkID := s.commitRow(ctx, owner, row)
		if len(errs) > 0 {
			row.Status, row.Errors = statusInvalid, errs
			skipped = append(skipped, skippedRow{Row: row.Line, Errors: errs})
		} else {
			row.LinkID = &linkID
			created++
		}
		j.Rows[i] = row
	}
	if err := s.Store.UpdateImportRows(ctx, j.ID, j.Rows); err != nil {
		s.fail(w, err, "import")
		return
	}
	s.audit(ctx, p, "import.committed", "import", j.ID, map[string]any{"created": created, "skipped": len(skipped)})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"created": created, "skipped": skipped})
}

func (s *Server) commitRow(ctx context.Context, owner domain.Principal, row store.ImportRow) ([]string, int64) {
	d := rowDraft(owner.WorkspaceID, row)
	if errs := issueCodes(s.validate(ctx, &d, nil, rowChecks).Errors); len(errs) > 0 {
		return errs, 0
	}
	if row.Campaign != "" {
		c, err := s.Store.CampaignByName(ctx, owner.WorkspaceID, row.Campaign)
		if err != nil {
			return []string{codeUnknownCampaign}, 0
		}
		d.CampaignID = &c.ID
	}
	if len(row.Tags) > 0 {
		ids, err := s.Store.EnsureTags(ctx, owner.WorkspaceID, row.Tags)
		if err != nil {
			s.Log.Error("import tags", "err", err)
			return []string{"INTERNAL"}, 0
		}
		d.TagIDs = ids
	}
	l, err := s.insertLink(ctx, owner, d, "")
	if errors.Is(err, store.ErrAliasTaken) {
		return []string{codeAliasTaken}, 0
	}
	if err != nil {
		s.Log.Error("import row", "row", row.Line, "err", err)
		return []string{"INTERNAL"}, 0
	}
	return nil, l.ID
}
