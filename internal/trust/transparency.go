package trust

import (
	"bytes"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"pathmaxing/internal/domain"
)

//go:embed transparency.html
var transparencyHTML string

var transparencyTmpl = template.Must(template.New("p").Parse(transparencyHTML))

// Transparency serves GET /p/{code}: before following a short link, anyone
// can see where it goes and what the last trust check found. It shows only
// the destination's host, never the full URL, which may carry the owner's
// tracking parameters.
type Transparency struct {
	Links domain.LinkResolver
	Repo  Repo
	Log   *slog.Logger
}

// Register mounts the page.
func (t *Transparency) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /p/{code}", t.page)
}

type transparencyView struct {
	Code, Host, State, StateLabel, Tone, Checked, Verdict, Live string
	Found                                                       bool
}

var ist = time.FixedZone("IST", 5*3600+1800)

func (t *Transparency) page(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	v := transparencyView{Code: code}
	l, err := t.Links.ResolveCode(r.Context(), code)
	status := http.StatusOK
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case err != nil:
		t.Log.Error("transparency page", "code", code, "err", err)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	default:
		v.Found = true
		v.Code = l.Code
		if u, err := url.Parse(l.Destination); err == nil {
			v.Host = u.Hostname()
		}
		v.State = string(l.TrustState)
		v.StateLabel, v.Tone = stateLabel(l.TrustState)
		v.Live = liveLabel(l, time.Now())
		v.Checked = "Not checked yet"
		if l.LastCheckedAt != nil {
			v.Checked = l.LastCheckedAt.In(ist).Format("2 Jan 2006, 15:04 IST")
		}
		if hist, err := t.Repo.History(r.Context(), l.ID, 1); err == nil && len(hist) > 0 {
			v.Verdict = hist[0].Verdict
		}
	}
	var buf bytes.Buffer
	if err := transparencyTmpl.Execute(&buf, v); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func stateLabel(s domain.TrustState) (label, tone string) {
	switch s {
	case domain.TrustQuarantined:
		return "Quarantined: a threat feed or the blocklist flagged it", "danger"
	case domain.TrustDegraded:
		return "Degraded: the destination stopped answering", "warn"
	case domain.TrustChanged:
		return "Changed: it now ends on a different site", "warn"
	}
	return "OK: the last check found nothing wrong", "ok"
}

func liveLabel(l domain.Link, now time.Time) string {
	switch {
	case l.Deleted(), l.Status == domain.StatusPaused, l.Status == domain.StatusArchived:
		return "Inactive"
	case l.Status == domain.StatusDisabledAdmin:
		return "Disabled by an administrator"
	case l.IsExpired(now):
		return "Expired"
	case l.NotStarted(now):
		return "Not live yet"
	}
	return "Live"
}
