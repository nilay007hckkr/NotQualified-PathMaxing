package urlcheck

import (
	"fmt"
	"strings"
)

// DomainSet is a set of blocked domains. Blocking a domain also blocks every
// subdomain: blocking evil.example blocks login.evil.example, but not
// notevil.example. A DomainSet is never changed after it is built, so it can
// be read from many goroutines; to change the blocklist, build a new set and
// swap it in.
type DomainSet struct {
	reasons map[string]string
}

// NewDomainSet builds a set from blocked domain -> reason. Domains are
// normalized the same way as NormalizeDomain; invalid ones are skipped.
func NewDomainSet(entries map[string]string) *DomainSet {
	s := &DomainSet{reasons: make(map[string]string, len(entries))}
	for d, reason := range entries {
		if n, err := NormalizeDomain(d); err == nil {
			s.reasons[n] = reason
		}
	}
	return s
}

// Len returns the number of blocked domains.
func (s *DomainSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.reasons)
}

// Match reports whether host is blocked, and by which blocked domain. It
// walks up the labels (a.b.evil.example, b.evil.example, evil.example), so
// the cost depends on the host, not on the size of the blocklist.
func (s *DomainSet) Match(host string) (rule string, blocked bool) {
	if s == nil || len(s.reasons) == 0 {
		return "", false
	}
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	for h != "" {
		if _, ok := s.reasons[h]; ok {
			return h, true
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	return "", false
}

// Reason returns why domain was blocked.
func (s *DomainSet) Reason(domain string) string {
	if s == nil {
		return ""
	}
	return s.reasons[domain]
}

// NormalizeDomain turns an admin's input into the stored form of a blocked
// domain: lowercase, no trailing dot, and forgiving of a pasted URL
// ("https://Evil.Example/login" becomes "evil.example").
func NormalizeDomain(input string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(input))
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndexByte(d, '@'); i >= 0 {
		d = d[i+1:]
	}
	if i := strings.LastIndexByte(d, ':'); i >= 0 && !strings.Contains(d[:i], ":") {
		d = d[:i] // drop a port
	}
	d = strings.TrimSuffix(d, ".")
	if d == "" {
		return "", fmt.Errorf("domain is empty")
	}
	if msg := hostProblem(d); msg != "" || !strings.Contains(d, ".") {
		return "", fmt.Errorf("%q is not a domain name", input)
	}
	return d, nil
}
