// Package bio serves public link-in-bio pages at /bio/{slug}: a mobile
// friendly list of a workspace's chosen links. Each entry points at the
// short link, not the destination, so taps are counted, classified and
// checked by the redirect like any other visit.
package bio

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/store"
)

// Pages is what the handler needs from the store.
type Pages interface {
	BioPageBySlug(ctx context.Context, slug string) (store.BioPage, error)
	BioLinks(ctx context.Context, pageID int64) ([]domain.Link, error)
}

// Handler serves bio pages.
type Handler struct {
	Pages   Pages
	BaseURL string
	Log     *slog.Logger
}

// Register mounts GET /bio/{slug}.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /bio/{slug}", h.serve)
}

type entry struct {
	Title, URL string
}

type view struct {
	Title   string
	Links   []entry
	Missing bool
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	page, err := h.Pages.BioPageBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, domain.ErrNotFound) {
		render(w, http.StatusNotFound, view{Title: "Page not found", Missing: true})
		return
	}
	if err != nil {
		h.Log.Error("bio page", "err", err)
		http.Error(w, "something went wrong; please try again", http.StatusInternalServerError)
		return
	}
	links, err := h.Pages.BioLinks(r.Context(), page.ID)
	if err != nil {
		h.Log.Error("bio links", "err", err)
		http.Error(w, "something went wrong; please try again", http.StatusInternalServerError)
		return
	}
	v := view{Title: page.Title, Links: []entry{}}
	if v.Title == "" {
		v.Title = page.Slug
	}
	for _, l := range links {
		title := l.Title
		if title == "" {
			title = l.Code
		}
		v.Links = append(v.Links, entry{Title: title, URL: h.BaseURL + "/" + l.Code})
	}
	render(w, http.StatusOK, v)
}

func render(w http.ResponseWriter, status int, v view) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// Owners edit pages at any time; a minute-old copy is never shown.
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.WriteHeader(status)
	_ = page.Execute(w, v) // the visitor went away; nothing to do
}

var page = template.Must(template.New("bio").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: light dark; --bg: #f6f7f9; --fg: #14171f; --card: #fff; --line: #d8dce3; --accent: #2f5bd3; }
  @media (prefers-color-scheme: dark) { :root { --bg: #111318; --fg: #eef0f4; --card: #1b1e25; --line: #333844; --accent: #8fb0ff; } }
  body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
  main { max-width: 32rem; margin: 0 auto; padding: 2.5rem 1rem; }
  h1 { font-size: 1.5rem; text-align: center; margin: 0 0 1.5rem; overflow-wrap: anywhere; }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: .75rem; }
  a { display: block; padding: .9rem 1rem; border: 1px solid var(--line); border-radius: .75rem; background: var(--card);
      color: inherit; text-decoration: none; text-align: center; font-weight: 600; overflow-wrap: anywhere; }
  a:hover { border-color: var(--accent); }
  a:focus-visible { outline: 3px solid var(--accent); outline-offset: 2px; }
  p { text-align: center; opacity: .75; }
  footer { margin-top: 2rem; text-align: center; font-size: .8rem; opacity: .6; }
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
{{if .Missing}}<p>There is no page at this address.</p>
{{else if .Links}}<ul>
{{range .Links}}<li><a href="{{.URL}}" rel="noopener">{{.Title}}</a></li>
{{end}}</ul>
{{else}}<p>No links here right now.</p>
{{end}}<footer>Short links by PathMaxing</footer>
</main>
</body>
</html>
`))
