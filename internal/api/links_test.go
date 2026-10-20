package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/rules"
)

func ptr[T any](v T) *T { return &v }

type created struct {
	Link     linkJSON
	Warnings []Issue
}

func (c *client) create(t *testing.T, in map[string]any) linkJSON {
	t.Helper()
	r := c.do("POST", "/api/links", in)
	if r.status != http.StatusCreated {
		t.Fatalf("create %v = %d %s", in, r.status, r.body)
	}
	var out created
	r.json(t, &out)
	return out.Link
}

func codes(issues []Issue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

// Every error and warning code API.md lists for destinations, aliases and
// rules can be reached through the validate step.
func TestValidateCodes(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("v@example.com")
	c.create(t, map[string]any{"destination": "https://a.example", "alias": "Taken"})
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO blocked_domains (domain, reason) VALUES ('evil.example', 'phishing')`); err != nil {
		t.Fatal(err)
	}
	if err := e.store.ReloadBlocklist(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.trust.set("https://malware.example/", domain.Precheck{ThreatMatch: true, ThreatSources: []string{"urlhaus"}})
	e.trust.set("https://down.example/", domain.Precheck{Reachable: ptr(false)})
	e.trust.set("https://hops.example/", domain.Precheck{Reachable: ptr(true), Hops: 4})

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	later := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	ok := "https://shop.example/sale"
	tests := []struct {
		name     string
		in       map[string]any
		errors   []string
		warnings []string
	}{
		{"valid", map[string]any{"destination": ok, "alias": "diwali"}, nil, nil},
		{"invalid url", map[string]any{"destination": "not a url"}, []string{"INVALID_URL"}, nil},
		{"missing destination", map[string]any{}, []string{"INVALID_URL"}, nil},
		{"javascript scheme", map[string]any{"destination": "javascript:alert(1)"}, []string{"SCHEME_NOT_ALLOWED"}, nil},
		{"userinfo", map[string]any{"destination": "https://bank.example@evil2.example/"}, []string{"USERINFO_NOT_ALLOWED"}, nil},
		{"self loop", map[string]any{"destination": "http://pm.test/abc"}, []string{"SELF_LOOP"}, nil},
		{"private ip", map[string]any{"destination": "http://10.0.0.5/admin"}, []string{"NOT_PUBLIC"}, nil},
		{"blocked subdomain", map[string]any{"destination": "https://login.evil.example/"}, []string{"BLOCKED_DOMAIN"}, nil},
		{"threat feed", map[string]any{"destination": "https://malware.example/"}, []string{"THREAT_MATCH"}, nil},
		{"alias taken in another case", map[string]any{"destination": ok, "alias": "TAKEN"}, []string{"ALIAS_TAKEN"}, nil},
		{"alias reserved", map[string]any{"destination": ok, "alias": "Admin"}, []string{"ALIAS_RESERVED"}, nil},
		{"alias invalid", map[string]any{"destination": ok, "alias": "-x"}, []string{"ALIAS_INVALID"}, nil},
		{"expiry in past", map[string]any{"destination": ok, "expires_at": past}, []string{"EXPIRY_IN_PAST"}, nil},
		{"expiry before start", map[string]any{"destination": ok, "starts_at": later, "expires_at": future}, []string{"EXPIRY_BEFORE_START"}, nil},
		{"cap zero", map[string]any{"destination": ok, "max_clicks": 0}, []string{"CAP_INVALID"}, nil},
		{"unknown device", map[string]any{"destination": ok, "targets": []map[string]string{{"device": "watch", "destination": ok}}}, []string{"TARGET_DEVICE_INVALID"}, nil},
		{"duplicate device", map[string]any{"destination": ok, "targets": []map[string]string{
			{"device": "mobile", "destination": ok}, {"device": "mobile", "destination": ok}}}, []string{"TARGET_DEVICE_INVALID"}, nil},
		{"target destination checked too", map[string]any{"destination": ok, "targets": []map[string]string{{"device": "mobile", "destination": "http://localhost/"}}}, []string{"NOT_PUBLIC"}, nil},
		{"http", map[string]any{"destination": "http://shop.example/"}, nil, []string{"HTTP_NOT_HTTPS"}},
		{"punycode", map[string]any{"destination": "https://xn--80ak6aa92e.com/"}, nil, []string{"PUNYCODE_DOMAIN"}},
		{"unreachable", map[string]any{"destination": "https://down.example/"}, nil, []string{"DESTINATION_UNREACHABLE"}},
		{"long chain", map[string]any{"destination": "https://hops.example/"}, nil, []string{"LONG_REDIRECT_CHAIN"}},
		{"one click without password", map[string]any{"destination": ok, "max_clicks": 1}, nil, []string{"ONE_CLICK_NO_PASSWORD"}},
		{"one click with password", map[string]any{"destination": ok, "max_clicks": 1, "password": "secret"}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := c.do("POST", "/api/links/validate", tt.in)
			var v Validation
			r.json(t, &v)
			if r.status != http.StatusOK || !slices.Equal(codes(v.Errors), tt.errors) || !slices.Equal(codes(v.Warnings), tt.warnings) {
				t.Fatalf("= %d %s; want errors %v warnings %v", r.status, r.body, tt.errors, tt.warnings)
			}
		})
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM links`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("validate saved links: %d, %v", n, err)
	}
}

