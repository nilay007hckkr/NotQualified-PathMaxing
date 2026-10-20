package trust

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"pathmaxing/internal/urlcheck"
)

// ErrBlockedAddress means the checker refused to connect to a private,
// loopback, link-local or otherwise non-public address.
var ErrBlockedAddress = errors.New("refused to connect to a non-public address")

// Checker fetches a destination the way a visitor's browser would, so the
// trust layer can see where it really ends up. Fetching URLs that users
// typed is a classic server-side request forgery (SSRF) risk: a link to
// http://169.254.169.254/ would read the cloud provider's metadata from
// inside our network. So the checker refuses non-public addresses at the
// moment it connects, after DNS resolution, which also covers every
// redirect hop and a DNS name that switches to a private address between
// checks.
type Checker struct {
	client       *http.Client
	MaxRedirects int
	MaxBody      int64
	UserAgent    string
}

// Result is what one check saw.
type Result struct {
	Status    int // 0 when no response arrived
	FinalURL  string
	FinalHost string
	Hops      int      // redirects followed
	HopHosts  []string // host of every URL visited, first to last
	Error     string   // network or policy error, empty on a response
}

// Reachable reports whether the destination answered without an error
// status.
func (r Result) Reachable() bool { return r.Error == "" && r.Status > 0 && r.Status < 400 }

// NewChecker returns a checker with the production limits: 5 redirects,
// 256 KB of body read, 5 s to connect and 15 s in total.
func NewChecker(userAgent string) *Checker {
	return newChecker(userAgent, urlcheck.IsPublicIP)
}

func newChecker(userAgent string, allow func(netip.Addr) bool) *Checker {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		// Control runs for the exact address being dialled, after DNS.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !allow(ip) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
			}
			return nil
		},
	}
	c := &Checker{MaxRedirects: 5, MaxBody: 256 << 10, UserAgent: userAgent}
	c.client = &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:                  nil, // a proxy would connect for us and skip the address check
			DialContext:            dialer.DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  8 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
			DisableKeepAlives:      true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > c.MaxRedirects {
				return fmt.Errorf("more than %d redirects", c.MaxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to a %s: URL", req.URL.Scheme)
			}
			return nil
		},
	}
	return c
}

// Check fetches rawURL, following up to MaxRedirects redirects.
func (c *Checker) Check(ctx context.Context, rawURL string) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{Error: err.Error()}
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "text/html,*/*;q=0.8")

	hops := 0
	final := req.URL
	hosts := []string{strings.ToLower(req.URL.Hostname())}
	client := *c.client
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		hosts = append(hosts, strings.ToLower(next.URL.Hostname()))
		if err := c.client.CheckRedirect(next, via); err != nil {
			return err
		}
		hops = len(via)
		final = next.URL
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		res := Result{Error: cleanError(err), Hops: hops, HopHosts: hosts}
		res.FinalURL, res.FinalHost = final.String(), strings.ToLower(final.Hostname())
		return res
	}
	defer resp.Body.Close()
	// Read a little so the server sees a normal client, but never a whole
	// large download.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, c.MaxBody))
	return Result{
		Status:    resp.StatusCode,
		FinalURL:  resp.Request.URL.String(),
		FinalHost: strings.ToLower(resp.Request.URL.Hostname()),
		Hops:      hops,
		HopHosts:  hosts,
	}
}

// cleanError drops Go's "Get \"url\":" prefix, which repeats the URL.
func cleanError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}
