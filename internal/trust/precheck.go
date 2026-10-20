// Package trust keeps checking a link's destination after it is published:
// threat feeds, the admin blocklist, and whether the page still answers and
// still ends on the same site.
package trust

import (
	"context"

	"pathmaxing/internal/domain"
	"pathmaxing/internal/threatintel"
)

// Prechecker implements domain.TrustChecker: the checks the creation wizard
// runs before a link is published.
type Prechecker struct {
	Feeds *threatintel.Set
	Probe *Checker // nil skips the reachability probe
}

// Precheck asks the threat feeds about rawURL and, in parallel, fetches it
// through the SSRF-safe checker. Feeds that could not answer are listed under
// Skipped, so the wizard never implies a check it didn't do.
func (p *Prechecker) Precheck(ctx context.Context, rawURL string) (domain.Precheck, error) {
	probe := make(chan Result, 1)
	if p.Probe != nil {
		go func() { probe <- p.Probe.Check(ctx, rawURL) }()
	}
	rep := p.Feeds.Check(ctx, rawURL)
	pc := domain.Precheck{ThreatMatch: rep.Malicious, Skipped: rep.Skipped}
	for _, m := range rep.Matches {
		pc.ThreatSources = append(pc.ThreatSources, m.Source)
	}
	if p.Probe == nil {
		pc.Skipped = append(pc.Skipped, "reachability: probe not configured")
		return pc, nil
	}
	res := <-probe
	reachable := res.Reachable()
	pc.Reachable = &reachable
	pc.Hops = res.Hops
	pc.FinalURL = res.FinalURL
	return pc, nil
}
