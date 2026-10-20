package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// profile is one kind of visitor. The user agents are the formats these
// browsers and bots really send; which label the server gives each visit is
// up to its classifier, not to the seed.
type profile struct {
	Name    string
	Weight  int
	Method  string
	Agents  []string
	Headers map[string]string
}

func profiles() []profile {
	return []profile{
		{Name: "human mobile", Weight: 40, Method: "GET", Agents: []string{
			"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.6668.81 Mobile Safari/537.36",
			"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
			"Mozilla/5.0 (Linux; Android 14; SAMSUNG SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36",
			"Mozilla/5.0 (Linux; Android 13; Redmi Note 12) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.6613.127 Mobile Safari/537.36",
		}},
		{Name: "human desktop", Weight: 22, Method: "GET", Agents: []string{
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
			"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0",
		}},
		{Name: "human tablet", Weight: 5, Method: "GET", Agents: []string{
			"Mozilla/5.0 (iPad; CPU OS 16_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1",
			"Mozilla/5.0 (Linux; Android 13; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
		}},
		{Name: "link preview", Weight: 12, Method: "GET", Agents: []string{
			"WhatsApp/2.23.20.0",
			"facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)",
			"Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)",
			"TelegramBot (like TwitterBot)",
			"Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)",
			"LinkedInBot/1.0 (compatible; Mozilla/5.0; Apache-HttpClient +http://www.linkedin.com)",
		}},
		// Mail security products open links before delivery, often with HEAD
		// and a plain browser user agent from a data centre.
		{Name: "email scanner", Weight: 9, Method: "HEAD", Agents: []string{
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			"Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.110 Safari/537.36",
		}},
		{Name: "crawler", Weight: 7, Method: "GET", Agents: []string{
			"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
			"Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)",
		}},
		{Name: "script", Weight: 5, Method: "GET", Agents: []string{"curl/8.4.0", "python-requests/2.31.0", "Go-http-client/1.1"}},
	}
}

var referrers = []string{
	"https://www.instagram.com/", "https://l.instagram.com/", "https://t.co/", "https://www.linkedin.com/",
	"https://www.google.com/", "https://www.youtube.com/", "", "", "",
}

// visit is one planned request.
type visit struct {
	URL      string
	Method   string
	Agent    string
	Profile  string
	Referrer string
	IP       string
	Headers  map[string]string
}

// plan picks n visits over the links. Weights favour the first links, the
// way real campaigns have a few hits and a long tail; a share of human
// visits arrive by QR code.
func plan(r *rand.Rand, links []link, n int) []visit {
	ps := profiles()
	total := 0
	for _, p := range ps {
		total += p.Weight
	}
	out := make([]visit, 0, n)
	for range n {
		pick := r.IntN(total)
		var p profile
		for _, cand := range ps {
			if pick < cand.Weight {
				p = cand
				break
			}
			pick -= cand.Weight
		}
		l := links[min(len(links)-1, int(float64(len(links))*r.Float64()*r.Float64()))]
		u := l.ShortURL
		human := strings.HasPrefix(p.Name, "human")
		if human && r.IntN(5) == 0 {
			u += "?s=qr"
		}
		v := visit{
			URL: u, Method: p.Method, Agent: p.Agents[r.IntN(len(p.Agents))], Profile: p.Name,
			// Simulated visitor addresses; the server only honours them with
			// TRUSTED_PROXY_HOPS=1, and stores only a daily hash either way.
			IP:      fmt.Sprintf("%d.%d.%d.%d", 49+r.IntN(60), r.IntN(256), r.IntN(256), 1+r.IntN(254)),
			Headers: p.Headers,
		}
		if human && !strings.Contains(u, "?s=qr") {
			v.Referrer = referrers[r.IntN(len(referrers))]
		}
		out = append(out, v)
	}
	return out
}

// send performs the visits with a few workers and counts the answers. It
// never follows redirects: the point is to exercise PathMaxing, not to load
// the destination websites.
func send(ctx context.Context, visits []visit, workers int) map[string]int {
	hc := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var mu sync.Mutex
	counts := map[string]int{}
	jobs := make(chan visit)
	var wg sync.WaitGroup
	for range max(1, workers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range jobs {
				key := v.Profile + " " + fetch(ctx, hc, v)
				mu.Lock()
				counts[key]++
				mu.Unlock()
			}
		}()
	}
	for _, v := range visits {
		select {
		case jobs <- v:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return counts
}

func fetch(ctx context.Context, hc *http.Client, v visit) string {
	req, err := http.NewRequestWithContext(ctx, v.Method, v.URL, nil)
	if err != nil {
		return "error"
	}
	req.Header.Set("User-Agent", v.Agent)
	req.Header.Set("X-Forwarded-For", v.IP)
	if v.Referrer != "" {
		req.Header.Set("Referer", v.Referrer)
	}
	for k, val := range v.Headers {
		req.Header.Set(k, val)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "error"
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return fmt.Sprint(resp.StatusCode)
}

func formatCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-22s %d\n", k, counts[k])
	}
	return b.String()
}
