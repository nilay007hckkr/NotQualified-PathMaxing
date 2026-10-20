package secret

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func withCookie(c *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "/sale", nil)
	r.AddCookie(c)
	return r
}

func TestUnlockCookie(t *testing.T) {
	u := NewKeys(strings.Repeat("k", 32)).NewUnlocker(true)
	hash := []byte("$2a$10$current")
	c := u.Cookie(42, hash, now)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Name != "pm_unlock_42" {
		t.Fatalf("cookie attributes: %+v", c)
	}

	tests := []struct {
		name string
		req  *http.Request
		id   int64
		hash []byte
		at   time.Time
		want bool
	}{
		{"valid", withCookie(c), 42, hash, now.Add(time.Hour), true},
		{"other link", withCookie(&http.Cookie{Name: CookieName(43), Value: c.Value}), 43, hash, now, false},
		{"password changed", withCookie(c), 42, []byte("$2a$10$new"), now, false},
		{"expired", withCookie(c), 42, hash, now.Add(13 * time.Hour), false},
		{"tampered expiry", withCookie(&http.Cookie{Name: c.Name, Value: "9999999999" + c.Value[strings.Index(c.Value, "."):]}), 42, hash, now, false},
		{"garbage", withCookie(&http.Cookie{Name: c.Name, Value: "nonsense"}), 42, hash, now, false},
		{"no cookie", httptest.NewRequest("GET", "/sale", nil), 42, hash, now, false},
	}
	for _, tt := range tests {
		if got := u.Valid(tt.req, tt.id, tt.hash, tt.at); got != tt.want {
			t.Errorf("%s: Valid = %v, want %v", tt.name, got, tt.want)
		}
	}

	other := NewKeys(strings.Repeat("x", 32)).NewUnlocker(true)
	if other.Valid(withCookie(c), 42, hash, now) {
		t.Error("a cookie signed with another secret was accepted")
	}
}

func TestVisitorHash(t *testing.T) {
	v := NewKeys(strings.Repeat("k", 32)).NewVisitors()
	ip := netip.MustParseAddr("203.0.113.7")
	ua := "Mozilla/5.0 Chrome/129"
	a := v.Hash(ip, ua, now)
	if len(a) != 32 || strings.Contains(a, "203") {
		t.Fatalf("hash %q", a)
	}
	if v.Hash(ip, ua, now.Add(6*time.Hour)) != a {
		t.Error("hash changed within the same UTC day")
	}
	if v.Hash(ip, ua, now.Add(24*time.Hour)) == a {
		t.Error("hash did not rotate the next day")
	}
	if v.Hash(netip.MustParseAddr("203.0.113.8"), ua, now) == a || v.Hash(ip, "curl/8", now) == a {
		t.Error("different visitors share a hash")
	}
	if NewKeys(strings.Repeat("x", 32)).NewVisitors().Hash(ip, ua, now) == a {
		t.Error("hash does not depend on the secret")
	}
}
