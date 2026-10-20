package trust

import (
	"strconv"
	"strings"
	"time"

	"pathmaxing/internal/domain"
)

// State is what the scanner remembers about a link between checks.
type State struct {
	Trust    domain.TrustState
	Failures int // failed checks in a row
	// BaselineHost is the final host of the first good check of
	// BaselineDestination. Ending on another host later means "changed".
	BaselineHost        string
	BaselineDestination string
}

// Observation is everything one scan found.
type Observation struct {
	Destination string
	Fetch       Result
	Threat      bool     // a threat feed flagged the destination or where it ends
	ThreatWhy   []string // e.g. "safe_browsing: SOCIAL_ENGINEERING"
	BlockedBy   string   // blocklist entry matching a host on the way, if any
}

// Verdicts recorded per check. "failed" is one failed check that hasn't
// made the link degraded yet.
const (
	VerdictOK          = "ok"
	VerdictFailed      = "failed"
	VerdictDegraded    = "degraded"
	VerdictChanged     = "changed"
	VerdictQuarantined = "quarantined"
)

// failuresToDegrade is how many failed checks in a row mark a link
// degraded. One failure is often a blip; two in a row usually isn't.
const failuresToDegrade = 2

// Transition is the outcome of applying an observation to a state.
type Transition struct {
	Next        State
	Verdict     string
	Quarantined bool   // true only when this check quarantined the link
	Detail      string // why, for the timeline and the audit log
}

// Evaluate is the trust state machine. It has no side effects, so every
// rule is a table test:
//
//   - a threat-feed hit, or a blocked host anywhere on the redirect chain →
//     quarantined (only an admin releases it);
//   - two failed checks in a row (no answer, or a 4xx/5xx) → degraded;
//   - a good check that ends on a different host than the baseline → changed;
//   - a good check on the baseline host → ok.
//
// Editing the destination starts a new baseline.
func Evaluate(prev State, obs Observation) Transition {
	next := prev
	if next.Trust == "" {
		next.Trust = domain.TrustOK
	}
	if obs.Destination != prev.BaselineDestination {
		next.BaselineDestination = obs.Destination
		next.BaselineHost = ""
		next.Failures = 0
		if next.Trust != domain.TrustQuarantined {
			next.Trust = domain.TrustOK
		}
	}

	if obs.Threat || obs.BlockedBy != "" {
		var why []string
		why = append(why, obs.ThreatWhy...)
		if obs.BlockedBy != "" {
			why = append(why, "blocklist: "+obs.BlockedBy)
		}
		was := next.Trust
		next.Trust = domain.TrustQuarantined
		return Transition{Next: next, Verdict: VerdictQuarantined, Quarantined: was != domain.TrustQuarantined, Detail: strings.Join(why, "; ")}
	}
	if next.Trust == domain.TrustQuarantined {
		// Clean now, but quarantine is sticky: an admin decides when to release.
		return Transition{Next: next, Verdict: VerdictQuarantined, Detail: "still quarantined until an admin releases it"}
	}

	if !obs.Fetch.Reachable() {
		next.Failures++
		detail := obs.Fetch.Error
		switch {
		case detail != "":
		case obs.Fetch.Status == 0:
			detail = "no response"
		default:
			detail = "HTTP " + strconv.Itoa(obs.Fetch.Status)
		}
		if next.Failures >= failuresToDegrade {
			next.Trust = domain.TrustDegraded
			return Transition{Next: next, Verdict: VerdictDegraded, Detail: detail}
		}
		return Transition{Next: next, Verdict: VerdictFailed, Detail: detail}
	}

	next.Failures = 0
	host := normalHost(obs.Fetch.FinalHost)
	if next.BaselineHost == "" {
		next.BaselineHost = host
	}
	if host != next.BaselineHost {
		next.Trust = domain.TrustChanged
		return Transition{Next: next, Verdict: VerdictChanged, Detail: "now ends on " + host + ", was " + next.BaselineHost}
	}
	next.Trust = domain.TrustOK
	return Transition{Next: next, Verdict: VerdictOK}
}

// normalHost ignores a leading "www.", which sites add and drop freely.
func normalHost(h string) string { return strings.TrimPrefix(strings.ToLower(h), "www.") }

// CheckRecord is one row of a link's trust timeline.
type CheckRecord struct {
	CheckedAt   time.Time
	Destination string
	HTTPStatus  int
	FinalURL    string
	FinalDomain string
	Hops        int
	Error       string
	Verdict     string
	Sources     []string
	Detail      string
}
