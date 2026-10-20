package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the server configuration, read once from the environment at
// startup. Every variable is listed in .env.example. The fields cover what
// the server itself needs; a part reads its own variables through String,
// Int, Duration and Bool so that adding one never touches this file.
type Config struct {
	Env         string // APP_ENV: development or production
	Addr        string // ADDR, else ":"+PORT (Render sets PORT), else ":8080"
	BaseURL     string // BASE_URL without a trailing slash; short links start with it
	DatabaseURL string // DATABASE_URL; empty means in-memory defaults only
	WebDistDir  string // WEB_DIST_DIR; the built web app served under /app/

	// SecretKey signs unlock cookies and keys the visitor hash. When it is
	// unset outside production, Load makes a random one and sets
	// SecretGenerated, so those reset on every restart.
	SecretKey       string
	SecretGenerated bool

	LogLevel slog.Level
	// AllowedOrigins are the origins that may send state-changing requests:
	// BaseURL's origin first, then ALLOWED_ORIGINS (for example the Vite dev
	// server).
	AllowedOrigins []string
	// TrustedProxyHops is how many reverse proxies sit in front of the server
	// and append to X-Forwarded-For (1 on Render). With 0 the header is
	// ignored, so a visitor can't spoof their address.
	TrustedProxyHops int
	// ShutdownTimeout bounds each shutdown stage: HTTP, workers, Stop hooks.
	ShutdownTimeout time.Duration

	getenv func(string) string
}

const minSecretLen = 32

// Load reads the configuration through getenv (os.Getenv in main, a map in
// tests).
func Load(getenv func(string) string) (Config, error) {
	c := Config{getenv: getenv}
	var err error

	c.Env = c.String("APP_ENV", "development")
	if c.Env != "development" && c.Env != "production" {
		return Config{}, fmt.Errorf("APP_ENV must be development or production, got %q", c.Env)
	}

	port := c.String("PORT", "8080")
	c.Addr = c.String("ADDR", ":"+port)

	c.BaseURL, err = parseOrigin("BASE_URL", c.String("BASE_URL", "http://localhost:"+port))
	if err != nil {
		return Config{}, err
	}
	if c.Production() && !strings.HasPrefix(c.BaseURL, "https://") {
		return Config{}, fmt.Errorf("BASE_URL must use https in production, because session cookies are Secure there")
	}

	c.DatabaseURL = c.String("DATABASE_URL", "")
	c.WebDistDir = c.String("WEB_DIST_DIR", "")

	c.SecretKey = c.String("SECRET_KEY", "")
	switch {
	case c.SecretKey == "" && c.Production():
		return Config{}, fmt.Errorf("SECRET_KEY is required in production (at least %d characters)", minSecretLen)
	case c.SecretKey == "":
		c.SecretKey = randomHex(32)
		c.SecretGenerated = true
	case len(c.SecretKey) < minSecretLen:
		return Config{}, fmt.Errorf("SECRET_KEY must be at least %d characters", minSecretLen)
	}

	if err := c.LogLevel.UnmarshalText([]byte(c.String("LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("LOG_LEVEL: %w", err)
	}

	c.AllowedOrigins = []string{c.BaseURL}
	for _, o := range strings.Split(c.String("ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o == "" {
			continue
		}
		origin, err := parseOrigin("ALLOWED_ORIGINS", o)
		if err != nil {
			return Config{}, err
		}
		c.AllowedOrigins = append(c.AllowedOrigins, origin)
	}

	if c.TrustedProxyHops, err = c.Int("TRUSTED_PROXY_HOPS", 0); err != nil {
		return Config{}, err
	}
	if c.TrustedProxyHops < 0 {
		return Config{}, fmt.Errorf("TRUSTED_PROXY_HOPS must not be negative")
	}
	if c.ShutdownTimeout, err = c.Duration("SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Production reports whether APP_ENV is production.
func (c Config) Production() bool { return c.Env == "production" }

// String returns the variable key, or def when it is unset or blank.
func (c Config) String(key, def string) string {
	if c.getenv == nil {
		return def
	}
	if v := strings.TrimSpace(c.getenv(key)); v != "" {
		return v
	}
	return def
}

// Int returns the variable key as an integer, or def when it is unset.
func (c Config) Int(key string, def int) (int, error) {
	v := c.String(key, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, v)
	}
	return n, nil
}

// Duration returns the variable key as a Go duration such as "30s", or def
// when it is unset.
func (c Config) Duration(key string, def time.Duration) (time.Duration, error) {
	v := c.String(key, "")
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 30s, got %q", key, v)
	}
	return d, nil
}

// Bool returns the variable key as a boolean, or def when it is unset.
func (c Config) Bool(key string, def bool) (bool, error) {
	v := c.String(key, "")
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false, got %q", key, v)
	}
	return b, nil
}

// parseOrigin checks that raw is an http(s) origin (scheme, host and an
// optional port, nothing else) and returns it without a trailing slash.
func parseOrigin(name, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%s must be an http or https URL such as https://pathmaxing.example, got %q", name, raw)
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%s must be just a scheme and host, without a path, query or credentials, got %q", name, raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("app: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// NewLogger returns JSON logs in production, where the hosting platform
// parses them, and readable text logs everywhere else.
func NewLogger(c Config, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.Production() {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
