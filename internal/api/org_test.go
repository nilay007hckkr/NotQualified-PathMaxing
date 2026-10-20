package api

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

func (c *client) createOrg(t *testing.T, path, key string, body map[string]any) map[string]any {
	t.Helper()
	r := c.do("POST", path, body)
	if r.status != http.StatusCreated {
		t.Fatalf("POST %s %v = %d %s", path, body, r.status, r.body)
	}
	var out map[string]map[string]any
	r.json(t, &out)
	return out[key]
}

func idOf(m map[string]any) int64 { return int64(m["id"].(float64)) }

func (c *client) getLinkJSON(t *testing.T, id int64) linkJSON {
	t.Helper()
	var out struct{ Link linkJSON }
	c.do("GET", "/api/links/"+strconv.FormatInt(id, 10), nil).json(t, &out)
	return out.Link
}

func TestCampaigns(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("camp@example.com")
	other, _ := e.signup("other@example.com")

	camp := c.createOrg(t, "/api/campaigns", "campaign", map[string]any{"name": "Diwali 2026", "utm_source": "instagram", "utm_medium": "social"})
	if camp["utm_source"] != "instagram" || camp["utm_campaign"] != "" || camp["link_count"].(float64) != 0 || camp["created_at"] == "" {
		t.Fatalf("campaign = %v", camp)
	}
	cid := idOf(camp)
	path := "/api/campaigns/" + strconv.FormatInt(cid, 10)

	l := c.create(t, map[string]any{"destination": "https://a.example", "campaign_id": cid})
	if l.CampaignID == nil || *l.CampaignID != cid {
		t.Fatalf("link campaign = %v", l.CampaignID)
	}
	var got map[string]map[string]any
	c.do("GET", path, nil).json(t, &got)
	if got["campaign"]["link_count"].(float64) != 1 {
		t.Fatalf("link_count = %v", got["campaign"]["link_count"])
	}

	for _, tt := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{"name": "DIWALI 2026"}, http.StatusConflict, "NAME_TAKEN"},
		{map[string]any{"name": "  "}, http.StatusUnprocessableEntity, "NAME_REQUIRED"},
		{map[string]any{}, http.StatusUnprocessableEntity, "NAME_REQUIRED"},
	} {
		r := c.do("POST", "/api/campaigns", tt.body)
		var v Validation
		r.json(t, &v)
		if r.status != tt.status || len(v.Errors) != 1 || v.Errors[0].Code != tt.code {
			t.Errorf("create %v = %d %s", tt.body, r.status, r.body)
		}
	}

	// PATCH is partial, and the web app may send read-only fields back.
	r := c.do("PATCH", path, map[string]any{"name": "Diwali", "id": cid, "link_count": 1})
	r.json(t, &got)
	if r.status != http.StatusOK || got["campaign"]["name"] != "Diwali" || got["campaign"]["utm_source"] != "instagram" {
		t.Fatalf("patch = %d %s", r.status, r.body)
	}

	// Another user can't see, change, delete or use it.
	for _, req := range []struct{ method, path string }{{"GET", path}, {"PATCH", path}, {"DELETE", path}} {
		if r := other.do(req.method, req.path, map[string]any{"name": "x"}); r.status != http.StatusNotFound {
			t.Errorf("%s by another user = %d", req.method, r.status)
		}
	}
	var page struct{ Items []map[string]any }
	other.do("GET", "/api/campaigns", nil).json(t, &page)
	if len(page.Items) != 0 {
		t.Fatalf("another user lists %d campaigns", len(page.Items))
	}
	bad := other.do("POST", "/api/links", map[string]any{"destination": "https://a.example", "campaign_id": cid})
	var v Validation
	bad.json(t, &v)
	if bad.status != http.StatusUnprocessableEntity || !slices.Equal(codes(v.Errors), []string{"UNKNOWN_CAMPAIGN"}) {
		t.Fatalf("using another user's campaign = %d %s", bad.status, bad.body)
	}

	var list struct{ Items []linkJSON }
	c.do("GET", "/api/links?campaign_id="+strconv.FormatInt(cid, 10), nil).json(t, &list)
	if len(list.Items) != 1 {
		t.Fatalf("filter by campaign = %d links", len(list.Items))
	}

	// Deleting the campaign keeps the link.
	if r := c.do("DELETE", path, nil); r.status != http.StatusNoContent {
		t.Fatalf("delete = %d", r.status)
	}
	if got := c.getLinkJSON(t, l.ID); got.ID != l.ID || got.CampaignID != nil {
		t.Fatalf("link after campaign delete = %+v", got)
	}
}

