// Package threatintel asks threat-intelligence feeds whether a URL is
// known to be malicious: Google Safe Browsing (v4 Lookup API) and abuse.ch
// URLhaus. Both need a free key; a feed without a key is skipped, and the
// skip is reported rather than treated as "safe".
package threatintel

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Result is one feed's answer about one URL.
type Result struct {
	Source    string // "safe_browsing" or "urlhaus"
	Malicious bool
	Detail    string // e.g. "SOCIAL_ENGINEERING" or "malware_download (online)"
}

// Feed is one threat-intelligence source.
type Feed interface {
	Name() string
	Check(ctx context.Context, rawURL string) (Result, error)
}

// Report combines every feed's answer.
type Report struct {
	Malicious bool
	Matches   []Result // the feeds that flagged the URL
	Checked   []string // the feeds that answered
	Skipped   []string // feeds that did not run or failed, with the reason
}

// Set asks several feeds at once.
type Set struct {
	feeds   []Feed
	skipped []string // feeds that are not configured, fixed at startup
	timeout time.Duration
}

// NewSet returns a set over feeds. notConfigured names the feeds that were
// left out for lack of a key, so every report can say they were skipped.
func NewSet(feeds []Feed, notConfigured []string, timeout time.Duration) *Set {
	return &Set{feeds: feeds, skipped: notConfigured, timeout: timeout}
}

// Check asks every feed in parallel. A feed that errors or times out is
// listed under Skipped: an unanswered question is not a clean bill of health.
func (s *Set) Check(ctx context.Context, rawURL string) Report {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		rep = Report{Skipped: append([]string(nil), s.skipped...)}
	)
	for _, f := range s.feeds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := f.Check(ctx, rawURL)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", f.Name(), err))
				return
			}
			rep.Checked = append(rep.Checked, f.Name())
			if res.Malicious {
				rep.Malicious = true
				rep.Matches = append(rep.Matches, res)
			}
		}()
	}
	wg.Wait()
	sort.Strings(rep.Checked)
	sort.Slice(rep.Matches, func(i, j int) bool { return rep.Matches[i].Source < rep.Matches[j].Source })
	return rep
}

// Feeds returns the names of the configured feeds.
func (s *Set) Feeds() []string {
	names := make([]string, len(s.feeds))
	for i, f := range s.feeds {
		names[i] = f.Name()
	}
	return names
}

// defaultClient has a short timeout of its own, so a hung feed can't hold
// a link check for long even if the caller forgets a deadline.
var defaultClient = &http.Client{Timeout: 8 * time.Second}
