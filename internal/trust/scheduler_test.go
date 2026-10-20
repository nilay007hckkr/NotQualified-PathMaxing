package trust

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/ratelimit"
)

func TestNextCheck(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	live := domain.Link{Status: domain.StatusActive}
	ok := Transition{Verdict: VerdictOK}
	tests := []struct {
		name   string
		link   domain.Link
		checks int
		t      Transition
		want   time.Duration
	}{
		{"after the creation check: 1 hour", live, 1, ok, time.Hour},
		{"after the second: 24 hours", live, 2, ok, 24 * time.Hour},
		{"live link: daily", live, 3, ok, 24 * time.Hour},
		{"paused link: weekly", domain.Link{Status: domain.StatusPaused}, 3, ok, 7 * 24 * time.Hour},
		{"expired link: weekly", domain.Link{Status: domain.StatusActive, ExpiresAt: &past}, 3, ok, 7 * 24 * time.Hour},
		{"a failure is retried in 30 minutes", live, 5, Transition{Verdict: VerdictFailed}, 30 * time.Minute},
	}
	for _, tt := range tests {
		if got := NextCheck(tt.link, tt.checks, tt.t, now).Sub(now); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSchedulerScansDueLinks(t *testing.T) {
	s, store, _, _ := newScanner(t)
	s.NextCheck = NextCheck
	ctx := context.Background()
	l, _ := store.Put(domain.Link{Code: "x", Destination: "https://shop.example/sale"})
	repo := s.Repo.(*MemRepo)
	repo.ScheduleNow(ctx, l.ID)

	sched := &Scheduler{Scanner: s, Repo: repo, Interval: time.Hour, Concurrency: 2, Batch: 10, Log: s.Log}
	sched.runOnce(ctx)
	hist, _ := repo.History(ctx, l.ID, 5)
	if len(hist) != 1 {
		t.Fatalf("history after a run: %d checks", len(hist))
	}
	next, _ := repo.NextCheckAt(ctx, l.ID)
	if next == nil || next.Sub(s.Now()) != time.Hour {
		t.Fatalf("next check %v, want an hour after the first check", next)
	}
	// Not due any more, so another run does nothing...
	sched.runOnce(ctx)
	if hist, _ := repo.History(ctx, l.ID, 5); len(hist) != 1 {
		t.Fatalf("a link that isn't due was scanned again")
	}
	// ...until a forced re-scan.
	sched.Kick()
	sched.Kick() // a second kick while one is pending is dropped, not blocked on
	repo.ScheduleNow(ctx, l.ID)
	sched.runOnce(ctx)
	if hist, _ := repo.History(ctx, l.ID, 5); len(hist) != 2 {
		t.Fatalf("forced re-scan did not run")
	}
}

// headerGuard authenticates "X-User: <id>:<workspace>" for tests.
type headerGuard struct{}

func (headerGuard) Authenticate(r *http.Request) (domain.Principal, error) {
	v := r.Header.Get("X-User")
	if v == "" {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	ws, _ := strconv.ParseInt(v, 10, 64)
	return domain.Principal{UserID: ws, WorkspaceID: ws, Role: domain.RoleUser}, nil
}

func TestTrustAPI(t *testing.T) {
	s, store, _, _ := newScanner(t)
	ctx := context.Background()
	l, _ := store.Put(domain.Link{Code: "mine", WorkspaceID: 1, Destination: "https://shop.example/sale"})
	if _, err := s.Scan(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	api := &API{
		Guard: headerGuard{}, Links: store, Repo: s.Repo,
		Scheduler:     &Scheduler{Scanner: s, Repo: s.Repo},
		RescanLimiter: ratelimit.New(10, time.Hour),
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	api.Register(mux)
	do := func(method, path, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if user != "" {
			r.Header.Set("X-User", user)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}
	path := "/api/links/" + strconv.FormatInt(l.ID, 10)

	rec := do("GET", path+"/trust", "1")
	var body trustJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 {
		t.Fatalf("trust: %d %s", rec.Code, rec.Body)
	}
	if body.State != domain.TrustOK || len(body.Checks) != 1 || body.Checks[0].HTTPStatus != 200 || body.LastCheckedAt == nil {
		t.Fatalf("trust body %+v", body)
	}

	for _, tc := range []struct {
		method, path, user string
		want               int
	}{
		{"GET", path + "/trust", "", 401},
		{"GET", path + "/trust", "2", 404}, // someone else's link
		{"POST", path + "/rescan", "2", 404},
		{"GET", "/api/links/999/trust", "1", 404},
		{"GET", "/api/links/abc/trust", "1", 404},
	} {
		if rec := do(tc.method, tc.path, tc.user); rec.Code != tc.want {
			t.Errorf("%s %s as %q: %d, want %d", tc.method, tc.path, tc.user, rec.Code, tc.want)
		}
	}

	for i := range 10 {
		if rec := do("POST", path+"/rescan", "1"); rec.Code != http.StatusAccepted || rec.Body.String() != "{\"queued\":true}\n" {
			t.Fatalf("rescan %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	rec = do("POST", path+"/rescan", "1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th rescan: %d", rec.Code)
	}
}
