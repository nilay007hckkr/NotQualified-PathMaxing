package clicks

import (
	"sync"
	"time"

	"pathmaxing/internal/domain"
)

// LiveClick is what the live feed sends for each visit.
type LiveClick struct {
	LinkID       int64     `json:"link_id"`
	TrafficClass string    `json:"traffic_class"`
	TS           time.Time `json:"ts"`
	WorkspaceID  int64     `json:"-"`
}

// Broker fans committed click events out to live subscribers in this
// process. The writer publishes after each batch commits, so the feed never
// shows a click the dashboard's totals don't have yet.
type Broker struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	ch    chan LiveClick
	match func(LiveClick) bool
}

// subscriberBuffer is how many clicks a slow viewer may fall behind before
// clicks are skipped for them; a live counter can lose a few, a writer can't
// wait for a browser.
const subscriberBuffer = 256

// NewBroker returns an empty broker.
func NewBroker() *Broker { return &Broker{subs: map[*subscriber]struct{}{}} }

// Subscribe returns a channel of the clicks match accepts, and a function
// that ends the subscription.
func (b *Broker) Subscribe(match func(LiveClick) bool) (<-chan LiveClick, func()) {
	s := &subscriber{ch: make(chan LiveClick, subscriberBuffer), match: match}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() {
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
	}
}

// Publish hands events to every matching subscriber without blocking.
func (b *Broker) Publish(events []domain.ClickEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) == 0 {
		return
	}
	for _, e := range events {
		c := LiveClick{LinkID: e.LinkID, TrafficClass: string(e.Class), TS: e.At.UTC(), WorkspaceID: e.WorkspaceID}
		for s := range b.subs {
			if !s.match(c) {
				continue
			}
			select {
			case s.ch <- c:
			default:
			}
		}
	}
}
