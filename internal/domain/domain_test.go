package domain

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestLinkSchedule(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		starts, expiry *time.Time
		wantNotStarted bool
		wantExpired    bool
	}{
		{name: "no schedule"},
		{name: "starts in the future", starts: ptr(now.Add(time.Minute)), wantNotStarted: true},
		{name: "started exactly now", starts: ptr(now)},
		{name: "expires in the future", expiry: ptr(now.Add(time.Nanosecond))},
		{name: "expires exactly now counts as expired", expiry: ptr(now), wantExpired: true},
		{name: "expired in the past", expiry: ptr(now.Add(-time.Hour)), wantExpired: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := Link{StartsAt: tt.starts, ExpiresAt: tt.expiry}
			if got := l.NotStarted(now); got != tt.wantNotStarted {
				t.Errorf("NotStarted = %v, want %v", got, tt.wantNotStarted)
			}
			if got := l.IsExpired(now); got != tt.wantExpired {
				t.Errorf("IsExpired = %v, want %v", got, tt.wantExpired)
			}
		})
	}
}

func TestLinkCapAndPassword(t *testing.T) {
	tests := []struct {
		name    string
		link    Link
		wantCap bool
		wantPwd bool
	}{
		{name: "no cap", link: Link{CapUsed: 1000}},
		{name: "under the cap", link: Link{MaxClicks: ptr(int64(3)), CapUsed: 2}, wantCap: false},
		{name: "at the cap", link: Link{MaxClicks: ptr(int64(3)), CapUsed: 3}, wantCap: true},
		{name: "password", link: Link{PasswordHash: []byte("$2a$...")}, wantPwd: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.link.CapReached(); got != tt.wantCap {
				t.Errorf("CapReached = %v, want %v", got, tt.wantCap)
			}
			if got := tt.link.HasPassword(); got != tt.wantPwd {
				t.Errorf("HasPassword = %v, want %v", got, tt.wantPwd)
			}
		})
	}
}

func TestDestinationFor(t *testing.T) {
	l := Link{
		Destination: "https://shop.example/sale",
		Targets:     []Target{{Device: DeviceMobile, Destination: "https://m.shop.example/sale"}},
	}
	tests := []struct {
		device Device
		want   string
	}{
		{DeviceMobile, "https://m.shop.example/sale"},
		{DeviceDesktop, "https://shop.example/sale"},
		{DeviceTablet, "https://shop.example/sale"},
		{"", "https://shop.example/sale"},
	}
	for _, tt := range tests {
		if got := l.DestinationFor(tt.device); got != tt.want {
			t.Errorf("DestinationFor(%q) = %q, want %q", tt.device, got, tt.want)
		}
	}
	if got := l.Destinations(); len(got) != 2 || got[1] != "https://m.shop.example/sale" {
		t.Errorf("Destinations = %v", got)
	}
}

func TestCloneSharesNothing(t *testing.T) {
	orig := Link{
		ExpiresAt: ptr(time.Unix(100, 0)), MaxClicks: ptr(int64(5)),
		Targets: []Target{{Device: DeviceMobile, Destination: "a"}}, TagIDs: []int64{1},
		PasswordHash: []byte("h"),
	}
	c := orig.Clone()
	*c.ExpiresAt = time.Unix(200, 0)
	*c.MaxClicks = 9
	c.Targets[0].Destination = "b"
	c.TagIDs[0] = 2
	c.PasswordHash[0] = 'x'
	if orig.ExpiresAt.Unix() != 100 || *orig.MaxClicks != 5 || orig.Targets[0].Destination != "a" ||
		orig.TagIDs[0] != 1 || orig.PasswordHash[0] != 'h' {
		t.Fatalf("changing the clone changed the original: %+v", orig)
	}
}

func TestNormalizeCode(t *testing.T) {
	if NormalizeCode("Sale") != NormalizeCode("sale") {
		t.Fatal("codes differing only in case must normalize to the same value")
	}
}

func TestEnumsValid(t *testing.T) {
	for _, s := range []LinkStatus{StatusActive, StatusPaused, StatusArchived, StatusDisabledAdmin} {
		if !s.Valid() {
			t.Errorf("%q not valid", s)
		}
	}
	for _, s := range []TrustState{TrustOK, TrustDegraded, TrustChanged, TrustQuarantined} {
		if !s.Valid() {
			t.Errorf("%q not valid", s)
		}
	}
	for _, c := range TrafficClasses() {
		if !c.Valid() {
			t.Errorf("%q not valid", c)
		}
	}
	if LinkStatus("expired").Valid() || TrustState("bad").Valid() || TrafficClass("robot").Valid() || Device("watch").Valid() || Device("").Valid() {
		t.Error("an unknown value was accepted")
	}
	if len(Outcomes()) != 9 {
		t.Errorf("Outcomes() has %d entries, API.md lists 9", len(Outcomes()))
	}
	if !ClassHuman.IsHuman() || ClassLinkPreview.IsHuman() {
		t.Error("IsHuman is wrong")
	}
}

func TestNewEventID(t *testing.T) {
	uuid4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := make(map[string]bool)
	for range 1000 {
		id := NewEventID()
		if !uuid4.MatchString(id) {
			t.Fatalf("%q is not a version 4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("duplicate event ID %q", id)
		}
		seen[id] = true
	}
}

func TestPrincipalCanAccess(t *testing.T) {
	tests := []struct {
		name string
		p    Principal
		ws   int64
		want bool
	}{
		{"own workspace", Principal{WorkspaceID: 7, Role: RoleUser}, 7, true},
		{"someone else's", Principal{WorkspaceID: 7, Role: RoleUser}, 8, false},
		{"admin sees everything", Principal{WorkspaceID: 1, Role: RoleAdmin}, 8, true},
		{"no workspace sees nothing", Principal{Role: RoleUser}, 0, false},
	}
	for _, tt := range tests {
		if got := tt.p.CanAccess(tt.ws); got != tt.want {
			t.Errorf("%s: CanAccess = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDefaultsFailSafe(t *testing.T) {
	if _, err := (DenyAllGuard{}).Authenticate(httptest.NewRequest("GET", "/api/links", nil)); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("DenyAllGuard error = %v, want ErrUnauthenticated", err)
	}
	pc, err := (SkipTrustChecker{}).Precheck(context.Background(), "https://example.com")
	if err != nil || pc.ThreatMatch || pc.Reachable != nil || len(pc.Skipped) != 2 {
		t.Errorf("SkipTrustChecker = %+v, %v; want two skipped checks and no verdict", pc, err)
	}
	if c := (NoGeo{}).Country(netip.MustParseAddr("8.8.8.8")); c != "" {
		t.Errorf("NoGeo = %q", c)
	}
	NoopInvalidator{}.Invalidate("x")
}