func TestCreateLink(t *testing.T) {
	e := newEnv(t)
	c, u := e.signup("c@example.com")
	expires := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	r := c.do("POST", "/api/links", map[string]any{
		"destination": "http://Shop.Example/Sale", "alias": "Diwali", "title": " Diwali sale ",
		"expires_at": expires.Format(time.RFC3339), "max_clicks": 100, "password": "open sesame",
		"targets": []map[string]string{{"device": "mobile", "destination": "https://m.shop.example/sale"}},
	})
	if r.status != http.StatusCreated {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	var out created
	r.json(t, &out)
	l := out.Link
	if l.Code != "Diwali" || l.ShortURL != testOrigin+"/Diwali" || l.Destination != "http://shop.example/Sale" ||
		l.Title != "Diwali sale" || l.Status != "active" || l.TrustState != "ok" || !l.HasPassword ||
		*l.MaxClicks != 100 || !l.ExpiresAt.Equal(expires) || l.IsExpired || len(l.Targets) != 1 ||
		l.TagIDs == nil || l.CampaignID != nil {
		t.Fatalf("link = %+v", l)
	}
	if !slices.Equal(codes(out.Warnings), []string{"HTTP_NOT_HTTPS"}) {
		t.Fatalf("warnings = %v", out.Warnings)
	}
	stored, err := e.store.GetLink(context.Background(), l.ID)
	if err != nil || stored.OwnerID != u.ID || stored.WorkspaceID != u.WorkspaceID {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if !slices.Contains(e.cache.invalidated(), "Diwali") {
		t.Fatalf("a new code must be invalidated (it may be cached as unknown); got %v", e.cache.invalidated())
	}

	gen := c.create(t, map[string]any{"destination": "https://a.example"})
	if len(gen.Code) != 8 {
		t.Fatalf("generated code %q", gen.Code)
	}

	bad := c.do("POST", "/api/links", map[string]any{"destination": "ftp://files.example", "max_clicks": -1})
	var v Validation
	bad.json(t, &v)
	if bad.status != http.StatusUnprocessableEntity || !slices.Equal(codes(v.Errors), []string{"SCHEME_NOT_ALLOWED", "CAP_INVALID"}) {
		t.Fatalf("invalid create = %d %s", bad.status, bad.body)
	}
	if r := c.do("POST", "/api/links", map[string]any{"destination": "https://a.example", "colour": "red"}); r.status != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", r.status)
	}
}

// Two people racing for "Sale" and "sale": one 201, the rest 409.
func TestAliasRaceOverHTTP(t *testing.T) {
	e := newEnv(t)
	a, _ := e.signup("a@example.com")
	b, _ := e.signup("b@example.com")
	var wg sync.WaitGroup
	statuses := make([]int, 6)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, alias := a, "Sale"
			if i%2 == 1 {
				c, alias = b, "sale"
			}
			statuses[i] = c.do("POST", "/api/links", map[string]any{"destination": "https://a.example", "alias": alias}).status
		}()
	}
	wg.Wait()
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{201, 409, 409, 409, 409, 409}) {
		t.Fatalf("statuses %v, want one 201 and five 409", statuses)
	}
}

