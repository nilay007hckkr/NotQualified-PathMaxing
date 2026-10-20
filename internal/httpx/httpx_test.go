package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		xff    []string
		hops   int
		want   string
	}{
		{name: "peer address", remote: "203.0.113.9:5000", want: "203.0.113.9"},
		{name: "header ignored without trusted proxies", remote: "203.0.113.9:5000", xff: []string{"1.2.3.4"}, want: "203.0.113.9"},
		{name: "one proxy: the last entry", remote: "10.0.0.2:1", xff: []string{"1.2.3.4"}, hops: 1, want: "1.2.3.4"},
		{name: "spoofed entries on the left are skipped", remote: "10.0.0.2:1", xff: []string{"6.6.6.6, 1.2.3.4"}, hops: 1, want: "1.2.3.4"},
		{name: "two proxies", remote: "10.0.0.2:1", xff: []string{"6.6.6.6, 1.2.3.4", "10.0.0.7"}, hops: 2, want: "1.2.3.4"},
		{name: "fewer entries than hops falls back to the peer", remote: "10.0.0.2:1", xff: []string{"1.2.3.4"}, hops: 2, want: "10.0.0.2"},
		{name: "garbage falls back to the peer", remote: "10.0.0.2:1", xff: []string{"not-an-ip"}, hops: 1, want: "10.0.0.2"},
		{name: "IPv4-mapped is unmapped", remote: "[::ffff:198.51.100.1]:80", want: "198.51.100.1"},
		{name: "IPv6 peer", remote: "[2001:db8::1]:443", want: "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/x", nil)
			r.RemoteAddr = tt.remote
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tt.hops).String(); got != tt.want {
				t.Fatalf("ClientIP = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusNotFound, "NOT_FOUND", "link not found")
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 404 || body.Error.Code != "NOT_FOUND" || body.Error.Message != "link not found" {
		t.Fatalf("got %d %+v", rec.Code, body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestDecodeJSON(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name    string
		ctype   string
		body    string
		wantErr string
	}{
		{name: "ok", ctype: "application/json", body: `{"name":"a"}`},
		{name: "charset is fine", ctype: "application/json; charset=utf-8", body: `{"name":"a"}`},
		{name: "form content type", ctype: "application/x-www-form-urlencoded", body: `{"name":"a"}`, wantErr: "Content-Type"},
		{name: "missing content type", body: `{"name":"a"}`, wantErr: "Content-Type"},
		{name: "unknown field", ctype: "application/json", body: `{"nme":"a"}`, wantErr: "unknown field"},
		{name: "two objects", ctype: "application/json", body: `{"name":"a"}{"name":"b"}`, wantErr: "single JSON object"},
		{name: "broken JSON", ctype: "application/json", body: `{"name":`, wantErr: "invalid JSON"},
		{name: "too large", ctype: "application/json", body: `{"name":"` + strings.Repeat("a", MaxJSONBody) + `"}`, wantErr: "larger than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/x", strings.NewReader(tt.body))
			if tt.ctype != "" {
				r.Header.Set("Content-Type", tt.ctype)
			}
			var in input
			err := DecodeJSON(httptest.NewRecorder(), r, &in)
			if tt.wantErr == "" {
				if err != nil || in.Name != "a" {
					t.Fatalf("got %+v, %v", in, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !errors.Is(err, ErrBadRequest) {
				t.Fatalf("error = %v, want a bad request mentioning %q", err, tt.wantErr)
			}
		})
	}
}
