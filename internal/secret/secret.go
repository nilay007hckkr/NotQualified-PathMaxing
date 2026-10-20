// Package secret derives keys from SECRET_KEY and uses them for the two
// things the redirect service must not let visitors forge or reverse: the
// cookie that remembers a link was unlocked, and the visitor hash that
// stands in for an IP address in analytics.
package secret

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Keys derives a separate key per purpose from one secret, so a key leaked
// from one use can't sign anything for another.
type Keys struct{ master []byte }

// NewKeys wraps the server secret.
func NewKeys(secret string) Keys { return Keys{master: []byte(secret)} }

func (k Keys) derive(purpose string) []byte {
	m := hmac.New(sha256.New, k.master)
	m.Write([]byte(purpose))
	return m.Sum(nil)
}

// ---- unlock cookies ----------------------------------------------------------

// Unlocker issues and checks the cookie set after a correct link password.
type Unlocker struct {
	key    []byte
	TTL    time.Duration
	Secure bool // HTTPS only; true in production
}

// NewUnlocker returns an Unlocker whose cookies last 12 hours.
func (k Keys) NewUnlocker(secure bool) *Unlocker {
	return &Unlocker{key: k.derive("unlock-cookie"), TTL: 12 * time.Hour, Secure: secure}
}

// CookieName is per link, so unlocking one link never unlocks another.
func CookieName(linkID int64) string { return "pm_unlock_" + strconv.FormatInt(linkID, 10) }

// fingerprint ties a cookie to the current password: changing the password
// changes the hash, and every old cookie stops working.
func fingerprint(passwordHash []byte) string {
	sum := sha256.Sum256(passwordHash)
	return hex.EncodeToString(sum[:8])
}

func (u *Unlocker) sign(msg string) string {
	m := hmac.New(sha256.New, u.key)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Cookie returns the unlock cookie for a link with passwordHash.
func (u *Unlocker) Cookie(linkID int64, passwordHash []byte, now time.Time) *http.Cookie {
	exp := now.Add(u.TTL).Unix()
	msg := fmt.Sprintf("%d.%s.%d", linkID, fingerprint(passwordHash), exp)
	return &http.Cookie{
		Name:     CookieName(linkID),
		Value:    fmt.Sprintf("%d.%s", exp, u.sign(msg)),
		Path:     "/",
		MaxAge:   int(u.TTL.Seconds()),
		HttpOnly: true,
		Secure:   u.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// Valid reports whether r carries an unexpired unlock cookie for this link
// and its current password.
func (u *Unlocker) Valid(r *http.Request, linkID int64, passwordHash []byte, now time.Time) bool {
	c, err := r.Cookie(CookieName(linkID))
	if err != nil {
		return false
	}
	expStr, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() >= exp {
		return false
	}
	want := u.sign(fmt.Sprintf("%d.%s.%d", linkID, fingerprint(passwordHash), exp))
	return hmac.Equal([]byte(sig), []byte(want))
}

// ---- visitor hash ---------------------------------------------------------------

// Visitors turns an IP address and user agent into a hash that is stable
// for one UTC day and then rotates. Analytics can count unique visitors per
// day, but nobody (us included, once the day's key is gone from memory) can
// get the IP back or follow a visitor across days. The raw IP is never
// stored.
type Visitors struct{ key []byte }

// NewVisitors returns a visitor hasher.
func (k Keys) NewVisitors() *Visitors { return &Visitors{key: k.derive("visitor-hash")} }

// Hash returns the visitor hash for ip and userAgent on now's UTC day.
func (v *Visitors) Hash(ip netip.Addr, userAgent string, now time.Time) string {
	day := hmac.New(sha256.New, v.key)
	day.Write([]byte(now.UTC().Format("2006-01-02")))
	m := hmac.New(sha256.New, day.Sum(nil))
	m.Write([]byte(ip.String()))
	m.Write([]byte{0})
	m.Write([]byte(userAgent))
	return hex.EncodeToString(m.Sum(nil)[:16])
}
