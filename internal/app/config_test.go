package app

import (
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"
)

func envFunc(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestLoad(t *testing.T) {
	longSecret := strings.Repeat("s", 32)
	tests := []struct {
		name    string
		env     map[string]string
		check   func(t *testing.T, c Config)
		wantErr string
	}{
		{
			name: "development defaults",
			env:  map[string]string{},
			check: func(t *testing.T, c Config) {
				if c.Env != "development" || c.Addr != ":8080" || c.BaseURL != "http://localhost:8080" {
					t.Errorf("got env=%q addr=%q base=%q", c.Env, c.Addr, c.BaseURL)
				}
				if !c.SecretGenerated || len(c.SecretKey) < minSecretLen {
					t.Errorf("want a generated secret, got generated=%v len=%d", c.SecretGenerated, len(c.SecretKey))
				}
				if c.LogLevel != slog.LevelInfo || c.TrustedProxyHops != 0 || c.ShutdownTimeout != 10*time.Second {
					t.Errorf("got level=%v hops=%d timeout=%v", c.LogLevel, c.TrustedProxyHops, c.ShutdownTimeout)
				}
				if !slices.Equal(c.AllowedOrigins, []string{"http://localhost:8080"}) {
					t.Errorf("AllowedOrigins = %v", c.AllowedOrigins)
				}
			},
		},
		{
			name: "PORT sets the listen address and default base URL",
			env:  map[string]string{"PORT": "10000"},
			check: func(t *testing.T, c Config) {
				if c.Addr != ":10000" || c.BaseURL != "http://localhost:10000" {
					t.Errorf("got addr=%q base=%q", c.Addr, c.BaseURL)
				}
			},
		},
		{
			name: "ADDR wins over PORT",
			env:  map[string]string{"PORT": "10000", "ADDR": "127.0.0.1:9000"},
			check: func(t *testing.T, c Config) {
				if c.Addr != "127.0.0.1:9000" {
					t.Errorf("addr = %q", c.Addr)
				}
			},
		},
		{
			name: "production with everything set",
			env: map[string]string{
				"APP_ENV": "production", "BASE_URL": "https://pm.example/", "SECRET_KEY": longSecret,
				"ALLOWED_ORIGINS": " https://admin.pm.example , ", "TRUSTED_PROXY_HOPS": "1",
				"LOG_LEVEL": "debug", "SHUTDOWN_TIMEOUT": "20s", "DATABASE_URL": "postgres://x",
			},
			check: func(t *testing.T, c Config) {
				if !c.Production() || c.BaseURL != "https://pm.example" || c.SecretGenerated || c.SecretKey != longSecret {
					t.Errorf("got prod=%v base=%q generated=%v", c.Production(), c.BaseURL, c.SecretGenerated)
				}
				if !slices.Equal(c.AllowedOrigins, []string{"https://pm.example", "https://admin.pm.example"}) {
					t.Errorf("AllowedOrigins = %v", c.AllowedOrigins)
				}
				if c.TrustedProxyHops != 1 || c.LogLevel != slog.LevelDebug || c.ShutdownTimeout != 20*time.Second || c.DatabaseURL != "postgres://x" {
					t.Errorf("got hops=%d level=%v timeout=%v db=%q", c.TrustedProxyHops, c.LogLevel, c.ShutdownTimeout, c.DatabaseURL)
				}
			},
		},
		{name: "unknown APP_ENV", env: map[string]string{"APP_ENV": "staging"}, wantErr: "APP_ENV"},
		{name: "production needs a secret", env: map[string]string{"APP_ENV": "production", "BASE_URL": "https://pm.example"}, wantErr: "SECRET_KEY is required"},
		{name: "short secret", env: map[string]string{"SECRET_KEY": "short"}, wantErr: "at least 32"},
		{name: "production needs https", env: map[string]string{"APP_ENV": "production", "SECRET_KEY": longSecret, "BASE_URL": "http://pm.example"}, wantErr: "https"},
		{name: "base URL with a path", env: map[string]string{"BASE_URL": "https://pm.example/app"}, wantErr: "without a path"},
		{name: "base URL with credentials", env: map[string]string{"BASE_URL": "https://u:p@pm.example"}, wantErr: "credentials"},
		{name: "base URL not http", env: map[string]string{"BASE_URL": "ftp://pm.example"}, wantErr: "http or https"},
		{name: "bad allowed origin", env: map[string]string{"ALLOWED_ORIGINS": "localhost:5173"}, wantErr: "ALLOWED_ORIGINS"},
		{name: "bad log level", env: map[string]string{"LOG_LEVEL": "loud"}, wantErr: "LOG_LEVEL"},
		{name: "negative proxy hops", env: map[string]string{"TRUSTED_PROXY_HOPS": "-1"}, wantErr: "negative"},
		{name: "proxy hops not a number", env: map[string]string{"TRUSTED_PROXY_HOPS": "one"}, wantErr: "integer"},
		{name: "bad shutdown timeout", env: map[string]string{"SHUTDOWN_TIMEOUT": "10"}, wantErr: "duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Load(envFunc(tt.env))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, c)
		})
	}
}

func TestGeneratedSecretsDiffer(t *testing.T) {
	a, _ := Load(envFunc(nil))
	b, _ := Load(envFunc(nil))
	if a.SecretKey == b.SecretKey {
		t.Fatal("two generated secrets are equal")
	}
}

func TestPartVariables(t *testing.T) {
	c, err := Load(envFunc(map[string]string{
		"QUEUE_SIZE": "500", "SCAN_EVERY": "45s", "FEATURE_ON": "true", "NAME": "  x  ",
		"BAD_INT": "five", "BAD_DUR": "-1s", "BAD_BOOL": "maybe",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if v := c.String("NAME", "d"); v != "x" {
		t.Errorf("String = %q", v)
	}
	if v := c.String("MISSING", "d"); v != "d" {
		t.Errorf("String default = %q", v)
	}
	if n, err := c.Int("QUEUE_SIZE", 1); n != 500 || err != nil {
		t.Errorf("Int = %d, %v", n, err)
	}
	if n, err := c.Int("MISSING", 7); n != 7 || err != nil {
		t.Errorf("Int default = %d, %v", n, err)
	}
	if d, err := c.Duration("SCAN_EVERY", time.Second); d != 45*time.Second || err != nil {
		t.Errorf("Duration = %v, %v", d, err)
	}
	if b, err := c.Bool("FEATURE_ON", false); !b || err != nil {
		t.Errorf("Bool = %v, %v", b, err)
	}
	for _, f := range []func() error{
		func() error { _, err := c.Int("BAD_INT", 0); return err },
		func() error { _, err := c.Duration("BAD_DUR", 0); return err },
		func() error { _, err := c.Bool("BAD_BOOL", false); return err },
	} {
		if f() == nil {
			t.Error("malformed value accepted")
		}
	}
	var zero Config
	if v := zero.String("ANY", "def"); v != "def" {
		t.Errorf("zero Config String = %q", v)
	}
}
