package alerts

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/migrate/pgtest"
)

type guard struct{ p domain.Principal }

func (g guard) Authenticate(*http.Request) (domain.Principal, error) {
	if g.p.UserID == 0 {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return g.p, nil
}

func TestAlerts(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	s := &Store{Pool: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	insert := func(code string, ws int64, extra string) int64 {
		var id int64
		err := pool.QueryRow(ctx, `INSERT INTO links (workspace_id, owner_id, code, destination) VALUES ($1, 1, $2, 'https://a.example') RETURNING id`, ws, code).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		if extra != "" {
			if _, err := pool.Exec(ctx, `UPDATE links SET `+extra+` WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	capped := insert("capped", 1, "max_clicks = 10, cap_used = 9")
	expiring := insert("soon", 1, "expires_at = now() + interval '3 hours'")
	insert("calm", 1, "")
	busy := insert("busy", 2, "")
	// 30 human clicks in the last few minutes on a link with no history.
	if _, err := pool.Exec(ctx, `
		INSERT INTO click_events (event_id, link_id, workspace_id, ts, outcome, traffic_class, class_reason, source, method)
		SELECT gen_random_uuid(), $1, 2, now() - (g || ' seconds')::interval, 'redirected', 'human', 'browser_user_agent', 'link', 'GET'
		FROM generate_series(1, 30) g`, busy); err != nil {
		t.Fatal(err)
	}

	for range 2 { // twice: alerts must not be raised twice
		if err := s.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.List(ctx, 0, true, false, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[int64]string{}
	for _, a := range all {
		kinds[a.LinkID] = a.Kind
	}
	if len(all) != 3 || kinds[capped] != KindNearCap || kinds[expiring] != KindExpiring || kinds[busy] != KindSpike {
		t.Fatalf("alerts = %+v", all)
	}

	// Trust alerts open and close with the state.
	l := domain.Link{ID: capped, WorkspaceID: 1, Code: "capped"}
	s.OnTrust(ctx, l, domain.TrustQuarantined, "blocklist: evil.example")
	if n, _ := s.CountOpenAlerts(ctx, 1, false); n != 3 {
		t.Fatalf("workspace 1 open alerts = %d, want 3", n)
	}
	s.OnTrust(ctx, l, domain.TrustOK, "")
	if n, _ := s.CountOpenAlerts(ctx, 1, false); n != 2 {
		t.Fatalf("after release = %d, want 2", n)
	}

	// The API is scoped to the caller's workspace.
	mux := http.NewServeMux()
	(&API{Guard: guard{domain.Principal{UserID: 5, WorkspaceID: 2, Role: domain.RoleUser}}, Store: s}).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/alerts?status=open", nil))
	var body struct{ Items []Alert }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || len(body.Items) != 1 || body.Items[0].Kind != KindSpike {
		t.Fatalf("GET /api/alerts = %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/alerts?status=closed", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status = %d", rec.Code)
	}
	noAuth := http.NewServeMux()
	(&API{Guard: guard{}, Store: s}).Register(noAuth)
	rec = httptest.NewRecorder()
	noAuth.ServeHTTP(rec, httptest.NewRequest("GET", "/api/alerts", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rec.Code)
	}
}
