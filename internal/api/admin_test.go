package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"pathmaxing/internal/analytics"
	"pathmaxing/internal/domain"
	"pathmaxing/internal/ratelimit"
	"pathmaxing/internal/rules"
)

type fakeAlerts struct{}

func (fakeAlerts) CountOpenAlerts(_ context.Context, workspaceID int64, all bool) (int, error) {
	if all {
		return 7, nil
	}
	return int(workspaceID), nil
}

func TestAdminRoleCheck(t *testing.T) {
	e := newEnv(t)
	user, _ := e.signup("user@example.com")
	routes := []struct{ method, path string }{
		{"GET", "/api/admin/links"}, {"GET", "/api/admin/abuse"}, {"POST", "/api/admin/links/1/disable"},
		{"POST", "/api/admin/links/1/enable"}, {"GET", "/api/admin/blocklist"}, {"POST", "/api/admin/blocklist"},
		{"DELETE", "/api/admin/blocklist/evil.example"}, {"GET", "/api/admin/audit"}, {"GET", "/api/admin/users"},
		{"GET", "/api/admin/analytics/overview"},
	}
	for _, rt := range routes {
		if r := user.do(rt.method, rt.path, map[string]string{}); r.status != http.StatusForbidden || r.errorCode(t) != "FORBIDDEN" {
			t.Errorf("%s %s as a user = %d %s", rt.method, rt.path, r.status, r.body)
		}
		if r := e.client().do(rt.method, rt.path, map[string]string{}); r.status != http.StatusUnauthorized {
			t.Errorf("%s %s logged out = %d", rt.method, rt.path, r.status)
		}
	}
}

