package trust

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pathmaxing/internal/threatintel"
)

func TestPrecheck(t *testing.T) {
	sb := threatintel.NewFake("safe_browsing", "phish")
	set := threatintel.NewSet([]threatintel.Feed{sb}, []string{"urlhaus: no URLHAUS_AUTH_KEY"}, time.Second)
	p := &Prechecker{Feeds: set}

	pc, err := p.Precheck(context.Background(), "https://phish.example/login")
	if err != nil || !pc.ThreatMatch || len(pc.ThreatSources) != 1 || pc.ThreatSources[0] != "safe_browsing" {
		t.Fatalf("phish: %+v, %v", pc, err)
	}
	pc, _ = p.Precheck(context.Background(), "https://shop.example/")
	if pc.ThreatMatch || len(pc.Skipped) != 2 {
		t.Fatalf("clean: %+v; want no match and the unconfigured feed listed as skipped", pc)
	}
}

func TestPrecheckProbesReachability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/landing", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := &Prechecker{Feeds: threatintel.NewSet(nil, nil, time.Second), Probe: testChecker()}
	pc, _ := p.Precheck(context.Background(), srv.URL+"/")
	if pc.Reachable == nil || !*pc.Reachable || pc.Hops != 1 || !strings.HasSuffix(pc.FinalURL, "/landing") {
		t.Fatalf("got %+v", pc)
	}
	pc, _ = p.Precheck(context.Background(), "http://10.0.0.1/")
	if pc.Reachable == nil || *pc.Reachable {
		t.Fatalf("a private address must count as unreachable: %+v", pc)
	}
}
