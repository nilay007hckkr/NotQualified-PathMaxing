package trust

import (
	"testing"

	"pathmaxing/internal/domain"
)

func TestEvaluate(t *testing.T) {
	const dest = "https://shop.example/sale"
	good := Result{Status: 200, FinalHost: "shop.example"}
	goodWWW := Result{Status: 200, FinalHost: "www.shop.example"}
	moved := Result{Status: 200, FinalHost: "parked-domains.example"}
	down := Result{Error: "connection refused"}
	notFound := Result{Status: 404, FinalHost: "shop.example"}
	base := State{Trust: domain.TrustOK, BaselineHost: "shop.example", BaselineDestination: dest}
	failedOnce := State{Trust: domain.TrustOK, Failures: 1, BaselineHost: "shop.example", BaselineDestination: dest}
	degraded := State{Trust: domain.TrustDegraded, Failures: 3, BaselineHost: "shop.example", BaselineDestination: dest}
	quarantined := State{Trust: domain.TrustQuarantined, BaselineHost: "shop.example", BaselineDestination: dest}
	edited := State{Trust: domain.TrustChanged, Failures: 1, BaselineHost: "old.example", BaselineDestination: "https://old.example"}

	tests := []struct {
		name        string
		prev        State
		obs         Observation
		wantTrust   domain.TrustState
		wantVerdict string
		wantQuar    bool
		wantFails   int
	}{
		{"first check sets the baseline", State{}, Observation{Destination: dest, Fetch: good}, domain.TrustOK, VerdictOK, false, 0},
		{"same host is ok", base, Observation{Destination: dest, Fetch: goodWWW}, domain.TrustOK, VerdictOK, false, 0},
		{"one failure is not degraded yet", base, Observation{Destination: dest, Fetch: down}, domain.TrustOK, VerdictFailed, false, 1},
		{"second failure in a row degrades", failedOnce, Observation{Destination: dest, Fetch: notFound}, domain.TrustDegraded, VerdictDegraded, false, 2},
		{"a good check heals degraded", degraded, Observation{Destination: dest, Fetch: good}, domain.TrustOK, VerdictOK, false, 0},
		{"ending somewhere else is changed", base, Observation{Destination: dest, Fetch: moved}, domain.TrustChanged, VerdictChanged, false, 0},
		{"threat feed quarantines", base, Observation{Destination: dest, Fetch: good, Threat: true, ThreatWhy: []string{"safe_browsing: SOCIAL_ENGINEERING"}}, domain.TrustQuarantined, VerdictQuarantined, true, 0},
		{"blocked hop quarantines", base, Observation{Destination: dest, Fetch: moved, BlockedBy: "evil.example"}, domain.TrustQuarantined, VerdictQuarantined, true, 0},
		{"threat beats a failure", base, Observation{Destination: dest, Fetch: down, Threat: true}, domain.TrustQuarantined, VerdictQuarantined, true, 0},
		{"quarantine is sticky", quarantined, Observation{Destination: dest, Fetch: good}, domain.TrustQuarantined, VerdictQuarantined, false, 0},
		{"already quarantined is not quarantined again", quarantined, Observation{Destination: dest, Fetch: good, Threat: true}, domain.TrustQuarantined, VerdictQuarantined, false, 0},
		{"editing the destination starts a new baseline", edited, Observation{Destination: dest, Fetch: good}, domain.TrustOK, VerdictOK, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.prev, tt.obs)
			if got.Next.Trust != tt.wantTrust || got.Verdict != tt.wantVerdict || got.Quarantined != tt.wantQuar || got.Next.Failures != tt.wantFails {
				t.Fatalf("got %s/%s quarantined=%v failures=%d, want %s/%s %v %d",
					got.Next.Trust, got.Verdict, got.Quarantined, got.Next.Failures, tt.wantTrust, tt.wantVerdict, tt.wantQuar, tt.wantFails)
			}
			if got.Next.BaselineDestination != tt.obs.Destination {
				t.Errorf("baseline destination %q", got.Next.BaselineDestination)
			}
		})
	}
}