func TestOwnership(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.signup("owner@example.com")
	other, _ := e.signup("other@example.com")
	l := owner.create(t, map[string]any{"destination": "https://a.example", "alias": "mine"})
	id := "/api/links/" + strconv.FormatInt(l.ID, 10)

	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"GET", id, nil},
		{"PATCH", id, map[string]any{"title": "stolen"}},
		{"POST", id + "/pause", nil},
		{"DELETE", id, nil},
		{"GET", "/api/links/999999", nil},
		{"GET", "/api/links/abc", nil},
	} {
		if r := other.do(req.method, req.path, req.body); r.status != http.StatusNotFound || r.errorCode(t) != "NOT_FOUND" {
			t.Errorf("%s %s by another user = %d %s, want 404", req.method, req.path, r.status, r.body)
		}
	}
	var page struct {
		Items      []linkJSON
		NextCursor *string `json:"next_cursor"`
	}
	other.do("GET", "/api/links", nil).json(t, &page)
	if len(page.Items) != 0 {
		t.Fatalf("another user's list shows %d links", len(page.Items))
	}
	if r := owner.do("GET", id, nil); r.status != http.StatusOK {
		t.Fatalf("owner GET = %d", r.status)
	}
	admin := e.admin("root@example.com")
	if r := admin.do("GET", id, nil); r.status != http.StatusOK {
		t.Fatalf("admin GET = %d", r.status)
	}
	if r := e.client().do("GET", "/api/links", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("logged out = %d", r.status)
	}
}

