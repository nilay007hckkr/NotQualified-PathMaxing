package classify

import (
	"net/netip"
	"sync"
	"time"

	"pathmaxing/internal/urlcheck"
)

// burstDetector notices one network opening several different links within
// a moment. A mail security product does exactly that when it checks every
// link in a message before delivery. Counting distinct links, not visits,
// matters: a room full of people on one Wi-Fi scanning the same QR code is
// many visits from one network, but to a single link, and they are people.
type burstDetector struct {
	window    time.Duration
	threshold int // distinct links within the window that make a burst

	mu        sync.Mutex
	seen      map[netip.Prefix][]hit
	lastSweep time.Time
}

type hit struct {
	at     time.Time
	linkID int64
}

func newBurstDetector(window time.Duration, threshold int) *burstDetector {
	return &burstDetector{window: window, threshold: threshold, seen: make(map[netip.Prefix][]hit)}
}

// subnet groups IPv4 by /24 and IPv6 by /48: scanners spread requests over
// neighbouring addresses.
func subnet(ip netip.Addr) netip.Prefix {
	ip = ip.Unmap()
	bits := 24
	if ip.Is6() {
		bits = 48
	}
	p, _ := ip.Prefix(bits)
	return p
}

// record notes a visit to linkID at t and reports whether it completes a
// burst. Private and other non-public addresses are never counted: behind a
// misconfigured proxy every visitor would share one, and a burst from
// "everyone" means nothing.
func (b *burstDetector) record(ip netip.Addr, linkID int64, t time.Time) bool {
	if !urlcheck.IsPublicIP(ip) {
		return false
	}
	key := subnet(ip)
	b.mu.Lock()
	defer b.mu.Unlock()
	if t.Sub(b.lastSweep) > time.Minute {
		b.lastSweep = t
		for k, hits := range b.seen {
			if len(hits) == 0 || t.Sub(hits[len(hits)-1].at) > b.window {
				delete(b.seen, k)
			}
		}
	}
	hits := b.seen[key]
	kept := hits[:0]
	for _, h := range hits {
		if t.Sub(h.at) <= b.window {
			kept = append(kept, h)
		}
	}
	kept = append(kept, hit{at: t, linkID: linkID})
	if len(kept) > 64 {
		kept = kept[len(kept)-64:]
	}
	b.seen[key] = kept
	links := make(map[int64]bool, len(kept))
	for _, h := range kept {
		links[h.linkID] = true
	}
	return len(links) >= b.threshold
}
