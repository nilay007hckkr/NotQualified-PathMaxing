package domain

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"time"
)

// Errors shared across parts. Implementations wrap them with %w, and callers
// test with errors.Is.
var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrUnauthenticated = errors.New("not authenticated")
)

// Role is a user's role. Visitors have no account and no role.
type Role string

const (
	RoleUser  Role = "user"  // sees and manages only their own workspace
	RoleAdmin Role = "admin" // sees everything, handles takedowns and the blocklist
)

// Principal is the authenticated user behind an API request.
type Principal struct {
	UserID      int64
	WorkspaceID int64
	Role        Role
	Email       string
	Name        string
}

// IsAdmin reports whether p has the admin role.
func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

// CanAccess reports whether p may see a resource that belongs to
// workspaceID. Callers answer "no" with 404, never 403, so nobody can probe
// for other users' links.
func (p Principal) CanAccess(workspaceID int64) bool {
	return p.IsAdmin() || (p.WorkspaceID != 0 && p.WorkspaceID == workspaceID)
}

// AuditEntry records who did what to which entity.
type AuditEntry struct {
	ActorID    *int64 // nil when the system acted, e.g. the trust scanner
	Action     string // e.g. "link.disabled", "link.quarantined"
	EntityType string // e.g. "link", "blocked_domain"
	EntityID   int64
	Detail     map[string]any
	At         time.Time
}

// Precheck is what the trust layer can say about a destination before a link
// is published.
type Precheck struct {
	ThreatMatch   bool
	ThreatSources []string // feeds that flagged it, e.g. "safe_browsing"
	Reachable     *bool    // nil when the reachability probe did not run
	Hops          int      // redirects the probe followed
	FinalURL      string
	Skipped       []string // checks that did not run, with the reason
}

// LinkResolver finds the link for a short code, ignoring case. It returns
// soft-deleted links too (the redirect answers those with 410 Gone) and
// ErrNotFound when no link ever had the code.
type LinkResolver interface {
	ResolveCode(ctx context.Context, code string) (Link, error)
}

// CapReserver atomically uses up one click of a capped link. It reports
// false when the cap is already reached, and must never let concurrent
// callers exceed max between them.
type CapReserver interface {
	ReserveClick(ctx context.Context, linkID, max int64) (bool, error)
}

// LinkStore is the read side of the link store that the redirect engine and
// trust layer need. The in-memory store implements it until the Postgres
// store replaces it.
type LinkStore interface {
	LinkResolver
	CapReserver
	GetLink(ctx context.Context, id int64) (Link, error)
}

// Blocklist reports whether an admin blocked host, either directly or by
// blocking one of its parent domains. rule is the blocked domain that
// matched. It is called on every visit, so implementations answer from
// memory.
type Blocklist interface {
	Blocked(ctx context.Context, host string) (rule string, blocked bool, err error)
}

// ClickSink receives every visit. Record must never block: the redirect does
// not wait for click logging.
type ClickSink interface {
	Record(e ClickEvent)
}

// TrafficClassifier labels a visit.
type TrafficClassifier interface {
	Classify(r RequestInfo) Classification
}

// TrustChecker checks a destination before a link is published: threat
// feeds and a reachability probe.
type TrustChecker interface {
	Precheck(ctx context.Context, rawURL string) (Precheck, error)
}

// CacheInvalidator drops a cached link. Call it after the change that made
// the cached copy stale has been committed.
type CacheInvalidator interface {
	Invalidate(code string)
}

// AuthGuard authenticates an API request: a valid session, and for
// state-changing requests an Origin that belongs to us. It returns
// ErrUnauthenticated (wrapped) when the request must be refused.
type AuthGuard interface {
	Authenticate(r *http.Request) (Principal, error)
}

// GeoLookup maps an IP address to an ISO country code, or "" when unknown.
type GeoLookup interface {
	Country(ip netip.Addr) string
}

// AuditLog stores audit entries.
type AuditLog interface {
	Append(ctx context.Context, e AuditEntry) error
}
