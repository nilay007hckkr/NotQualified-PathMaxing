// Package urlcheck validates a link's destination before it is saved, so
// invalid or dangerous URLs surface in the creation wizard instead of after
// publishing. It covers the rules that need no network: the threat feeds and
// the reachability probe are the trust layer's job.
package urlcheck

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// Error and warning codes from API.md.
const (
	CodeInvalidURL       = "INVALID_URL"
	CodeSchemeNotAllowed = "SCHEME_NOT_ALLOWED"
	CodeUserinfo         = "USERINFO_NOT_ALLOWED"
	CodeSelfLoop         = "SELF_LOOP"
	CodeNotPublic        = "NOT_PUBLIC"
	CodeBlockedDomain    = "BLOCKED_DOMAIN"

	WarnHTTP      = "HTTP_NOT_HTTPS"
	WarnPunycode  = "PUNYCODE_DOMAIN"
	MaxURLLength  = 2048
	maxHostLength = 253
)

// Issue is one problem with a destination.
type Issue struct {
	Code    string
	Message string
}

// Result is the outcome of Check.
type Result struct {
	// URL is the destination with its scheme and host lowercased; the path
	// and query are kept exactly as given, because servers may treat them
	// case-sensitively.
	URL      string
	Host     string // lowercased host name or IP, without port
	Errors   []Issue
	Warnings []Issue
}

// OK reports whether the destination can be saved.
func (r Result) OK() bool { return len(r.Errors) == 0 }

// Options are the checks that depend on where the server runs.
type Options struct {
	// SelfHosts are the hosts this server answers on (BASE_URL's host). A
	// destination on one of them would send visitors in a loop or chain
	// short links, which hides the final destination from the trust layer.
	SelfHosts []string
	// Blocklist, when set, refuses destinations on blocked domains.
	Blocklist *DomainSet
}

// Check validates raw as a link destination.
func Check(raw string, opts Options) Result {
	var r Result
	fail := func(code, msg string) Result {
		r.Errors = append(r.Errors, Issue{code, msg})
		return r
	}

	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return fail(CodeInvalidURL, "destination is required")
	case len(raw) > MaxURLLength:
		return fail(CodeInvalidURL, fmt.Sprintf("destination is longer than %d characters", MaxURLLength))
	case strings.ContainsFunc(raw, func(c rune) bool { return c <= ' ' || c == 0x7f }):
		return fail(CodeInvalidURL, "destination contains spaces or control characters; encode them first")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fail(CodeInvalidURL, "destination is not a valid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme == "":
		return fail(CodeInvalidURL, "destination must start with https:// or http://")
	case scheme != "http" && scheme != "https":
		return fail(CodeSchemeNotAllowed, fmt.Sprintf("%s: links are not allowed; use https:// or http://", scheme))
	case u.Opaque != "" || u.Host == "":
		return fail(CodeInvalidURL, "destination has no host name")
	case u.User != nil:
		// https://yourbank.com@evil.example goes to evil.example.
		return fail(CodeUserinfo, "destination must not contain a user name or password before the host")
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if port := u.Port(); port != "" && !validPort(port) {
		return fail(CodeInvalidURL, "destination has an invalid port")
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if !IsPublicIP(ip) {
			return fail(CodeNotPublic, "destination points to a private, loopback or reserved address")
		}
	} else {
		if msg := hostProblem(host); msg != "" {
			return fail(CodeInvalidURL, msg)
		}
		if internalName(host) {
			return fail(CodeNotPublic, "destination is an internal host name, not a public website")
		}
	}
	r.Host = host

	for _, self := range opts.SelfHosts {
		if self = strings.TrimSuffix(strings.ToLower(self), "."); self != "" && host == self {
			return fail(CodeSelfLoop, "destination is a PathMaxing short link; link to the final page instead")
		}
	}
	if opts.Blocklist != nil {
		if rule, ok := opts.Blocklist.Match(host); ok {
			return fail(CodeBlockedDomain, fmt.Sprintf("destinations on %s are blocked", rule))
		}
	}

	u.Scheme = scheme
	u.Host = strings.ToLower(u.Host)
	if p := u.Port(); p != "" {
		u.Host = host + ":" + p
		if strings.Contains(host, ":") {
			u.Host = "[" + host + "]:" + p
		}
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	r.URL = u.String()

	if scheme == "http" {
		r.Warnings = append(r.Warnings, Issue{WarnHTTP, "destination uses http, not https; visitors' traffic can be read or changed on the way"})
	}
	for _, label := range strings.Split(host, ".") {
		if strings.HasPrefix(label, "xn--") {
			r.Warnings = append(r.Warnings, Issue{WarnPunycode, "destination uses an internationalized (xn--) domain; make sure it isn't imitating a familiar name"})
			break
		}
	}
	return r
}

func validPort(p string) bool {
	if len(p) > 5 {
		return false
	}
	n := 0
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= 1 && n <= 65535
}

// hostProblem returns why host is not a valid DNS name, or "".
func hostProblem(host string) string {
	if len(host) > maxHostLength {
		return "destination host name is too long"
	}
	for _, c := range host {
		if c > 0x7f {
			return "write internationalized domains in their xn-- form"
		}
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "destination host name is not valid"
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "destination host name is not valid"
			}
		}
	}
	// A real top-level domain is letters (or an xn-- name). This also refuses
	// numeric tricks such as 0x7f.1 or 2130706433, which browsers read as IPs.
	tld := labels[len(labels)-1]
	if len(labels) > 1 && !strings.HasPrefix(tld, "xn--") {
		for i := 0; i < len(tld); i++ {
			if tld[i] < 'a' || tld[i] > 'z' {
				return "destination host name has no valid top-level domain"
			}
		}
		if len(tld) < 2 {
			return "destination host name has no valid top-level domain"
		}
	}
	return ""
}

// internalName reports names that only resolve inside a private network.
func internalName(host string) bool {
	if !strings.Contains(host, ".") {
		return true // "intranet", "localhost", "2130706433"
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home.arpa", ".intranet", ".corp"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// nonPublic lists the ranges IsPublicIP refuses on top of the standard
// library's private, loopback, link-local, multicast and unspecified checks.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes 255.255.255.255
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: embeds an IPv4 address
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

// IsPublicIP reports whether ip is a public unicast address that a website
// could live on. The trust layer uses it again after DNS resolution and on
// every redirect, because a public name can still resolve to a private IP.
func IsPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap() // ::ffff:127.0.0.1 is 127.0.0.1
	if !ip.IsValid() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
