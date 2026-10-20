package trust

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"pathmaxing/internal/urlcheck"
)

// testChecker allows exactly one extra address, 127.0.0.1 (the test
// server), and applies the production rule to everything else.
func testChecker() *Checker {
	return newChecker("PathMaxingTrustBot/test", func(ip netip.Addr) bool {
		return ip == netip.MustParseAddr("127.0.0.1") || urlcheck.IsPublicIP(ip)
	})
}

func TestCheckerFollowsRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/middle", http.StatusFound) })
	mux.HandleFunc("/middle", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/end", http.StatusMovedPermanently) })
	mux.HandleFunc("/end", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "PathMaxingTrustBot") {
			t.Errorf("user agent %q", r.UserAgent())
		}
		io.WriteString(w, "ok")
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := testChecker().Check(context.Background(), srv.URL+"/start")
	if res.Status != 200 || res.Hops != 2 || !strings.HasSuffix(res.FinalURL, "/end") || res.FinalHost != "127.0.0.1" || !res.Reachable() {
		t.Fatalf("got %+v", res)
	}
	if res := testChecker().Check(context.Background(), srv.URL+"/gone"); res.Status != 404 || res.Reachable() {
		t.Fatalf("404: %+v", res)
	}
}

func TestCheckerRefusesPrivateAddresses(t *testing.T) {
	// The production checker refuses even our own loopback test server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secret") }))
	defer srv.Close()
	for _, target := range []string{
		srv.URL,
		"http://127.0.0.1:9/",
		"http://10.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://localhost:9/",
	} {
		res := NewChecker("t").Check(context.Background(), target)
		if !strings.Contains(res.Error, ErrBlockedAddress.Error()) || res.Status != 0 {
			t.Errorf("%s: %+v, want a refused connection", target, res)
		}
	}
}

// A public page that redirects into a private range must be stopped at
// the hop, not followed.
func TestCheckerRefusesRedirectIntoPrivateRange(t *testing.T) {
	for _, target := range []string{"http://10.0.0.1/admin", "http://169.254.169.254/latest/meta-data/", "http://192.168.1.1/"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		}))
		res := testChecker().Check(context.Background(), srv.URL)
		srv.Close()
		if !strings.Contains(res.Error, ErrBlockedAddress.Error()) || res.Hops != 1 {
			t.Errorf("redirect to %s: %+v", target, res)
		}
	}
}

func TestCheckerLimits(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/ftp", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://files.example/x", http.StatusFound)
	})
	mux.HandleFunc("/huge", func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("x", 64<<10)
		for range 1000 { // 64 MB if the checker kept reading
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := testChecker()

	if res := c.Check(context.Background(), srv.URL+"/loop"); !strings.Contains(res.Error, "more than 5 redirects") {
		t.Errorf("loop: %+v", res)
	}
	if res := c.Check(context.Background(), srv.URL+"/ftp"); !strings.Contains(res.Error, "ftp") {
		t.Errorf("ftp redirect: %+v", res)
	}
	if res := c.Check(context.Background(), srv.URL+"/huge"); res.Status != 200 {
		t.Errorf("huge body: %+v", res)
	}
}
