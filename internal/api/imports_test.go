package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"pathmaxing/internal/domain"
)

func (c *client) upload(t *testing.T, csv string) response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "links.csv")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(fw, csv)
	mw.Close()
	req, _ := http.NewRequest("POST", c.e.http.URL+"/api/imports", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", c.origin)
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: b}
}

// The planted-errors file: every row but 2, 14 and 15 has exactly one
// problem, and row 14's alias is taken between preview and commit.
const plantedCSV = `destination,alias,title,campaign,tags,expires_at,max_clicks
https://shop.example/a,good1,Good one,Fest,promo;sale,,
not a url,,Bad URL,,,,
javascript:alert(1),,Script,,,,
http://192.168.1.1/admin,,Private IP,,,,
https://shop.example/b,admin,Reserved,,,,
https://shop.example/c,GOOD1,Duplicate,,,,
https://shop.example/d,,Unknown campaign,Nope,,,
https://shop.example/e,,Past,,,2020-01-01T00:00:00Z,
https://shop.example/f,,Zero cap,,,,0
https://shop.example/g,,Text cap,,,,many
https://shop.example/h,existing,Taken,,,,
https://malware.example/,,Threat,,,,
https://shop.example/i,later1,Later,,,,
https://shop.example/j,,No alias,fest,PROMO,,5
`

func TestImportPreviewAndCommit(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("bulk@example.com")
	other, _ := e.signup("other@example.com")
	c.createOrg(t, "/api/campaigns", "campaign", map[string]any{"name": "Fest"})
	c.create(t, map[string]any{"destination": "https://a.example", "alias": "Existing"})
	e.trust.set("https://malware.example/", domain.Precheck{ThreatMatch: true})

	r := c.upload(t, plantedCSV)
	if r.status != http.StatusCreated {
		t.Fatalf("upload = %d %s", r.status, r.body)
	}
	var job importJSON
	r.json(t, &job)
	if job.Status != "preview" || job.Total != 14 || job.Valid != 3 || job.Invalid != 11 {
		t.Fatalf("preview counts: %+v", job)
	}
	want := map[int][]string{
		2: {}, 3: {"INVALID_URL"}, 4: {"SCHEME_NOT_ALLOWED"}, 5: {"NOT_PUBLIC"}, 6: {"ALIAS_RESERVED"},
		7: {"DUP_IN_FILE"}, 8: {"UNKNOWN_CAMPAIGN"}, 9: {"EXPIRY_IN_PAST"}, 10: {"CAP_INVALID"}, 11: {"CAP_INVALID"},
		12: {"ALIAS_TAKEN"}, 13: {"THREAT_MATCH"}, 14: {}, 15: {},
	}
	for _, row := range job.Rows {
		if !slices.Equal(row.Errors, want[row.Row]) || (row.Status == "valid") != (len(want[row.Row]) == 0) {
			t.Errorf("row %d: %s %v, want %v", row.Row, row.Status, row.Errors, want[row.Row])
		}
	}
	if job.Rows[0].Alias != "good1" || job.Rows[0].Destination != "https://shop.example/a" {
		t.Errorf("row 2 echo = %+v", job.Rows[0])
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM links`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("preview created links: %d, %v", n, err)
	}

	// Someone takes row 14's alias before the commit.
	other.create(t, map[string]any{"destination": "https://b.example", "alias": "LATER1"})
	path := "/api/imports/" + strconv.FormatInt(job.ID, 10)
	if r := other.do("POST", path+"/commit", nil); r.status != http.StatusNotFound {
		t.Fatalf("another user's commit = %d", r.status)
	}
	r = c.do("POST", path+"/commit", nil)
	var res struct {
		Created int
		Skipped []skippedRow
	}
	r.json(t, &res)
	if r.status != http.StatusOK || res.Created != 2 || len(res.Skipped) != 1 || res.Skipped[0].Row != 14 ||
		!slices.Equal(res.Skipped[0].Errors, []string{"ALIAS_TAKEN"}) {
		t.Fatalf("commit = %d %s", r.status, r.body)
	}
	if r := c.do("POST", path+"/commit", nil); r.status != http.StatusConflict || r.errorCode(t) != "ALREADY_COMMITTED" {
		t.Fatalf("second commit = %d %s", r.status, r.body)
	}

	r = c.do("GET", path, nil)
	r.json(t, &job)
	if job.Status != "committed" || job.Valid != 2 || job.Rows[12].Status != "invalid" || !slices.Equal(job.Rows[12].Errors, []string{"ALIAS_TAKEN"}) {
		t.Fatalf("job after commit = %s", r.body)
	}

	// Row 2 got its campaign and both tags (created on the way); row 15 the
	// existing tag, matched ignoring case.
	var list struct{ Items []linkJSON }
	c.do("GET", "/api/links?q=shop.example", nil).json(t, &list)
	if len(list.Items) != 2 {
		t.Fatalf("imported links = %d", len(list.Items))
	}
	byTitle := map[string]linkJSON{}
	for _, l := range list.Items {
		byTitle[l.Title] = l
	}
	good, noAlias := byTitle["Good one"], byTitle["No alias"]
	if good.Code != "good1" || good.CampaignID == nil || len(good.TagIDs) != 2 {
		t.Fatalf("row 2 link = %+v", good)
	}
	if noAlias.CampaignID == nil || *noAlias.CampaignID != *good.CampaignID || len(noAlias.TagIDs) != 1 ||
		!slices.Contains(good.TagIDs, noAlias.TagIDs[0]) || *noAlias.MaxClicks != 5 {
		t.Fatalf("row 15 link = %+v", noAlias)
	}
	var tags struct{ Items []tagJSON }
	c.do("GET", "/api/tags", nil).json(t, &tags)
	if len(tags.Items) != 2 {
		t.Fatalf("tags after import = %+v", tags.Items)
	}
}

func TestImportLimits(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("limits@example.com")
	tests := []struct {
		name   string
		csv    string
		status int
		code   string
	}{
		{"too many rows", "destination\n" + strings.Repeat("https://a.example\n", 1001), http.StatusRequestEntityTooLarge, "TOO_MANY_ROWS"},
		{"over 1 MB", "destination,title\n" + strings.Repeat("https://a.example,"+strings.Repeat("x", 1100)+"\n", 1000), http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE"},
		{"no destination column", "alias\nabc\n", http.StatusBadRequest, "BAD_CSV"},
		{"empty", "", http.StatusBadRequest, "BAD_CSV"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := c.upload(t, tt.csv)
			if r.status != tt.status || r.errorCode(t) != tt.code {
				t.Fatalf("= %d %s, want %d %s", r.status, r.body, tt.status, tt.code)
			}
		})
	}
	r := c.do("POST", "/api/imports", map[string]string{"file": "x"})
	if r.status != http.StatusBadRequest {
		t.Fatalf("JSON instead of multipart = %d", r.status)
	}
	if ok := c.upload(t, "destination\n"+strings.Repeat("https://a.example\n", 1000)); ok.status != http.StatusCreated {
		t.Fatalf("1,000 rows = %d %s", ok.status, ok.body)
	}
	var job importJSON
	_ = json.Unmarshal(c.upload(t, "destination\nhttps://a.example\n").body, &job)
	if r := e.client().do("GET", "/api/imports/"+strconv.FormatInt(job.ID, 10), nil); r.status != http.StatusUnauthorized {
		t.Fatalf("logged-out GET = %d", r.status)
	}
}