func TestStatusChanges(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("s@example.com")
	l := c.create(t, map[string]any{"destination": "https://a.example", "alias": "pausable"})
	path := "/api/links/" + strconv.FormatInt(l.ID, 10)
	engine := &rules.Engine{Blocklist: e.store, Caps: e.store}
	outcome := func() domain.Outcome {
		got, err := e.store.ResolveCode(context.Background(), "pausable")
		if err != nil {
			t.Fatal(err)
		}
		d, err := engine.Decide(context.Background(), got, rules.Visit{Human: true, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return d.Outcome
	}

	for _, step := range []struct {
		action  string
		status  string
		outcome domain.Outcome
	}{
		{"pause", "paused", domain.OutcomeInactive},
		{"resume", "active", domain.OutcomeRedirected},
		{"archive", "archived", domain.OutcomeInactive},
		{"resume", "active", domain.OutcomeRedirected},
	} {
		r := c.do("POST", path+"/"+step.action, nil)
		var out struct{ Link linkJSON }
		r.json(t, &out)
		if r.status != http.StatusOK || out.Link.Status != step.status {
			t.Fatalf("%s = %d %s", step.action, r.status, r.body)
		}
		if got := outcome(); got != step.outcome {
			t.Fatalf("after %s the redirect says %s, want %s", step.action, got, step.outcome)
		}
	}

	// An owner can't undo an admin's takedown.
	if _, err := e.store.SetStatus(context.Background(), l.ID, domain.StatusDisabledAdmin, "phishing", true); err != nil {
		t.Fatal(err)
	}
	if r := c.do("POST", path+"/resume", nil); r.status != http.StatusConflict || r.errorCode(t) != "LINK_DISABLED" {
		t.Fatalf("resume a disabled link = %d %s", r.status, r.body)
	}

	if r := c.do("DELETE", path, nil); r.status != http.StatusNoContent {
		t.Fatalf("delete = %d", r.status)
	}
	if r := c.do("GET", path, nil); r.status != http.StatusNotFound {
		t.Fatalf("get after delete = %d", r.status)
	}
	if got := outcome(); got != domain.OutcomeBlockedAdmin {
		// Deleted links still resolve so the redirect can answer 410; the
		// takedown is still the first rule that refuses it.
		t.Fatalf("deleted link outcome %s", got)
	}
	inv := e.cache.invalidated()
	if n := len(slices.DeleteFunc(inv, func(c string) bool { return c != "pausable" })); n < 6 {
		t.Fatalf("cache invalidated %d times for pausable, want one per change", n)
	}
	var audits int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE entity_type = 'link' AND entity_id = $1`, l.ID).Scan(&audits); err != nil || audits != 6 {
		t.Fatalf("audit entries = %d, %v; want 6 (create, 4 status changes, delete)", audits, err)
	}
}

func TestUpdateLink(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("u@example.com")
	l := c.create(t, map[string]any{"destination": "https://a.example", "alias": "editme", "password": "pw", "max_clicks": 5,
		"targets": []map[string]string{{"device": "mobile", "destination": "https://m.a.example"}}})
	path := "/api/links/" + strconv.FormatInt(l.ID, 10)
	patch := func(body map[string]any) (int, linkJSON, Validation) {
		r := c.do("PATCH", path, body)
		var out struct{ Link linkJSON }
		var v Validation
		if r.status == http.StatusOK {
			r.json(t, &out)
		} else {
			r.json(t, &v)
		}
		return r.status, out.Link, v
	}

	status, got, _ := patch(map[string]any{"title": "New title"})
	if status != 200 || got.Title != "New title" || got.Destination != "https://a.example" || !got.HasPassword ||
		*got.MaxClicks != 5 || len(got.Targets) != 1 {
		t.Fatalf("title patch changed other fields: %d %+v", status, got)
	}
	status, got, _ = patch(map[string]any{"password": "", "max_clicks": nil, "targets": []any{}, "destination": "https://b.example/x"})
	if status != 200 || got.HasPassword || got.MaxClicks != nil || len(got.Targets) != 0 || got.Destination != "https://b.example/x" {
		t.Fatalf("clearing patch: %d %+v", status, got)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	for _, tt := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"expires_at": past}, "EXPIRY_IN_PAST"},
		{map[string]any{"destination": "javascript:alert(1)"}, "SCHEME_NOT_ALLOWED"},
		{map[string]any{"max_clicks": 0}, "CAP_INVALID"},
		{map[string]any{"alias": "other"}, "ALIAS_INVALID"},
	} {
		if status, _, v := patch(tt.body); status != http.StatusUnprocessableEntity || !slices.Equal(codes(v.Errors), []string{tt.code}) {
			t.Errorf("patch %v = %d %v, want 422 %s", tt.body, status, v.Errors, tt.code)
		}
	}
	// Sending the same code back (any case) is not a change.
	if status, _, _ := patch(map[string]any{"alias": "EDITME"}); status != http.StatusOK {
		t.Fatalf("same alias = %d", status)
	}
}

func TestListLinks(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("l@example.com")
	for i := range 5 {
		c.create(t, map[string]any{"destination": "https://shop.example/" + strconv.Itoa(i), "title": "Item " + strconv.Itoa(i)})
	}
	sale := c.create(t, map[string]any{"destination": "https://other.example/", "alias": "big_sale", "title": "100% off"})
	c.do("POST", "/api/links/"+strconv.FormatInt(sale.ID, 10)+"/pause", nil)

	type pageT struct {
		Items      []linkJSON
		NextCursor *string `json:"next_cursor"`
	}
	list := func(query string) pageT {
		r := c.do("GET", "/api/links"+query, nil)
		if r.status != http.StatusOK {
			t.Fatalf("list %s = %d %s", query, r.status, r.body)
		}
		var p pageT
		r.json(t, &p)
		return p
	}
	p := list("?limit=4")
	if len(p.Items) != 4 || p.NextCursor == nil || p.Items[0].ID != sale.ID {
		t.Fatalf("first page: %d items, cursor %v", len(p.Items), p.NextCursor)
	}
	p2 := list("?limit=4&cursor=" + *p.NextCursor)
	if len(p2.Items) != 2 || p2.NextCursor != nil || p2.Items[0].ID >= p.Items[3].ID {
		t.Fatalf("second page: %d items, cursor %v", len(p2.Items), p2.NextCursor)
	}
	for query, want := range map[string]int{
		"?q=shop.example": 5, "?q=BIG_sale": 1, "?q=100%25": 1, "?q=_": 1, "?status=paused": 1, "?status=active": 5,
		"?trust_state=quarantined": 0, "": 6,
	} {
		if got := len(list(query).Items); got != want {
			t.Errorf("list%s = %d links, want %d", query, got, want)
		}
	}
	for _, bad := range []string{"?status=expired", "?limit=0", "?cursor=!!", "?trust_state=fine"} {
		if r := c.do("GET", "/api/links"+bad, nil); r.status != http.StatusBadRequest {
			t.Errorf("list%s = %d, want 400", bad, r.status)
		}
	}
}
