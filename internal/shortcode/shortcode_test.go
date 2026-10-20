package shortcode

import (
	"slices"
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	seen := make(map[string]bool)
	counts := make(map[rune]int)
	const n = 20000
	for range n {
		c := Generate()
		if len(c) != Length {
			t.Fatalf("Generate() = %q, want %d characters", c, Length)
		}
		if strings.ToLower(c) != c {
			t.Fatalf("Generate() = %q is not lowercase", c)
		}
		if code, _ := CheckAlias(c); code != "" {
			t.Fatalf("generated code %q fails the alias rules with %s", c, code)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q in %d draws", c, n)
		}
		seen[c] = true
		for _, r := range c {
			counts[r]++
		}
	}
	// Every character should appear, and none wildly more than others:
	// each is expected n*8/36 ≈ 4444 times.
	if len(counts) != len(alphabet) {
		t.Fatalf("only %d of %d characters appeared", len(counts), len(alphabet))
	}
	for r, c := range counts {
		if c < 3800 || c > 5100 {
			t.Errorf("character %q appeared %d times, expected about 4444", r, c)
		}
	}
}

func TestCheckAlias(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"diwali", ""},
		{"Sale", ""},
		{"black-friday_2026", ""},
		{"abc", ""},
		{strings.Repeat("a", MaxAliasLen), ""},
		{"ab", CodeAliasInvalid},
		{strings.Repeat("a", MaxAliasLen+1), CodeAliasInvalid},
		{"", CodeAliasInvalid},
		{"-sale", CodeAliasInvalid},
		{"sale_", CodeAliasInvalid},
		{"big sale", CodeAliasInvalid},
		{"sale/2", CodeAliasInvalid},
		{"sale.html", CodeAliasInvalid},
		{"café", CodeAliasInvalid},
		{"sale?x", CodeAliasInvalid},
		{"%2e%2e", CodeAliasInvalid},
		{"api", CodeAliasReserved},
		{"API", CodeAliasReserved},
		{"Admin", CodeAliasReserved},
		{"healthz", CodeAliasReserved},
		{"signup", CodeAliasReserved},
		{"apis", ""},
	}
	for _, tt := range tests {
		code, msg := CheckAlias(tt.alias)
		if code != tt.want {
			t.Errorf("CheckAlias(%q) = %q (%s), want %q", tt.alias, code, msg, tt.want)
		}
		if code != "" && msg == "" {
			t.Errorf("CheckAlias(%q) gave code %s without a message", tt.alias, code)
		}
	}
}

// Every reserved word from API.md must be refused, and the list must match.
func TestReservedMatchesAPI(t *testing.T) {
	fromAPI := []string{"api", "app", "p", "bio", "admin", "login", "logout", "signup", "static", "assets", "health", "healthz"}
	slices.Sort(fromAPI)
	if !slices.Equal(Reserved(), fromAPI) {
		t.Fatalf("Reserved() = %v, API.md lists %v", Reserved(), fromAPI)
	}
	for _, w := range fromAPI {
		if !IsReserved(w) || !IsReserved(strings.ToUpper(w)) {
			t.Errorf("%q is not reserved", w)
		}
		if len(w) >= Length {
			t.Errorf("reserved word %q is as long as a generated code", w)
		}
	}
}
