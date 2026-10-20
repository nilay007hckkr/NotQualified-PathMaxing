package threatintel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSafeBrowsing answers like the v4 API: a match for URLs containing
// "phish", nothing otherwise, and checks the key and request shape.
func fakeSafeBrowsing(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/threatMatches:find" || r.URL.Query().Get("key") != "sb-key" {
			http.Error(w, "bad key", http.StatusForbidden)
			return
		}
		var body struct {
			ThreatInfo struct {
				ThreatEntries []struct{ URL string } `json:"threatEntries"`
			} `json:"threatInfo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.ThreatInfo.ThreatEntries) != 1 {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if strings.Contains(body.ThreatInfo.ThreatEntries[0].URL, "phish") {
			io.WriteString(w, `{"matches":[{"threatType":"SOCIAL_ENGINEERING","platformType":"ANY_PLATFORM"}]}`)
			return
		}
		io.WriteString(w, `{}`)
	}))
}

func fakeURLhaus(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/url/" || r.Header.Get("Auth-Key") != "uh-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.ParseForm()
		switch u := r.PostForm.Get("url"); {
		case strings.Contains(u, "malware"):
			io.WriteString(w, `{"query_status":"ok","url_status":"online","threat":"malware_download"}`)
		case strings.Contains(u, "broken"):
			io.WriteString(w, `not json`)
		default:
			io.WriteString(w, `{"query_status":"no_results"}`)
		}
	}))
}

func TestSafeBrowsing(t *testing.T) {
	srv := fakeSafeBrowsing(t)
	defer srv.Close()
	sb := SafeBrowsing{Key: "sb-key", BaseURL: srv.URL}
	res, err := sb.Check(context.Background(), "https://phish.example/login")
	if err != nil || !res.Malicious || res.Detail != "SOCIAL_ENGINEERING" || res.Source != "safe_browsing" {
		t.Fatalf("phish: %+v, %v", res, err)
	}
	res, err = sb.Check(context.Background(), "https://shop.example/")
	if err != nil || res.Malicious {
		t.Fatalf("clean: %+v, %v", res, err)
	}
	if _, err := (SafeBrowsing{Key: "wrong", BaseURL: srv.URL}).Check(context.Background(), "https://a.example"); err == nil {
		t.Fatal("a rejected key was not an error")
	}
}

func TestURLhaus(t *testing.T) {
	srv := fakeURLhaus(t)
	defer srv.Close()
	uh := URLhaus{Key: "uh-key", BaseURL: srv.URL}
	res, err := uh.Check(context.Background(), "http://malware.example/x.exe")
	if err != nil || !res.Malicious || res.Detail != "malware_download (online)" {
		t.Fatalf("malware: %+v, %v", res, err)
	}
	if res, err := uh.Check(context.Background(), "https://shop.example/"); err != nil || res.Malicious {
		t.Fatalf("clean: %+v, %v", res, err)
	}
	if _, err := uh.Check(context.Background(), "https://broken.example/"); err == nil {
		t.Fatal("an unreadable answer was not an error")
	}
	if _, err := (URLhaus{Key: "nope", BaseURL: srv.URL}).Check(context.Background(), "https://a.example"); err == nil {
		t.Fatal("a rejected key was not an error")
	}
}

type slowFeed struct{}

func (slowFeed) Name() string { return "slow" }
func (slowFeed) Check(ctx context.Context, _ string) (Result, error) {
	<-ctx.Done()
	return Result{}, ctx.Err()
}

func TestSetCombinesFeeds(t *testing.T) {
	a := NewFake("safe_browsing", "phish")
	b := NewFake("urlhaus", "malware")
	set := NewSet([]Feed{a, b, slowFeed{}}, []string{"geo: not configured"}, 200*time.Millisecond)

	rep := set.Check(context.Background(), "https://phish.example/malware.exe")
	if !rep.Malicious || len(rep.Matches) != 2 || rep.Matches[0].Source != "safe_browsing" {
		t.Fatalf("both feeds should match: %+v", rep)
	}
	if strings.Join(rep.Checked, ",") != "safe_browsing,urlhaus" {
		t.Fatalf("checked = %v", rep.Checked)
	}
	// The unconfigured feed and the one that timed out are both reported.
	if len(rep.Skipped) != 2 || !strings.Contains(strings.Join(rep.Skipped, ";"), "slow") {
		t.Fatalf("skipped = %v", rep.Skipped)
	}

	b.Fail(errors.New("503"))
	rep = set.Check(context.Background(), "https://shop.example/")
	if rep.Malicious || !strings.Contains(strings.Join(rep.Skipped, ";"), "urlhaus: 503") {
		t.Fatalf("a failing feed must show as skipped: %+v", rep)
	}
}
