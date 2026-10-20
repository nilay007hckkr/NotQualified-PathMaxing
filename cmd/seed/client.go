package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// client talks to the management API with a session cookie, the same way
// the web app does, including the Origin header the server's CSRF check
// expects on state-changing requests.
type client struct {
	base string
	http *http.Client
}

func newClient(base string) (*client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	base = strings.TrimRight(base, "/")
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("bad -base: %w", err)
	}
	return &client{base: base, http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}, nil
}

// apiError is a non-2xx answer.
type apiError struct {
	Status int
	Code   string
	Body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.Code, strings.TrimSpace(e.Body))
}

func statusOf(err error) int {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

func (c *client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", c.base)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var env struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(data, &env)
		return &apiError{Status: resp.StatusCode, Code: env.Error.Code, Body: string(data)}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// signIn logs in, or creates the account when there is none yet.
func (c *client) signIn(ctx context.Context, email, password, name string) error {
	creds := map[string]string{"email": email, "password": password}
	err := c.do(ctx, http.MethodPost, "/api/auth/login", creds, nil)
	if statusOf(err) == http.StatusUnauthorized {
		creds["name"] = name
		err = c.do(ctx, http.MethodPost, "/api/auth/signup", creds, nil)
		if statusOf(err) == http.StatusConflict {
			return errors.New("an account with this email exists but the password is different")
		}
	}
	return err
}

type campaign struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type link struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	ShortURL string `json:"short_url"`
}

// ensureCampaign creates a campaign or, if one with the name exists, uses it.
func (c *client) ensureCampaign(ctx context.Context, in map[string]string) (campaign, error) {
	var created struct {
		Campaign campaign `json:"campaign"`
	}
	err := c.do(ctx, http.MethodPost, "/api/campaigns", in, &created)
	if statusOf(err) != http.StatusConflict {
		return created.Campaign, err
	}
	var page struct {
		Items []campaign `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/campaigns?limit=100", nil, &page); err != nil {
		return campaign{}, err
	}
	for _, cp := range page.Items {
		if strings.EqualFold(cp.Name, in["name"]) {
			return cp, nil
		}
	}
	return campaign{}, fmt.Errorf("campaign %q exists but is not listed", in["name"])
}

// ensureLink creates a link with a fixed alias or, on a re-run, finds the
// one created last time, so seeding twice adds traffic, not duplicates.
func (c *client) ensureLink(ctx context.Context, in map[string]any) (link, error) {
	var created struct {
		Link link `json:"link"`
	}
	err := c.do(ctx, http.MethodPost, "/api/links", in, &created)
	if statusOf(err) != http.StatusConflict && statusOf(err) != http.StatusUnprocessableEntity {
		return created.Link, err
	}
	alias, _ := in["alias"].(string)
	var page struct {
		Items []link `json:"items"`
	}
	if err2 := c.do(ctx, http.MethodGet, "/api/links?limit=100&q="+url.QueryEscape(alias), nil, &page); err2 != nil {
		return link{}, err2
	}
	for _, l := range page.Items {
		if strings.EqualFold(l.Code, alias) {
			return l, nil
		}
	}
	return link{}, fmt.Errorf("create link %q: %w", alias, err)
}
