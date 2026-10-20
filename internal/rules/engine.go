// Package rules decides what happens to a visit to a short link.
//
// The rules run in a fixed order and the first refusal wins:
//
//  1. Blocked: disabled by an admin, quarantined by the trust scanner, or a
//     destination on the domain blocklist → 451
//  2. Inactive: paused, archived or deleted by the owner → 410
//  3. Schedule: not started yet → 404; expired (expires_at <= now) → 410
//  4. Password: no valid unlock cookie → the password form
//  5. Click cap: reserve one click atomically; none left → 410
//
// Then device targeting picks the destination and the visitor gets a 302.
// Rules 1-4 only read, and the click cap is the only rule that writes, so it
// runs last: a visit refused for any other reason never uses up a click.
package rules

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pathmaxing/internal/domain"
)

// Visit is what the engine needs to know about a request.
type Visit struct {
	Device domain.Device
	// Human visits use up the click cap. Bots (link previews, scanners,
	// crawlers) only check whether it is full, so a chat app fetching a
	// preview can't burn a one-time link.
	Human bool
	// Unlocked is true when the request carries a valid unlock cookie for
	// this link's current password.
	Unlocked bool
	Now      time.Time
}

// Decision is the engine's answer.
type Decision struct {
	Outcome domain.Outcome
	// Status is the HTTP status to send: 302 for a redirect, 200 for the
	// password form, otherwise the refusal's status.
	Status int
	// Destination is set when the visit is redirected.
	Destination string
	// BlockedBy is the blocklist entry that matched, for blocked_domain.
	BlockedBy string
}

// Redirected reports whether the visitor is sent to the destination.
func (d Decision) Redirected() bool { return d.Outcome == domain.OutcomeRedirected }

// Engine applies the rules. Both dependencies are required.
type Engine struct {
	Blocklist domain.Blocklist
	Caps      domain.CapReserver
}

// Decide applies the rules to one visit of l. An error means a dependency
// failed (for example the database during a cap reservation); the caller
// should answer 503 rather than guess, so a cap can never be exceeded.
func (e *Engine) Decide(ctx context.Context, l domain.Link, v Visit) (Decision, error) {
	// 1. Blocked.
	if l.Status == domain.StatusDisabledAdmin {
		return refuse(domain.OutcomeBlockedAdmin, http.StatusUnavailableForLegalReasons), nil
	}
	if l.TrustState == domain.TrustQuarantined {
		return refuse(domain.OutcomeQuarantined, http.StatusUnavailableForLegalReasons), nil
	}
	for _, dest := range l.Destinations() {
		rule, blocked, err := e.Blocklist.Blocked(ctx, hostOf(dest))
		if err != nil {
			return Decision{}, fmt.Errorf("check blocklist: %w", err)
		}
		if blocked {
			d := refuse(domain.OutcomeBlockedDomain, http.StatusUnavailableForLegalReasons)
			d.BlockedBy = rule
			return d, nil
		}
	}

	// 2. Inactive.
	if l.Deleted() || l.Status == domain.StatusPaused || l.Status == domain.StatusArchived {
		return refuse(domain.OutcomeInactive, http.StatusGone), nil
	}

	// 3. Schedule. Expiry is computed here, on every visit, never stored.
	if l.NotStarted(v.Now) {
		return refuse(domain.OutcomeNotStarted, http.StatusNotFound), nil
	}
	if l.IsExpired(v.Now) {
		return refuse(domain.OutcomeExpired, http.StatusGone), nil
	}

	// 4. Password.
	if l.HasPassword() && !v.Unlocked {
		return Decision{Outcome: domain.OutcomePasswordRequired, Status: http.StatusOK}, nil
	}

	// 5. Click cap: the only rule that writes, so it comes last.
	if l.MaxClicks != nil {
		if v.Human {
			ok, err := e.Caps.ReserveClick(ctx, l.ID, *l.MaxClicks)
			if err != nil {
				return Decision{}, fmt.Errorf("reserve click: %w", err)
			}
			if !ok {
				return refuse(domain.OutcomeCapReached, http.StatusGone), nil
			}
		} else if l.CapReached() {
			return refuse(domain.OutcomeCapReached, http.StatusGone), nil
		}
	}

	return Decision{
		Outcome:     domain.OutcomeRedirected,
		Status:      http.StatusFound,
		Destination: l.DestinationFor(v.Device),
	}, nil
}

func refuse(o domain.Outcome, status int) Decision {
	return Decision{Outcome: o, Status: status}
}

// hostOf returns the lowercased host of a destination. Destinations were
// validated when the link was saved; an unparsable one yields "" and is
// simply not matched.
func hostOf(dest string) string {
	u, err := url.Parse(dest)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
