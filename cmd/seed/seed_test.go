package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestPlanMix(t *testing.T) {
	links := []link{{Code: "a", ShortURL: "http://s/a"}, {Code: "b", ShortURL: "http://s/b"}}
	visits := plan(newRand(42), links, 2000)
	if len(visits) != 2000 {
		t.Fatalf("planned %d visits", len(visits))
	}
	byProfile := map[string]int{}
	qr, heads := 0, 0
	for _, v := range visits {
		byProfile[v.Profile]++
		if strings.HasSuffix(v.URL, "?s=qr") {
			qr++
			if !strings.HasPrefix(v.Profile, "human") {
				t.Fatalf("a %s visit came by QR", v.Profile)
			}
		}
		if v.Method == "HEAD" {
			heads++
		}
		if v.Agent == "" || v.IP == "" {
			t.Fatalf("incomplete visit %+v", v)
		}
	}
	// Every profile appears, humans are the majority, some visits are QR scans.
	if len(byProfile) != len(profiles()) {
		t.Fatalf("profiles used: %v", byProfile)
	}
	humans := byProfile["human mobile"] + byProfile["human desktop"] + byProfile["human tablet"]
	if humans < 1100 || humans > 1600 || qr == 0 || heads != byProfile["email scanner"] {
		t.Fatalf("humans %d, qr %d, heads %d, mix %v", humans, qr, heads, byProfile)
	}
	// The same seed plans the same traffic.
	again := plan(newRand(42), links, 2000)
	if again[17].URL != visits[17].URL || again[17].Agent != visits[17].Agent || again[17].IP != visits[17].IP {
		t.Fatal("plan is not repeatable for a fixed seed")
	}
}

// fakeServer implements just enough of the API to check the seed's flow:
// login falls back to signup, a re-run reuses existing campaigns and links,
// state-changing calls carry our Origin, and redirects are not followed.
type fakeServer struct {
	mu        sync.Mutex
	users     map[string]string
	campaigns []campaign
	links     []link
	visits    int
	badOrigin int
}

func newFake() (*fakeServer, *httptest.Server) {
	f := &fakeServer{users: map[string]string{}}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			if r.Method != "GET" && r.Header.Get("Origin") != srv.URL {
				f.badOrigin++
			}
			f.mu.Unlock()
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/auth/login", guard(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		if pw, ok := f.users[in["email"]]; !ok || pw != in["password"] {
			writeJSON(w, 401, map[string]any{"error": map[string]string{"code": "BAD_CREDENTIALS"}})
			return
		}
		writeJSON(w, 200, map[string]any{"user": map[string]any{"id": 1}})
	}))
	mux.HandleFunc("POST /api/auth/signup", guard(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.users[in["email"]] = in["password"]
		f.mu.Unlock()
		writeJSON(w, 201, map[string]any{"user": map[string]any{"id": 1}})
	}))
	mux.HandleFunc("POST /api/campaigns", guard(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, c := range f.campaigns {
			if c.Name == in["name"] {
				writeJSON(w, 409, map[string]any{"error": map[string]string{"code": "NAME_TAKEN"}})
				return
			}
		}
		c := campaign{ID: int64(len(f.campaigns) + 1), Name: in["name"].(string)}
		f.campaigns = append(f.campaigns, c)
		writeJSON(w, 201, map[string]any{"campaign": c})
	}))
	mux.HandleFunc("GET /api/campaigns", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"items": f.campaigns})
	})
	mux.HandleFunc("POST /api/links", guard(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, l := range f.links {
			if l.Code == in["alias"] {
				writeJSON(w, 409, map[string]any{"error": map[string]string{"code": "ALIAS_TAKEN"}})
				return
			}
		}
		l := link{ID: int64(len(f.links) + 1), Code: in["alias"].(string), ShortURL: srv.URL + "/" + in["alias"].(string)}
		f.links = append(f.links, l)
		writeJSON(w, 201, map[string]any{"link": l})
	}))
	mux.HandleFunc("GET /api/links", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"items": f.links})
	})
	mux.HandleFunc("GET /{code}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.visits++
		f.mu.Unlock()
		http.Redirect(w, r, srv.URL+"/destination-must-not-be-fetched", http.StatusFound)
	})
	mux.HandleFunc("GET /destination-must-not-be-fetched", func(w http.ResponseWriter, r *http.Request) {
		panic("the seed followed a redirect")
	})
	return f, srv
}

func TestRunTwice(t *testing.T) {
	f, srv := newFake()
	defer srv.Close()
	cfg := config{Base: srv.URL, Email: "demo@pathmaxing.example", Password: "demo-pass-1", Name: "Demo", Visits: 60, Workers: 4, Seed: 7}
	for i := range 2 {
		sum, err := run(context.Background(), cfg, io.Discard)
		if err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
		if !strings.Contains(sum, "sent 60 visits") {
			t.Fatalf("summary %q", sum)
		}
	}
	if len(f.campaigns) != len(demoCampaigns) || len(f.links) != len(demoLinks) {
		t.Fatalf("second run duplicated data: %d campaigns, %d links", len(f.campaigns), len(f.links))
	}
	if f.visits != 120 || f.badOrigin != 0 {
		t.Fatalf("visits %d (want 120), requests without our Origin %d", f.visits, f.badOrigin)
	}
}