func TestTakedownAndRelease(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.signup("owner@example.com")
	admin := e.admin("root@example.com")
	l := owner.create(t, map[string]any{"destination": "https://phish.example/login", "alias": "phishy"})
	ok := owner.create(t, map[string]any{"destination": "https://fine.example"})
	id := strconv.FormatInt(l.ID, 10)

	var page struct{ Items []linkJSON }
	admin.do("GET", "/api/admin/links?q=phish", nil).json(t, &page)
	if len(page.Items) != 1 || page.Items[0].ID != l.ID {
		t.Fatalf("admin search = %+v", page.Items)
	}

	if r := admin.do("POST", "/api/admin/links/"+id+"/disable", map[string]string{"reason": " "}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("disable without reason = %d", r.status)
	}
	r := admin.do("POST", "/api/admin/links/"+id+"/disable", map[string]string{"reason": "phishing report #12"})
	var out struct{ Link linkJSON }
	r.json(t, &out)
	if r.status != http.StatusOK || out.Link.Status != "disabled_admin" {
		t.Fatalf("disable = %d %s", r.status, r.body)
	}
	engine := &rules.Engine{Blocklist: e.store, Caps: e.store}
	decide := func() domain.Outcome {
		got, _ := e.store.ResolveCode(context.Background(), "phishy")
		d, err := engine.Decide(context.Background(), got, rules.Visit{Human: true, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return d.Outcome
	}
	if got := decide(); got != domain.OutcomeBlockedAdmin {
		t.Fatalf("after takedown the redirect says %s", got)
	}
	if r := owner.do("POST", "/api/links/"+id+"/resume", nil); r.status != http.StatusConflict {
		t.Fatalf("owner resume after takedown = %d", r.status)
	}

	// The scanner quarantined it meanwhile; enabling releases that too.
	if _, err := e.pool.Exec(context.Background(), `UPDATE links SET trust_state = 'quarantined' WHERE id = $1`, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE links SET trust_state = 'changed' WHERE id = $1`, ok.ID); err != nil {
		t.Fatal(err)
	}
	admin.do("GET", "/api/admin/abuse", nil).json(t, &page)
	if len(page.Items) != 2 {
		t.Fatalf("abuse queue = %d links, want the disabled and the changed one", len(page.Items))
	}
	r = admin.do("POST", "/api/admin/links/"+id+"/enable", nil)
	r.json(t, &out)
	if r.status != http.StatusOK || out.Link.Status != "active" || out.Link.TrustState != "ok" {
		t.Fatalf("enable = %d %s", r.status, r.body)
	}
	if got := decide(); got != domain.OutcomeRedirected {
		t.Fatalf("after enable the redirect says %s", got)
	}
	if n := len(slices.DeleteFunc(e.cache.invalidated(), func(c string) bool { return c != "phishy" })); n < 3 {
		t.Fatalf("cache invalidated %d times; want create, disable and enable", n)
	}
	if r := admin.do("POST", "/api/admin/links/999999/enable", nil); r.status != http.StatusNotFound {
		t.Fatalf("enable unknown = %d", r.status)
	}

	var audit struct {
		Items      []auditJSON
		NextCursor *string `json:"next_cursor"`
	}
	admin.do("GET", "/api/admin/audit?limit=3", nil).json(t, &audit)
	actions := []string{}
	for _, a := range audit.Items {
		actions = append(actions, a.Action)
	}
	if !slices.Equal(actions, []string{"link.released", "link.enabled", "link.disabled"}) || audit.NextCursor == nil {
		t.Fatalf("latest audit entries = %v, cursor %v", actions, audit.NextCursor)
	}
	var detail map[string]any
	_ = json.Unmarshal(audit.Items[2].Detail, &detail)
	if detail["reason"] != "phishing report #12" || audit.Items[2].ActorID == nil || audit.Items[2].EntityID != l.ID {
		t.Fatalf("takedown audit = %+v", audit.Items[2])
	}
	admin.do("GET", "/api/admin/audit?limit=3&cursor="+*audit.NextCursor, nil).json(t, &audit)
	if len(audit.Items) == 0 || audit.Items[0].Action != "link.created" {
		t.Fatalf("second audit page = %+v", audit.Items)
	}
}

func TestBlocklistAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin("root@example.com")
	user, _ := e.signup("user@example.com")

	r := admin.do("POST", "/api/admin/blocklist", map[string]string{"domain": "https://Evil.Example/login", "reason": "phishing"})
	var out struct {
		BlockedDomain blockedJSON `json:"blocked_domain"`
	}
	r.json(t, &out)
	if r.status != http.StatusCreated || out.BlockedDomain.Domain != "evil.example" || out.BlockedDomain.Reason != "phishing" {
		t.Fatalf("block = %d %s", r.status, r.body)
	}
	// Effective at once, subdomains included, without waiting for a reload.
	v := user.do("POST", "/api/links/validate", map[string]string{"destination": "https://login.evil.example/"})
	var res Validation
	v.json(t, &res)
	if !slices.Equal(codes(res.Errors), []string{"BLOCKED_DOMAIN"}) {
		t.Fatalf("validate after block = %s", v.body)
	}
	if _, blocked, _ := e.store.Blocked(context.Background(), "a.evil.example"); !blocked {
		t.Fatal("the redirect's blocklist was not reloaded")
	}

	if r := admin.do("POST", "/api/admin/blocklist", map[string]string{"domain": "not a domain"}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid domain = %d", r.status)
	}
	var list struct{ Items []blockedJSON }
	admin.do("GET", "/api/admin/blocklist", nil).json(t, &list)
	if len(list.Items) != 1 {
		t.Fatalf("blocklist = %+v", list.Items)
	}
	if r := admin.do("DELETE", "/api/admin/blocklist/EVIL.example", nil); r.status != http.StatusNoContent {
		t.Fatalf("unblock = %d", r.status)
	}
	if r := admin.do("DELETE", "/api/admin/blocklist/evil.example", nil); r.status != http.StatusNotFound {
		t.Fatalf("unblock twice = %d", r.status)
	}
	if _, blocked, _ := e.store.Blocked(context.Background(), "evil.example"); blocked {
		t.Fatal("still blocked after unblock")
	}
}

func TestAdminUsersAndOverview(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	e.srv.Alerts = fakeAlerts{}
	a, ua := e.signup("a@example.com")
	b, ub := e.signup("b@example.com")
	admin := e.admin("root@example.com")
	la := a.create(t, map[string]any{"destination": "https://a.example"})
	lb := b.create(t, map[string]any{"destination": "https://b.example"})
	b.create(t, map[string]any{"destination": "https://c.example"})
	e.clicks(la, ua.WorkspaceID, now.Add(-time.Hour), domain.ClassHuman, 2)
	e.clicks(lb, ub.WorkspaceID, now.Add(-time.Hour), domain.ClassHuman, 3)

	var users struct {
		Items []struct {
			ID        int64
			Email     string
			Role      string
			CreatedAt time.Time `json:"created_at"`
			LinkCount int64     `json:"link_count"`
		}
	}
	admin.do("GET", "/api/admin/users", nil).json(t, &users)
	counts := map[string]int64{}
	for _, u := range users.Items {
		counts[u.Email] = u.LinkCount
		if u.CreatedAt.IsZero() {
			t.Errorf("%s has no created_at", u.Email)
		}
	}
	if len(users.Items) != 3 || counts["a@example.com"] != 1 || counts["b@example.com"] != 2 || users.Items[0].Role != "admin" {
		t.Fatalf("users = %+v", users.Items)
	}

	var ov analytics.Overview
	admin.do("GET", "/api/admin/analytics/overview", nil).json(t, &ov)
	if ov.Totals.Human != 5 || len(ov.TopLinks) != 2 || ov.AlertsOpen == nil || *ov.AlertsOpen != 7 {
		t.Fatalf("platform overview = %+v", ov)
	}
	a.do("GET", "/api/analytics/overview", nil).json(t, &ov)
	if ov.Totals.Human != 2 || ov.AlertsOpen == nil || *ov.AlertsOpen != int(ua.WorkspaceID) {
		t.Fatalf("workspace overview = %+v", ov)
	}
}

func TestRateLimits(t *testing.T) {
	e := newEnv(t)
	e.srv.CreateLimit = ratelimit.New(2, time.Minute)
	e.srv.LoginLimit = ratelimit.New(2, 15*time.Minute)
	e.srv.ImportLimit = ratelimit.New(1, time.Hour)
	c, _ := e.signup("rl@example.com")
	other, _ := e.signup("other@example.com")

	for i := range 2 {
		if r := c.do("POST", "/api/links", map[string]string{"destination": "https://a.example/" + strconv.Itoa(i)}); r.status != http.StatusCreated {
			t.Fatalf("create %d = %d", i, r.status)
		}
	}
	r := c.do("POST", "/api/links", map[string]string{"destination": "https://a.example/3"})
	if r.status != http.StatusTooManyRequests || r.errorCode(t) != "RATE_LIMITED" || r.header.Get("Retry-After") == "" {
		t.Fatalf("third create = %d %s (Retry-After %q)", r.status, r.body, r.header.Get("Retry-After"))
	}
	// The limit is per user.
	if r := other.do("POST", "/api/links", map[string]string{"destination": "https://b.example"}); r.status != http.StatusCreated {
		t.Fatalf("another user's create = %d", r.status)
	}

	if r := c.upload(t, "destination\nhttps://a.example\n"); r.status != http.StatusCreated {
		t.Fatalf("first import = %d", r.status)
	}
	if r := c.upload(t, "destination\nhttps://a.example\n"); r.status != http.StatusTooManyRequests {
		t.Fatalf("second import = %d", r.status)
	}

	login := func() int {
		return e.client().do("POST", "/api/auth/login", map[string]string{"email": "rl@example.com", "password": "wrong"}).status
	}
	if got := []int{login(), login(), login()}; !slices.Equal(got, []int{401, 401, 429}) {
		t.Fatalf("logins = %v, want 401 401 429", got)
	}
}
