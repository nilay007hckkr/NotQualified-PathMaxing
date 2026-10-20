package urlcheck

import (
	"net/netip"
	"strings"
	"testing"
)

func codes(issues []Issue) string {
	var c []string
	for _, i := range issues {
		c = append(c, i.Code)
	}
	return strings.Join(c, ",")
}

func TestCheck(t *testing.T) {
	opts := Options{
		SelfHosts: []string{"pm.example"},
		Blocklist: NewDomainSet(map[string]string{"evil.example": "phishing"}),
	}
	tests := []struct {
		name     string
		raw      string
		wantErr  string // comma-separated codes
		wantWarn string
		wantURL  string
	}{
		// Accepted.
		{name: "https", raw: "https://shop.example/sale?x=1#top", wantURL: "https://shop.example/sale?x=1#top"},
		{name: "scheme and host lowercased, path kept", raw: "HTTPS://Shop.Example:8443/Sale/A", wantURL: "https://shop.example:8443/Sale/A"},
		{name: "surrounding spaces trimmed", raw: "  https://shop.example  ", wantURL: "https://shop.example"},
		{name: "trailing dot dropped", raw: "https://shop.example./x", wantURL: "https://shop.example/x"},
		{name: "public IPv4", raw: "https://8.8.8.8/", wantURL: "https://8.8.8.8/"},
		{name: "public IPv6", raw: "https://[2606:4700::1111]:443/", wantURL: "https://[2606:4700::1111]:443/"},
		{name: "http warns", raw: "http://shop.example", wantWarn: WarnHTTP, wantURL: "http://shop.example"},
		{name: "punycode warns", raw: "https://xn--80ak6aa92e.com/", wantWarn: WarnPunycode},
		{name: "http and punycode", raw: "http://www.xn--pple-43d.com", wantWarn: WarnHTTP + "," + WarnPunycode},

		// INVALID_URL
		{name: "empty", raw: "", wantErr: CodeInvalidURL},
		{name: "blank", raw: "   ", wantErr: CodeInvalidURL},
		{name: "no scheme", raw: "shop.example/sale", wantErr: CodeInvalidURL},
		{name: "not a URL", raw: "not a url", wantErr: CodeInvalidURL},
		{name: "no host", raw: "https:///path", wantErr: CodeInvalidURL},
		{name: "only scheme", raw: "https://", wantErr: CodeInvalidURL},
		{name: "space inside", raw: "https://shop.example/big sale", wantErr: CodeInvalidURL},
		{name: "too long", raw: "https://shop.example/" + strings.Repeat("a", MaxURLLength), wantErr: CodeInvalidURL},
		{name: "label starts with dash", raw: "https://-bad.example", wantErr: CodeInvalidURL},
		{name: "empty label", raw: "https://a..example", wantErr: CodeInvalidURL},
		{name: "underscore in host", raw: "https://ex_ample.com", wantErr: CodeInvalidURL},
		{name: "numeric TLD", raw: "https://example.123", wantErr: CodeInvalidURL},
		{name: "hex IP trick", raw: "http://0x7f.1/", wantErr: CodeInvalidURL},
		{name: "non-ASCII host", raw: "https://bücher.example", wantErr: CodeInvalidURL},
		{name: "bad port", raw: "https://shop.example:99999/", wantErr: CodeInvalidURL},
		{name: "label too long", raw: "https://" + strings.Repeat("a", 64) + ".example", wantErr: CodeInvalidURL},

		// SCHEME_NOT_ALLOWED
		{name: "javascript", raw: "javascript:alert(1)", wantErr: CodeSchemeNotAllowed},
		{name: "data", raw: "data:text/html,<script>alert(1)</script>", wantErr: CodeSchemeNotAllowed},
		{name: "ftp", raw: "ftp://files.example/f", wantErr: CodeSchemeNotAllowed},
		{name: "file", raw: "file:///etc/passwd", wantErr: CodeSchemeNotAllowed},
		{name: "mailto", raw: "mailto:a@shop.example", wantErr: CodeSchemeNotAllowed},

		// USERINFO_NOT_ALLOWED
		{name: "user before host", raw: "https://yourbank.example@evil.example/", wantErr: CodeUserinfo},
		{name: "user and password", raw: "https://user:pass@shop.example", wantErr: CodeUserinfo},

		// NOT_PUBLIC
		{name: "loopback", raw: "http://127.0.0.1/", wantErr: CodeNotPublic},
		{name: "10/8", raw: "http://10.1.2.3", wantErr: CodeNotPublic},
		{name: "172.16/12", raw: "http://172.16.0.1", wantErr: CodeNotPublic},
		{name: "192.168/16", raw: "http://192.168.1.1", wantErr: CodeNotPublic},
		{name: "cloud metadata", raw: "http://169.254.169.254/latest/meta-data/", wantErr: CodeNotPublic},
		{name: "unspecified", raw: "http://0.0.0.0/", wantErr: CodeNotPublic},
		{name: "CGNAT", raw: "http://100.64.0.1", wantErr: CodeNotPublic},
		{name: "IPv6 loopback", raw: "http://[::1]/", wantErr: CodeNotPublic},
		{name: "IPv6 link-local", raw: "http://[fe80::1]/", wantErr: CodeNotPublic},
		{name: "IPv6 unique local", raw: "http://[fd00::1]/", wantErr: CodeNotPublic},
		{name: "IPv4-mapped loopback", raw: "http://[::ffff:127.0.0.1]/", wantErr: CodeNotPublic},
		{name: "localhost", raw: "http://localhost:8080/", wantErr: CodeNotPublic},
		{name: "localhost subdomain", raw: "http://app.localhost/", wantErr: CodeNotPublic},
		{name: "mDNS name", raw: "http://printer.local/", wantErr: CodeNotPublic},
		{name: "internal name", raw: "https://db.internal/", wantErr: CodeNotPublic},
		{name: "single label", raw: "http://intranet/", wantErr: CodeNotPublic},
		{name: "decimal IP", raw: "http://2130706433/", wantErr: CodeNotPublic},

		// SELF_LOOP
		{name: "our own short link", raw: "https://pm.example/abc", wantErr: CodeSelfLoop},
		{name: "our own host, other case", raw: "https://PM.Example./x", wantErr: CodeSelfLoop},

		// BLOCKED_DOMAIN
		{name: "blocked domain", raw: "https://evil.example/login", wantErr: CodeBlockedDomain},
		{name: "blocked subdomain", raw: "https://login.EVIL.example", wantErr: CodeBlockedDomain},
		{name: "similar name not blocked", raw: "https://notevil.example", wantURL: "https://notevil.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Check(tt.raw, opts)
			if got := codes(r.Errors); got != tt.wantErr {
				t.Fatalf("errors = %q, want %q (%+v)", got, tt.wantErr, r.Errors)
			}
			if got := codes(r.Warnings); got != tt.wantWarn {
				t.Errorf("warnings = %q, want %q", got, tt.wantWarn)
			}
			if tt.wantURL != "" && r.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", r.URL, tt.wantURL)
			}
			if r.OK() != (tt.wantErr == "") {
				t.Errorf("OK() = %v", r.OK())
			}
			for _, i := range append(r.Errors, r.Warnings...) {
				if i.Message == "" {
					t.Errorf("%s has no message", i.Code)
				}
			}
		})
	}
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"2606:4700::1111", true},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"172.31.255.255", false},
		{"192.168.0.1", false},
		{"169.254.169.254", false},
		{"100.100.100.100", false},
		{"0.1.2.3", false},
		{"224.0.0.1", false},
		{"255.255.255.255", false},
		{"198.18.0.1", false},
		{"192.0.2.10", false},
		{"::1", false},
		{"::", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"ff02::1", false},
		{"::ffff:10.0.0.1", false},
		{"64:ff9b::a00:1", false},
		{"2001:db8::1", false},
	}
	for _, tt := range tests {
		if got := IsPublicIP(netip.MustParseAddr(tt.ip)); got != tt.want {
			t.Errorf("IsPublicIP(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
	if IsPublicIP(netip.Addr{}) {
		t.Error("the zero Addr is public")
	}
}

func TestDomainSet(t *testing.T) {
	s := NewDomainSet(map[string]string{"Evil.Example.": "phishing", "not a domain": "x", "bad.test": "malware"})
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (the invalid entry is skipped)", s.Len())
	}
	tests := []struct {
		host string
		rule string
		ok   bool
	}{
		{"evil.example", "evil.example", true},
		{"a.b.EVIL.example.", "evil.example", true},
		{"notevil.example", "", false},
		{"evil.example.org", "", false},
		{"bad.test", "bad.test", true},
		{"test", "", false},
	}
	for _, tt := range tests {
		rule, ok := s.Match(tt.host)
		if rule != tt.rule || ok != tt.ok {
			t.Errorf("Match(%q) = %q, %v; want %q, %v", tt.host, rule, ok, tt.rule, tt.ok)
		}
	}
	if s.Reason("evil.example") != "phishing" {
		t.Errorf("Reason = %q", s.Reason("evil.example"))
	}
	var empty *DomainSet
	if _, ok := empty.Match("evil.example"); ok || empty.Len() != 0 {
		t.Error("a nil set blocks something")
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "evil.example", want: "evil.example"},
		{in: "  Evil.Example. ", want: "evil.example"},
		{in: "https://Login.Evil.Example/path?x=1", want: "login.evil.example"},
		{in: "user@evil.example:8080", want: "evil.example"},
		{in: "", wantErr: true},
		{in: "localhost", wantErr: true},
		{in: "evil example.com", wantErr: true},
		{in: "-evil.example", wantErr: true},
	}
	for _, tt := range tests {
		got, err := NormalizeDomain(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("NormalizeDomain(%q) = %q, %v; want %q (error %v)", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}
