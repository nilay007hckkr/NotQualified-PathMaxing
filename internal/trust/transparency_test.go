package trust

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pathmaxing/internal/domain"
)

func TestTransparencyPage(t *testing.T) {
	s, store, _, _ := newScanner(t)
	l, _ := store.Put(domain.Link{Code: "Promo", Destination: "https://shop.example/sale?utm_source=secret"})
	if _, err := s.Scan(context.Background(), l.ID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&Transparency{Links: store, Repo: s.Repo, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/p/promo", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "shop.example") || !strings.Contains(body, "OK: the last check") || !strings.Contains(body, "IST") {
		t.Fatalf("page = %d\n%s", rec.Code, body)
	}
	// Only the host is shown: the full URL can hold the owner's tracking tags.
	if strings.Contains(body, "utm_source") {
		t.Fatal("the page leaks the destination's query string")
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/p/missing", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "No such link") {
		t.Fatalf("missing = %d", rec.Code)
	}
}
