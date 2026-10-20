package classify

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	agents "github.com/monperrus/crawler-user-agents"

	"pathmaxing/internal/domain"
)

// More reason codes for the honest classifier.
const (
	ReasonKnownCrawler       = "ua_known_crawler"
	ReasonCrawlerVerified    = "crawler_verified_dns"
	ReasonCrawlerImpersonate = "crawler_failed_dns_check"
	ReasonHeadBrowser        = "head_with_browser_ua"
	ReasonBurst              = "burst_from_subnet"
)

// Resolver is the part of net.Resolver crawler verification needs; tests
// pass a fake.
type Resolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// Honest labels every visit as human, link_preview, email_scanner,
// crawler or other_bot, with the reason. Signals, strongest first:
//
//   - a HEAD request: browsers never send one when a person clicks. With a
//     browser user agent it is most likely a mail security scanner checking
//     the link before delivery, so it is labelled email_scanner;
//   - a prefetch, which nobody clicked;
//   - user agents of chat apps that fetch preview cards, and of HTTP
//     libraries and scripts;
//   - the community-maintained crawler list (monperrus/crawler-user-agents).
//     Search engines that publish a DNS check (Google, Bing, Apple, Yandex,
//     Baidu) are verified with a reverse and forward lookup, so anyone can
//     claim to be Googlebot but only Google passes;
//   - bursts: one /24 (or /48) opening three or more different links within
//     two seconds looks like a scanner checking every link in an email. Many
//     visits to one link (a crowd scanning a poster's QR code) don't count.
//
// Email scanners publish no signature, so email_scanner always means
// "probable". The labelled list in testdata measures precision and recall.
type Honest struct {
	Resolver Resolver
	// DNSTimeout bounds crawler verification on the redirect path.
	DNSTimeout time.Duration
	bursts     *burstDetector
	verified   sync.Map // ip string -> verifyResult
}

// NewHonest returns the classifier the server uses.
func NewHonest() *Honest {
	return &Honest{Resolver: net.DefaultResolver, DNSTimeout: 400 * time.Millisecond, bursts: newBurstDetector(2*time.Second, 3)}
}

// Classify implements domain.TrafficClassifier.
func (h *Honest) Classify(r domain.RequestInfo) domain.Classification {
	ua := strings.ToLower(strings.TrimSpace(r.UserAgent))
	browser := strings.HasPrefix(ua, "mozilla/") && !containsAny(ua, botKeywords)
	switch {
	case r.Method == http.MethodHead && browser:
		return domain.Classification{Class: domain.ClassEmailScanner, Reason: ReasonHeadBrowser}
	case r.Method == http.MethodHead:
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonHead}
	case strings.EqualFold(r.Purpose, "prefetch"):
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonPrefetch}
	case ua == "":
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonEmptyUA}
	case containsAny(ua, linkPreviewAgents):
		return domain.Classification{Class: domain.ClassLinkPreview, Reason: ReasonLinkPreview}
	case containsAny(ua, httpLibraries):
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonHTTPLibrary}
	}

	if domains := verifiableCrawler(ua); domains != nil {
		return h.verifyCrawler(r.IP, domains)
	}
	if agents.IsCrawler(r.UserAgent) || containsAny(ua, botKeywords) {
		return domain.Classification{Class: domain.ClassCrawler, Reason: ReasonKnownCrawler}
	}
	if !strings.HasPrefix(ua, "mozilla/") {
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonNonBrowserUA}
	}
	if h.bursts != nil && r.IP.IsValid() && h.bursts.record(r.IP, r.LinkID, r.At) {
		return domain.Classification{Class: domain.ClassEmailScanner, Reason: ReasonBurst}
	}
	return domain.Classification{Class: domain.ClassHuman, Reason: ReasonBrowser}
}

// verifiableCrawler returns the host suffixes a crawler's reverse DNS must
// end with, for crawlers whose operators publish that check.
func verifiableCrawler(ua string) []string {
	switch {
	case strings.Contains(ua, "googlebot"), strings.Contains(ua, "google-inspectiontool"):
		return []string{".googlebot.com", ".google.com", ".googleusercontent.com"}
	case strings.Contains(ua, "bingbot"):
		return []string{".search.msn.com"}
	case strings.Contains(ua, "applebot"):
		return []string{".applebot.apple.com"}
	case strings.Contains(ua, "yandexbot"):
		return []string{".yandex.ru", ".yandex.net", ".yandex.com"}
	case strings.Contains(ua, "baiduspider"):
		return []string{".baidu.com", ".baidu.jp"}
	}
	return nil
}

type verifyResult struct {
	ok      bool
	checked time.Time
}

// verifyCrawler does the published check: the IP's PTR name must end in the
// operator's domain, and that name must resolve back to the same IP. Results
// are cached for a day, since crawlers come from a small set of addresses.
// When DNS can't answer in time, the visit is still a crawler (it said so),
// just an unverified one.
func (h *Honest) verifyCrawler(ip netip.Addr, suffixes []string) domain.Classification {
	crawler := domain.Classification{Class: domain.ClassCrawler, Reason: ReasonKnownCrawler}
	if !ip.IsValid() || h.Resolver == nil {
		return crawler
	}
	key := ip.String()
	if v, ok := h.verified.Load(key); ok && time.Since(v.(verifyResult).checked) < 24*time.Hour {
		return verdict(v.(verifyResult).ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.DNSTimeout)
	defer cancel()
	names, err := h.Resolver.LookupAddr(ctx, key)
	if err != nil {
		return crawler
	}
	ok := false
	for _, name := range names {
		name = strings.TrimSuffix(strings.ToLower(name), ".")
		if !hasAnySuffix(name, suffixes) {
			continue
		}
		addrs, err := h.Resolver.LookupHost(ctx, name)
		if err != nil {
			return crawler
		}
		for _, a := range addrs {
			if back, err := netip.ParseAddr(a); err == nil && back.Unmap() == ip.Unmap() {
				ok = true
			}
		}
	}
	h.verified.Store(key, verifyResult{ok: ok, checked: time.Now()})
	return verdict(ok)
}

func verdict(ok bool) domain.Classification {
	if ok {
		return domain.Classification{Class: domain.ClassCrawler, Reason: ReasonCrawlerVerified}
	}
	// It claims to be a search engine and isn't: a scraper in disguise.
	return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonCrawlerImpersonate}
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}
