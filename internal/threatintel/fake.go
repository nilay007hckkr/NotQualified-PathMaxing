package threatintel

import (
	"context"
	"strings"
	"sync"
)

// Fake is an in-memory feed for tests and local demos: it flags any URL
// containing one of its markers. Tests use it instead of the real feeds,
// which they must never call.
type Fake struct {
	Source string

	mu      sync.Mutex
	markers []string
	err     error
}

// NewFake returns a fake feed named source.
func NewFake(source string, markers ...string) *Fake {
	return &Fake{Source: source, markers: markers}
}

// Flag adds a marker.
func (f *Fake) Flag(marker string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markers = append(f.markers, marker)
}

// Fail makes every check return err (nil to recover).
func (f *Fake) Fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Name implements Feed.
func (f *Fake) Name() string { return f.Source }

// Check implements Feed.
func (f *Fake) Check(_ context.Context, rawURL string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Result{}, f.err
	}
	for _, m := range f.markers {
		if strings.Contains(rawURL, m) {
			return Result{Source: f.Source, Malicious: true, Detail: "fake match on " + m}, nil
		}
	}
	return Result{Source: f.Source}, nil
}
