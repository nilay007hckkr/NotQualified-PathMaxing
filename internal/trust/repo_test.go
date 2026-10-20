package trust

import (
	"context"
	"testing"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/migrate/pgtest"
)

func TestPGRepo(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	r := PGRepo{Pool: pool}
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO links (workspace_id, owner_id, code, destination) VALUES (1, 1, 'x', 'https://a.example') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	due, err := r.Due(ctx, time.Now(), 10)
	if err != nil || len(due) != 1 || due[0] != id {
		t.Fatalf("a never-checked link must be due: %v, %v", due, err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	rec := CheckRecord{CheckedAt: now, Destination: "https://a.example", HTTPStatus: 200, FinalURL: "https://a.example/", FinalDomain: "a.example", Verdict: VerdictOK, Sources: []string{"blocklist"}}
	st := State{Trust: domain.TrustOK, BaselineHost: "a.example", BaselineDestination: "https://a.example"}
	if err := r.SaveCheck(ctx, id, rec, st, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := r.TrustState(ctx, id)
	if err != nil || got != st {
		t.Fatalf("TrustState = %+v, %v", got, err)
	}
	if due, _ := r.Due(ctx, now, 10); len(due) != 0 {
		t.Fatalf("checked link still due: %v", due)
	}
	if err := r.ScheduleNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	if due, _ := r.Due(ctx, time.Now().Add(time.Second), 10); len(due) != 1 {
		t.Fatalf("forced re-scan not due: %v", due)
	}
	hist, err := r.History(ctx, id, 5)
	if err != nil || len(hist) != 1 || hist[0].Verdict != VerdictOK || hist[0].HTTPStatus != 200 || hist[0].Sources[0] != "blocklist" {
		t.Fatalf("History = %+v, %v", hist, err)
	}

	// A re-scan forced while a check runs survives that check's save.
	started := time.Now().UTC().Add(-time.Second)
	if err := r.ScheduleNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	rec.CheckedAt = started
	if err := r.SaveCheck(ctx, id, rec, st, started.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if due, _ := r.Due(ctx, time.Now().Add(time.Second), 10); len(due) != 1 {
		t.Fatal("a forced re-scan was overwritten by the check that was already running")
	}
	rec.CheckedAt = now

	// Quarantined links are not re-scanned.
	st.Trust = domain.TrustQuarantined
	if err := r.SaveCheck(ctx, id, rec, st, now); err != nil {
		t.Fatal(err)
	}
	if due, _ := r.Due(ctx, now.Add(time.Hour), 10); len(due) != 0 {
		t.Fatalf("quarantined link is due: %v", due)
	}
	var last time.Time
	if err := pool.QueryRow(ctx, `SELECT last_checked_at FROM links WHERE id = $1`, id).Scan(&last); err != nil || !last.Equal(now) {
		t.Fatalf("last_checked_at = %v, %v", last, err)
	}
}
