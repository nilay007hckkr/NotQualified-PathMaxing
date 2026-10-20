package classify

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"pathmaxing/internal/domain"
)

const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"

// fakeDNS answers PTR and A lookups from maps.
type fakeDNS struct {
	ptr   map[string][]string
	hosts map[string][]string
	calls int
}

func (f *fakeDNS) LookupAddr(_ context.Context, addr string) ([]string, error) {
	f.calls++
	if n, ok := f.ptr[addr]; ok {
		return n, nil
	}
	return nil, errors.New("no PTR")
}

func (f *fakeDNS) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := f.hosts[host]; ok {
		return a, nil
	}
	return nil, errors.New("no A")
}

func newTestHonest() (*Honest, *fakeDNS) {
	dns := &fakeDNS{
		ptr: map[string][]string{
			"66.249.66.1": {"crawl-66-249-66-1.googlebot.com."},
			"203.0.113.9": {"crawl-66-249-66-1.googlebot.com."}, // forged PTR: the name doesn't point back here
		},
		hosts: map[string][]string{"crawl-66-249-66-1.googlebot.com": {"66.249.66.1"}},
	}
	h := NewHonest()
	h.Resolver = dns
	return h, dns
}

func TestHonestSignals(t *testing.T) {
	h, dns := newTestHonest()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	googlebot := "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	tests := []struct {
		name   string
		req    domain.RequestInfo
		class  domain.TrafficClass
		reason string
	}{
		{"person", domain.RequestInfo{Method: "GET", UserAgent: chromeUA, IP: netip.MustParseAddr("49.36.1.1"), At: now}, domain.ClassHuman, ReasonBrowser},
		{"HEAD with a browser UA is a probable mail scanner", domain.RequestInfo{Method: "HEAD", UserAgent: chromeUA, At: now}, domain.ClassEmailScanner, ReasonHeadBrowser},
		{"HEAD from a tool", domain.RequestInfo{Method: "HEAD", UserAgent: "curl/8.4.0", At: now}, domain.ClassOtherBot, ReasonHead},
		{"prefetch", domain.RequestInfo{Method: "GET", UserAgent: chromeUA, Purpose: "prefetch", At: now}, domain.ClassOtherBot, ReasonPrefetch},
		{"WhatsApp preview", domain.RequestInfo{Method: "GET", UserAgent: "WhatsApp/2.24.21.80 A", At: now}, domain.ClassLinkPreview, ReasonLinkPreview},
		{"real Googlebot", domain.RequestInfo{Method: "GET", UserAgent: googlebot, IP: netip.MustParseAddr("66.249.66.1"), At: now}, domain.ClassCrawler, ReasonCrawlerVerified},
		{"fake Googlebot (PTR doesn't resolve back)", domain.RequestInfo{Method: "GET", UserAgent: googlebot, IP: netip.MustParseAddr("203.0.113.9"), At: now}, domain.ClassOtherBot, ReasonCrawlerImpersonate},
		{"Googlebot UA, no PTR at all", domain.RequestInfo{Method: "GET", UserAgent: googlebot, IP: netip.MustParseAddr("198.51.100.7"), At: now}, domain.ClassCrawler, ReasonKnownCrawler},
		{"crawler from the community list", domain.RequestInfo{Method: "GET", UserAgent: "Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)", At: now}, domain.ClassCrawler, ReasonKnownCrawler},
		{"script", domain.RequestInfo{Method: "GET", UserAgent: "python-requests/2.31.0", At: now}, domain.ClassOtherBot, ReasonHTTPLibrary},
		{"empty", domain.RequestInfo{Method: "GET", At: now}, domain.ClassOtherBot, ReasonEmptyUA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.Classify(tt.req)
			if got.Class != tt.class || got.Reason != tt.reason {
				t.Fatalf("got %s/%s, want %s/%s", got.Class, got.Reason, tt.class, tt.reason)
			}
		})
	}
	// Verification results are cached, so a busy crawler costs one lookup.
	before := dns.calls
	h.Classify(domain.RequestInfo{Method: "GET", UserAgent: googlebot, IP: netip.MustParseAddr("66.249.66.1"), At: now})
	if dns.calls != before {
		t.Fatal("verified crawler looked up again")
	}
}

