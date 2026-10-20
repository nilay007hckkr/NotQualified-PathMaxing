package domain

import (
	"crypto/rand"
	"fmt"
	"net/netip"
	"time"
)

// TrafficClass labels who (or what) made a visit. Only humans use up click
// caps and one-time links.
type TrafficClass string

const (
	ClassHuman        TrafficClass = "human"
	ClassEmailScanner TrafficClass = "email_scanner" // probable: scanners publish no signatures
	ClassLinkPreview  TrafficClass = "link_preview"  // chat apps fetching a preview card
	ClassCrawler      TrafficClass = "crawler"       // search engines and other self-declared crawlers
	ClassOtherBot     TrafficClass = "other_bot"
)

// TrafficClasses returns every class, in the order the API reports them.
func TrafficClasses() []TrafficClass {
	return []TrafficClass{ClassHuman, ClassEmailScanner, ClassLinkPreview, ClassCrawler, ClassOtherBot}
}

// IsHuman reports whether c counts as a person.
func (c TrafficClass) IsHuman() bool { return c == ClassHuman }

// Valid reports whether c is one of the known classes.
func (c TrafficClass) Valid() bool {
	for _, k := range TrafficClasses() {
		if c == k {
			return true
		}
	}
	return false
}

// Source says how the visitor reached the link.
type Source string

const (
	SourceLink Source = "link"
	SourceQR   Source = "qr" // the QR code encodes /{code}?s=qr
)

// Outcome is what the redirect engine did with a visit. Refused visits are
// recorded too, so expired and disabled links stay traceable in reports.
type Outcome string

const (
	OutcomeRedirected       Outcome = "redirected"
	OutcomeExpired          Outcome = "expired"
	OutcomeCapReached       Outcome = "cap_reached"
	OutcomeBlockedAdmin     Outcome = "blocked_admin"
	OutcomeBlockedDomain    Outcome = "blocked_domain"
	OutcomeQuarantined      Outcome = "quarantined"
	OutcomeInactive         Outcome = "inactive"
	OutcomeNotStarted       Outcome = "not_started"
	OutcomePasswordRequired Outcome = "password_required"
)

// Outcomes returns every outcome, in the order the API reports them.
func Outcomes() []Outcome {
	return []Outcome{
		OutcomeRedirected, OutcomeExpired, OutcomeCapReached, OutcomeBlockedAdmin,
		OutcomeBlockedDomain, OutcomeQuarantined, OutcomeInactive, OutcomeNotStarted,
		OutcomePasswordRequired,
	}
}

// ClickEvent is one visit to a link, allowed or refused. It never holds the
// visitor's IP address: VisitorHash is a daily-rotating HMAC of it, and
// Country is looked up before the address is dropped.
type ClickEvent struct {
	// EventID is unique per visit, so a batch insert that is retried after a
	// timeout can't count the same visit twice.
	EventID     string
	LinkID      int64
	WorkspaceID int64
	At          time.Time
	Outcome     Outcome
	Class       TrafficClass
	ClassReason string // why the classifier chose Class, e.g. "ua_link_preview"
	Source      Source
	Method      string // GET or HEAD
	Device      Device
	Browser     string
	OS          string
	// ReferrerHost is only the host of the Referer header: the full URL can
	// carry personal data in its path or query.
	ReferrerHost string
	Country      string // ISO 3166-1 alpha-2, empty when unknown
	VisitorHash  string
}

// NewEventID returns a random version 4 UUID.
func NewEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("domain: crypto/rand failed: %v", err))
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RequestInfo is what a traffic classifier gets to see about a visit. The IP
// is only ever held in memory.
type RequestInfo struct {
	Method    string
	UserAgent string
	Referrer  string
	// Purpose is the Sec-Purpose or Purpose header; browsers set it to
	// "prefetch" when they load a page nobody has clicked yet.
	Purpose string
	IP      netip.Addr
	At      time.Time
}

// Classification is a traffic class plus the reason it was chosen.
type Classification struct {
	Class  TrafficClass
	Reason string
}