func TestTagsAndFolders(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("tags@example.com")
	other, _ := e.signup("other@example.com")

	red := idOf(c.createOrg(t, "/api/tags", "tag", map[string]any{"name": "promo", "color": "#ff0000"}))
	blue := idOf(c.createOrg(t, "/api/tags", "tag", map[string]any{"name": "blue"}))
	theirs := idOf(other.createOrg(t, "/api/tags", "tag", map[string]any{"name": "theirs"}))
	if r := c.do("POST", "/api/tags", map[string]any{"name": "x", "color": "red"}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("bad color = %d", r.status)
	}

	top := idOf(c.createOrg(t, "/api/folders", "folder", map[string]any{"name": "Marketing"}))
	sub := idOf(c.createOrg(t, "/api/folders", "folder", map[string]any{"name": "Festive", "parent_id": top}))

	l := c.create(t, map[string]any{"destination": "https://a.example", "tag_ids": []int64{blue, red, blue}, "folder_id": sub})
	if !slices.Equal(l.TagIDs, []int64{red, blue}) || l.FolderID == nil || *l.FolderID != sub {
		t.Fatalf("link tags %v folder %v", l.TagIDs, l.FolderID)
	}
	c.create(t, map[string]any{"destination": "https://b.example"})

	var list struct{ Items []linkJSON }
	for query, want := range map[string]int{
		"?tag_id=" + strconv.FormatInt(red, 10): 1, "?folder_id=" + strconv.FormatInt(sub, 10): 1,
		"?folder_id=" + strconv.FormatInt(top, 10): 0, "": 2,
	} {
		c.do("GET", "/api/links"+query, nil).json(t, &list)
		if len(list.Items) != want {
			t.Errorf("links%s = %d, want %d", query, len(list.Items), want)
		}
	}

	for _, tt := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"tag_ids": []int64{red, theirs}}, "UNKNOWN_TAG"},
		{map[string]any{"folder_id": 999999}, "UNKNOWN_FOLDER"},
	} {
		r := c.do("PATCH", "/api/links/"+strconv.FormatInt(l.ID, 10), tt.body)
		var v Validation
		r.json(t, &v)
		if r.status != http.StatusUnprocessableEntity || !slices.Equal(codes(v.Errors), []string{tt.code}) {
			t.Errorf("patch %v = %d %s", tt.body, r.status, r.body)
		}
	}
	r := c.do("PATCH", "/api/links/"+strconv.FormatInt(l.ID, 10), map[string]any{"tag_ids": []int64{}, "folder_id": nil})
	var out struct{ Link linkJSON }
	r.json(t, &out)
	if r.status != http.StatusOK || len(out.Link.TagIDs) != 0 || out.Link.FolderID != nil {
		t.Fatalf("clear tags and folder = %d %s", r.status, r.body)
	}

	// A folder can't move inside its own subtree.
	r = c.do("PATCH", "/api/folders/"+strconv.FormatInt(top, 10), map[string]any{"parent_id": sub})
	var v Validation
	r.json(t, &v)
	if r.status != http.StatusUnprocessableEntity || !slices.Equal(codes(v.Errors), []string{"FOLDER_CYCLE"}) {
		t.Fatalf("cycle = %d %s", r.status, r.body)
	}
	if r := other.do("PATCH", "/api/tags/"+strconv.FormatInt(red, 10), map[string]any{"name": "mine"}); r.status != http.StatusNotFound {
		t.Fatalf("another user's tag = %d", r.status)
	}

	// Deleting the parent folder lifts its subfolder to the top level.
	if r := c.do("DELETE", "/api/folders/"+strconv.FormatInt(top, 10), nil); r.status != http.StatusNoContent {
		t.Fatalf("delete folder = %d", r.status)
	}
	var folder map[string]map[string]any
	c.do("GET", "/api/folders/"+strconv.FormatInt(sub, 10), nil).json(t, &folder)
	if folder["folder"]["parent_id"] != nil {
		t.Fatalf("subfolder after parent delete = %v", folder)
	}
	if r := c.do("DELETE", "/api/tags/"+strconv.FormatInt(red, 10), nil); r.status != http.StatusNoContent {
		t.Fatalf("delete tag = %d", r.status)
	}
	var tags struct{ Items []tagJSON }
	c.do("GET", "/api/tags", nil).json(t, &tags)
	if len(tags.Items) != 1 || tags.Items[0].Name != "blue" {
		t.Fatalf("tags = %+v", tags.Items)
	}
}
