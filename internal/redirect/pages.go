package redirect

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"pathmaxing/internal/domain"
)

//go:embed page.html
var pageFS embed.FS

var pageTmpl = template.Must(template.ParseFS(pageFS, "page.html"))

// page is everything page.html can show.
type page struct {
	Title    string
	Heading  string
	Message  string
	Tone     string // "info", "warn" or "danger": the page's accent color
	Code     string
	Host     string // destination host on warning pages; never a clickable link
	Password bool   // show the password form
	Error    string // e.g. "That password is not right."
	QR       bool   // keep ?s=qr on the form so the unlocked visit still counts as a scan
}

// pageFor describes a refusal for a visitor.
func pageFor(o domain.Outcome) page {
	switch o {
	case domain.OutcomeQuarantined:
		return page{Title: "Warning: unsafe link", Heading: "This link was flagged as unsafe",
			Message: "A threat-intelligence feed lists this link's destination as malicious, so PathMaxing has blocked it. Do not try to open the destination another way.",
			Tone:    "danger"}
	case domain.OutcomeBlockedAdmin:
		return page{Title: "Link disabled", Heading: "This link was disabled",
			Message: "A PathMaxing administrator took this link down.", Tone: "danger"}
	case domain.OutcomeBlockedDomain:
		return page{Title: "Destination blocked", Heading: "This link's destination is blocked",
			Message: "The website this link points to is on PathMaxing's blocklist.", Tone: "danger"}
	case domain.OutcomeInactive:
		return page{Title: "Link inactive", Heading: "This link is no longer active",
			Message: "Its owner has paused, archived or deleted it.", Tone: "info"}
	case domain.OutcomeExpired:
		return page{Title: "Link expired", Heading: "This link has expired", Message: "Its owner set it to stop working at a certain time.", Tone: "info"}
	case domain.OutcomeCapReached:
		return page{Title: "Link limit reached", Heading: "This link has reached its click limit",
			Message: "Its owner limited how many times it can be used.", Tone: "info"}
	case domain.OutcomeNotStarted:
		return page{Title: "Not found", Heading: "This link isn’t available", Message: "Check the address, or try again later.", Tone: "info"}
	}
	return notFoundPage()
}

func notFoundPage() page {
	return page{Title: "Not found", Heading: "There’s no link here", Message: "Check the address for typos. Short links ignore letter case.", Tone: "info"}
}

func unavailablePage() page {
	return page{Title: "Temporarily unavailable", Heading: "Something went wrong on our side", Message: "Please try again in a moment.", Tone: "warn"}
}

func passwordPage(code string, qr bool, errMsg string) page {
	return page{Title: "Password required", Heading: "This link is password-protected",
		Message: "Enter the password its owner gave you.", Tone: "info",
		Code: code, Password: true, Error: errMsg, QR: qr}
}

// renderPage writes p with status. Every page is private and uncacheable,
// and kept out of search engines, like the redirects themselves.
func renderPage(w http.ResponseWriter, status int, p page) {
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, p); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func retryAfter(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())))
}
