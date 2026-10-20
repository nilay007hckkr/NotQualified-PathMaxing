// Package domain holds the types and interfaces that the parts of PathMaxing
// share. A part never imports a part with a higher number, so whenever a
// lower part needs something a higher part implements (the redirect engine
// needs the Postgres store, the trust layer needs the auth guard), the
// contract lives here and the implementation is plugged in through the
// module registry in internal/app.
package domain

import (
	"strings"
	"time"
)

// LinkStatus is set by the owner or an admin. Expiry is deliberately not a
// status: it is computed on every visit from ExpiresAt, so changing the
// expiry date can never leave a stale "expired" flag behind.
type LinkStatus string

const (
	StatusActive        LinkStatus = "active"
	StatusPaused        LinkStatus = "paused"
	StatusArchived      LinkStatus = "archived"
	StatusDisabledAdmin LinkStatus = "disabled_admin"
)

// Valid reports whether s is one of the known statuses.
func (s LinkStatus) Valid() bool {
	switch s {
	case StatusActive, StatusPaused, StatusArchived, StatusDisabledAdmin:
		return true
	}
	return false
}

// TrustState is what the trust scanner last concluded about a link's
// destination.
type TrustState string

const (
	TrustOK          TrustState = "ok"
	TrustDegraded    TrustState = "degraded"    // the destination failed twice in a row
	TrustChanged     TrustState = "changed"     // it now ends on a different domain
	TrustQuarantined TrustState = "quarantined" // a threat feed flagged it; visitors get a warning
)

// Valid reports whether t is one of the known trust states.
func (t TrustState) Valid() bool {
	switch t {
	case TrustOK, TrustDegraded, TrustChanged, TrustQuarantined:
		return true
	}
	return false
}

// Device is the kind of device a visit came from, used for device
// targeting and analytics. Visits whose device can't be told apart (most
// bots) have an empty Device.
type Device string

const (
	DeviceMobile  Device = "mobile"
	DeviceTablet  Device = "tablet"
	DeviceDesktop Device = "desktop"
)

// Valid reports whether d can be used in a device target.
func (d Device) Valid() bool {
	return d == DeviceMobile || d == DeviceTablet || d == DeviceDesktop
}

// Target sends visits from one kind of device to its own destination.
type Target struct {
	Device      Device
	Destination string
}

// Link is a short link. It is the hub that campaigns, tags, device targets,
// click events and QR codes hang off.
type Link struct {
	ID          int64
	WorkspaceID int64
	OwnerID     int64
	// Code is stored as the owner typed it; lookups ignore case, so "Sale"
	// and "sale" are the same link.
	Code        string
	Destination string
	Title       string
	Status      LinkStatus
	TrustState  TrustState

	StartsAt  *time.Time // nil: live immediately
	ExpiresAt *time.Time // nil: never expires

	MaxClicks *int64 // nil: no click cap
	CapUsed   int64  // human clicks that used up the cap so far

	PasswordHash []byte // bcrypt hash; empty when the link has no password
	Targets      []Target

	CampaignID *int64
	FolderID   *int64
	TagIDs     []int64

	LastCheckedAt *time.Time // last trust check, nil if never checked
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time // soft delete: the code is never reused
}

// NormalizeCode returns the form codes are compared in. The unique index on
// lower(code) enforces the same rule in Postgres.
func NormalizeCode(code string) string { return strings.ToLower(code) }

// IsExpired reports whether the link has expired at now. A link whose
// expiry is exactly now counts as expired.
func (l Link) IsExpired(now time.Time) bool {
	return l.ExpiresAt != nil && !now.Before(*l.ExpiresAt)
}

// NotStarted reports whether the link's start time is still in the future.
func (l Link) NotStarted(now time.Time) bool {
	return l.StartsAt != nil && now.Before(*l.StartsAt)
}

// HasPassword reports whether visitors must unlock the link first.
func (l Link) HasPassword() bool { return len(l.PasswordHash) > 0 }

// CapReached reports whether a capped link has used up all its clicks, as
// far as this copy of the link knows. The decision that matters is the
// atomic reservation in CapReserver; this is only for read-only checks.
func (l Link) CapReached() bool {
	return l.MaxClicks != nil && l.CapUsed >= *l.MaxClicks
}

// Deleted reports whether the owner soft-deleted the link.
func (l Link) Deleted() bool { return l.DeletedAt != nil }

// DestinationFor returns the destination for a visit from device: the
// matching device target if there is one, otherwise the main destination.
func (l Link) DestinationFor(device Device) string {
	for _, t := range l.Targets {
		if t.Device == device && device != "" {
			return t.Destination
		}
	}
	return l.Destination
}

// Destinations returns the main destination followed by every device
// target's, so a check (such as the blocklist) can cover all of them.
func (l Link) Destinations() []string {
	out := make([]string, 0, 1+len(l.Targets))
	out = append(out, l.Destination)
	for _, t := range l.Targets {
		out = append(out, t.Destination)
	}
	return out
}

// Clone returns a deep copy, so a store can hand out links without sharing
// slices or pointers with its own copy.
func (l Link) Clone() Link {
	c := l
	c.StartsAt = clonePtr(l.StartsAt)
	c.ExpiresAt = clonePtr(l.ExpiresAt)
	c.MaxClicks = clonePtr(l.MaxClicks)
	c.CampaignID = clonePtr(l.CampaignID)
	c.FolderID = clonePtr(l.FolderID)
	c.LastCheckedAt = clonePtr(l.LastCheckedAt)
	c.DeletedAt = clonePtr(l.DeletedAt)
	if l.PasswordHash != nil {
		c.PasswordHash = append([]byte(nil), l.PasswordHash...)
	}
	if l.Targets != nil {
		c.Targets = append([]Target(nil), l.Targets...)
	}
	if l.TagIDs != nil {
		c.TagIDs = append([]int64(nil), l.TagIDs...)
	}
	return c
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
