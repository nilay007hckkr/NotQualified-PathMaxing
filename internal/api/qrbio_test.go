package api

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestLinkQR(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("qr@example.com")
	other, _ := e.signup("other@example.com")
	l := c.create(t, map[string]any{"destination": "https://a.example", "alias": "scanme"})
	path := "/api/links/" + strconv.FormatInt(l.ID, 10) + "/qr"

	r := c.do("GET", path, nil)
	if r.status != http.StatusOK || r.header.Get("Content-Type") != "image/png" {
		t.Fatalf("default QR = %d %q", r.status, r.header.Get("Content-Type"))
	}
	img, err := png.Decode(bytes.NewReader(r.body))
	if err != nil || img.Bounds().Dx() != 512 {
		t.Fatalf("default PNG: %v, %v", err, img)
	}
	r = c.do("GET", path+"?format=svg&size=200", nil)
	if r.status != http.StatusOK || r.header.Get("Content-Type") != "image/svg+xml" || !bytes.Contains(r.body, []byte(`width="200"`)) {
		t.Fatalf("SVG QR = %d %q", r.status, r.header.Get("Content-Type"))
	}
	for _, bad := range []string{"?size=64", "?size=2048", "?size=big", "?format=gif"} {
		if r := c.do("GET", path+bad, nil); r.status != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, r.status)
		}
	}
	if r := other.do("GET", path, nil); r.status != http.StatusNotFound {
		t.Fatalf("another user's QR = %d", r.status)
	}
}

func TestBioPages(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("bio@example.com")
	other, _ := e.signup("other@example.com")
	shop := c.create(t, map[string]any{"destination": "https://shop.example", "title": "Shop <now>"})
	blog := c.create(t, map[string]any{"destination": "https://blog.example", "alias": "blog"})
	paused := c.create(t, map[string]any{"destination": "https://old.example", "title": "Old"})
	c.do("POST", "/api/links/"+strconv.FormatInt(paused.ID, 10)+"/pause", nil)
	theirs := other.create(t, map[string]any{"destination": "https://x.example"})

	r := c.do("POST", "/api/bio-pages", map[string]any{"slug": "Asha", "title": "Asha's links", "link_ids": []int64{blog.ID, shop.ID, paused.ID}})
	if r.status != http.StatusCreated {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	var out struct {
		BioPage bioJSON `json:"bio_page"`
	}
	r.json(t, &out)
	if out.BioPage.Slug != "Asha" || !slices.Equal(out.BioPage.LinkIDs, []int64{blog.ID, shop.ID, paused.ID}) {
		t.Fatalf("bio page = %+v", out.BioPage)
	}
	id := strconv.FormatInt(out.BioPage.ID, 10)

	for _, tt := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{"slug": "asha", "title": "dup"}, http.StatusConflict, "SLUG_TAKEN"},
		{map[string]any{"slug": "admin"}, http.StatusUnprocessableEntity, "SLUG_INVALID"},
		{map[string]any{"slug": "a b"}, http.StatusUnprocessableEntity, "SLUG_INVALID"},
		{map[string]any{"slug": "mine", "link_ids": []int64{theirs.ID}}, http.StatusUnprocessableEntity, "UNKNOWN_LINK"},
		{map[string]any{"slug": "twice", "link_ids": []int64{blog.ID, blog.ID}}, http.StatusUnprocessableEntity, "DUPLICATE_LINK"},
	} {
		r := c.do("POST", "/api/bio-pages", tt.body)
		var v Validation
		r.json(t, &v)
		if r.status != tt.status || !slices.Equal(codes(v.Errors), []string{tt.code}) {
			t.Errorf("create %v = %d %s", tt.body, r.status, r.body)
		}
	}

	// The public page lists only links a visitor could open, in order,
	// pointing at the short links, with HTML escaped.
	resp, err := http.Get(e.http.URL + "/bio/ASHA")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("public page = %d", resp.StatusCode)
	}
	iBlog, iShop := strings.Index(html, testOrigin+"/blog"), strings.Index(html, testOrigin+"/"+shop.Code)
	if iBlog < 0 || iShop < 0 || iBlog > iShop || strings.Contains(html, "Old") || !strings.Contains(html, "Shop &lt;now&gt;") ||
		!strings.Contains(html, "Asha&#39;s links") || strings.Contains(html, "https://shop.example") {
		t.Fatalf("public page:\n%s", html)
	}

	// PATCH reorders and retitles; another user can't touch it.
	r = c.do("PATCH", "/api/bio-pages/"+id, map[string]any{"title": "New", "link_ids": []int64{shop.ID}})
	r.json(t, &out)
	if r.status != http.StatusOK || out.BioPage.Title != "New" || out.BioPage.Slug != "Asha" || !slices.Equal(out.BioPage.LinkIDs, []int64{shop.ID}) {
		t.Fatalf("patch = %d %s", r.status, r.body)
	}
	if r := other.do("PATCH", "/api/bio-pages/"+id, map[string]any{"title": "x"}); r.status != http.StatusNotFound {
		t.Fatalf("another user's patch = %d", r.status)
	}
	var list struct{ Items []bioJSON }
	other.do("GET", "/api/bio-pages", nil).json(t, &list)
	if len(list.Items) != 0 {
		t.Fatalf("another user lists %d pages", len(list.Items))
	}

	resp, err = http.Get(e.http.URL + "/bio/nobody-here")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing page = %d", resp.StatusCode)
	}
}
