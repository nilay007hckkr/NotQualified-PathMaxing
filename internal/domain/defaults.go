package domain

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
)

// The defaults below are what the server runs with until a later part
// provides the real implementation. Each one fails safe: the API stays
// closed, nothing is cached so nothing can go stale, and skipped checks say
// that they were skipped.

// DenyAllGuard refuses every API request. It is the default until the auth
// module provides sessions, so a half-built server is closed, not open.
type DenyAllGuard struct{}

// Authenticate always fails.
func (DenyAllGuard) Authenticate(*http.Request) (Principal, error) {
	return Principal{}, fmt.Errorf("no auth module is configured: %w", ErrUnauthenticated)
}

// NoopInvalidator is the invalidator to use when nothing is cached.
type NoopInvalidator struct{}

// Invalidate does nothing.
func (NoopInvalidator) Invalidate(string) {}

// SkipTrustChecker runs no checks and reports both as skipped, so the
// creation wizard can say so instead of implying the destination was checked.
type SkipTrustChecker struct{}

// Precheck reports that every check was skipped.
func (SkipTrustChecker) Precheck(context.Context, string) (Precheck, error) {
	return Precheck{Skipped: []string{
		"threat_feeds: trust module not configured",
		"reachability: trust module not configured",
	}}, nil
}

// NoGeo knows no countries.
type NoGeo struct{}

// Country always returns "".
func (NoGeo) Country(netip.Addr) string { return "" }