func TestBurstFromOneSubnet(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	visit := func(h *Honest, ip string, link int64, after time.Duration) domain.TrafficClass {
		return h.Classify(domain.RequestInfo{Method: "GET", UserAgent: chromeUA, IP: netip.MustParseAddr(ip), At: t0.Add(after), LinkID: link}).Class
	}

	// A mail scanner opens the three links of one message within 200 ms from
	// neighbouring addresses: the third completes the burst.
	h, _ := newTestHonest()
	got := []domain.TrafficClass{
		visit(h, "52.10.4.11", 1, 0),
		visit(h, "52.10.4.12", 2, 100*time.Millisecond),
		visit(h, "52.10.4.13", 3, 200*time.Millisecond),
	}
	if fmt.Sprint(got) != "[human human email_scanner]" {
		t.Fatalf("scanner: %v", got)
	}
	if c := visit(h, "49.36.9.9", 4, 300*time.Millisecond); c != domain.ClassHuman {
		t.Fatalf("someone on another network: %s", c)
	}
	if c := visit(h, "52.10.4.15", 5, time.Minute); c != domain.ClassHuman {
		t.Fatalf("the same network a minute later: %s", c)
	}

	// A room on one Wi-Fi scanning the same poster: many visits, one link.
	h, _ = newTestHonest()
	for i := range 20 {
		if c := visit(h, "49.36.20.7", 9, time.Duration(i)*50*time.Millisecond); c != domain.ClassHuman {
			t.Fatalf("crowd visit %d labelled %s", i+1, c)
		}
	}

	// Private addresses (a proxy that hides the real client) never count.
	h, _ = newTestHonest()
	for i, link := range []int64{1, 2, 3, 4} {
		if c := visit(h, "10.0.0.5", link, time.Duration(i)*10*time.Millisecond); c != domain.ClassHuman {
			t.Fatalf("private address labelled %s", c)
		}
	}
}

// TestAccuracyOnLabelledUserAgents measures the user-agent rules on the
// labelled list in testdata (GET requests, no burst, no DNS). The numbers
// it logs are the ones quoted in the part 3 owner guide.
func TestAccuracyOnLabelledUserAgents(t *testing.T) {
	f, err := os.Open("testdata/user_agents.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := NewHonest()
	h.Resolver = nil // no DNS in tests

	type row struct{ label, ua string }
	var rows []row
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		label, ua, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("bad line %q", line)
		}
		rows = append(rows, row{label, ua})
	}

	var humans, bots, tp, fp, fn, tn, classRight int
	for _, r := range rows {
		got := h.Classify(domain.RequestInfo{Method: "GET", UserAgent: r.ua, At: time.Now()})
		trueBot := r.label != "human"
		predBot := !got.Class.IsHuman()
		switch {
		case trueBot && predBot:
			tp++
		case !trueBot && predBot:
			fp++
			t.Logf("human labelled %s: %s", got.Class, r.ua)
		case trueBot && !predBot:
			fn++
			t.Logf("bot missed (%s): %s", r.label, r.ua)
		default:
			tn++
		}
		if trueBot {
			bots++
			if string(got.Class) == r.label {
				classRight++
			} else {
				t.Logf("bot class %s, labelled %s: %s", got.Class, r.label, r.ua)
			}
		} else {
			humans++
		}
	}
	precision := float64(tp) / float64(tp+fp)
	recall := float64(tp) / float64(tp+fn)
	humanPrecision := float64(tn) / float64(tn+fn)
	humanRecall := float64(tn) / float64(tn+fp)
	t.Logf("labelled list: %d human, %d bot user agents", humans, bots)
	t.Logf("bot detection: precision %.3f, recall %.3f (tp %d, fp %d, fn %d)", precision, recall, tp, fp, fn)
	t.Logf("humans: precision %.3f, recall %.3f", humanPrecision, humanRecall)
	t.Logf("exact bot class: %d of %d (%.3f)", classRight, bots, float64(classRight)/float64(bots))

	if humans < 50 || bots < 50 {
		t.Fatalf("the list needs at least 50 of each, has %d human and %d bot", humans, bots)
	}
	if precision < 0.95 || recall < 0.95 {
		t.Fatalf("bot detection fell below 0.95: precision %.3f recall %.3f", precision, recall)
	}
}
