package threatintel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SafeBrowsing is the Google Safe Browsing v4 Lookup API. Google allows it
// for non-commercial use only, which fits this prototype; the commercial
// route is Web Risk (on the roadmap).
type SafeBrowsing struct {
	Key     string
	BaseURL string // tests point this at a fake server
	Client  *http.Client
}

// Name implements Feed.
func (SafeBrowsing) Name() string { return "safe_browsing" }

// Check implements Feed.
func (s SafeBrowsing) Check(ctx context.Context, rawURL string) (Result, error) {
	base := s.BaseURL
	if base == "" {
		base = "https://safebrowsing.googleapis.com"
	}
	body, _ := json.Marshal(map[string]any{
		"client": map[string]string{"clientId": "pathmaxing", "clientVersion": "1.0"},
		"threatInfo": map[string]any{
			"threatTypes":      []string{"MALWARE", "SOCIAL_ENGINEERING", "UNWANTED_SOFTWARE", "POTENTIALLY_HARMFUL_APPLICATION"},
			"platformTypes":    []string{"ANY_PLATFORM"},
			"threatEntryTypes": []string{"URL"},
			"threatEntries":    []map[string]string{{"url": rawURL}},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v4/threatMatches:find?key="+url.QueryEscape(s.Key), bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Matches []struct {
			ThreatType string `json:"threatType"`
		} `json:"matches"`
	}
	if err := doJSON(s.Client, req, &out); err != nil {
		return Result{}, err
	}
	res := Result{Source: s.Name()}
	if len(out.Matches) > 0 {
		res.Malicious = true
		var types []string
		for _, m := range out.Matches {
			types = append(types, m.ThreatType)
		}
		res.Detail = strings.Join(types, ",")
	}
	return res, nil
}

// URLhaus is abuse.ch's database of URLs used to spread malware.
type URLhaus struct {
	Key     string
	BaseURL string
	Client  *http.Client
}

// Name implements Feed.
func (URLhaus) Name() string { return "urlhaus" }

// Check implements Feed. A URL that URLhaus knows is reported even when its
// status is "offline": it was used to spread malware and may come back.
func (u URLhaus) Check(ctx context.Context, rawURL string) (Result, error) {
	base := u.BaseURL
	if base == "" {
		base = "https://urlhaus-api.abuse.ch"
	}
	form := url.Values{"url": {rawURL}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/url/", strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Auth-Key", u.Key)
	var out struct {
		QueryStatus string `json:"query_status"`
		URLStatus   string `json:"url_status"`
		Threat      string `json:"threat"`
	}
	if err := doJSON(u.Client, req, &out); err != nil {
		return Result{}, err
	}
	res := Result{Source: u.Name()}
	switch out.QueryStatus {
	case "ok":
		res.Malicious = true
		res.Detail = fmt.Sprintf("%s (%s)", out.Threat, out.URLStatus)
	case "no_results":
	default:
		return Result{}, fmt.Errorf("unexpected query_status %q", out.QueryStatus)
	}
	return res, nil
}

func doJSON(client *http.Client, req *http.Request, out any) error {
	if client == nil {
		client = defaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
