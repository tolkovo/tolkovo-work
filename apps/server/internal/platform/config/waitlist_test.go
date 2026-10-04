package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// waitlistVars is a minimal valid configuration with the waitlist on.
func waitlistVars(extra ...string) map[string]string {
	vars := map[string]string{
		"TOLKOVO_DATABASE_URL":      dsn,
		"TOLKOVO_WAITLIST_ENABLED":  "true",
		"TOLKOVO_WAITLIST_SITE_URL": "http://localhost:4321",
		"TOLKOVO_SMTP_HOST":         "localhost",
		"TOLKOVO_SMTP_PORT":         "1025",
		"TOLKOVO_SMTP_TLS":          "none",
		"TOLKOVO_SMTP_FROM":         "Tolkovo <noreply@tolkovo.test>",
	}
	for i := 0; i+1 < len(extra); i += 2 {
		vars[extra[i]] = extra[i+1]
	}
	return vars
}

func TestLoadFrom_WaitlistIsOffByDefault(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	w, s := cfg.Waitlist, cfg.SMTP
	if w.Enabled || w.TokenTTL != 48*time.Hour || w.PendingRetention != 168*time.Hour || len(w.Origins) != 0 {
		t.Fatalf("unexpected waitlist defaults: %+v", w)
	}
	if s.Host != "" || s.TLS != SMTPStartTLS || s.Port != 0 || s.Timeout != 15*time.Second {
		t.Fatalf("unexpected smtp defaults: %+v", s)
	}
}

func TestLoadFrom_WaitlistOriginsDefaultToSite(t *testing.T) {
	cfg, err := LoadFrom(waitlistVars())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.Waitlist.Origins) != 1 || cfg.Waitlist.Origins[0] != "http://localhost:4321" {
		t.Fatalf("origins: %v", cfg.Waitlist.Origins)
	}
	cfg, err = LoadFrom(waitlistVars("TOLKOVO_WAITLIST_ORIGINS", "https://tolkovo.dev,https://www.tolkovo.dev"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.Waitlist.Origins) != 2 {
		t.Fatalf("explicit origins: %v", cfg.Waitlist.Origins)
	}
}

func TestLoadFrom_ProductionWaitlistWithSMTPOverTLS(t *testing.T) {
	_, err := LoadFrom(waitlistVars(
		"TOLKOVO_ENV", "production", "TOLKOVO_STORAGE_SIGNING_KEY", strings.Repeat("k", 32),
		"TOLKOVO_WAITLIST_SITE_URL", "https://tolkovo.dev",
		"TOLKOVO_SMTP_HOST", "smtp.example.com", "TOLKOVO_SMTP_PORT", "0", "TOLKOVO_SMTP_TLS", "starttls",
		"TOLKOVO_SMTP_USER", "mailer", "TOLKOVO_SMTP_PASSWORD", "s3cret",
	))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
}

func TestLoadFrom_RejectsInvalidWaitlistAndSMTP(t *testing.T) {
	prod := []string{"TOLKOVO_ENV", "production", "TOLKOVO_STORAGE_SIGNING_KEY", strings.Repeat("k", 32)}
	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"enabled without smtp", waitlistVars("TOLKOVO_SMTP_HOST", ""), "TOLKOVO_SMTP_HOST"},
		{"enabled without site", waitlistVars("TOLKOVO_WAITLIST_SITE_URL", ""), "TOLKOVO_WAITLIST_SITE_URL"},
		{"site with query", waitlistVars("TOLKOVO_WAITLIST_SITE_URL", "https://tolkovo.dev/?a=1"), "TOLKOVO_WAITLIST_SITE_URL"},
		{"plain http site in production", waitlistVars(append(prod,
			"TOLKOVO_SMTP_HOST", "127.0.0.1")...), "must use https"},
		{"origin with path", waitlistVars("TOLKOVO_WAITLIST_ORIGINS", "https://tolkovo.dev/en"), "TOLKOVO_WAITLIST_ORIGINS"},
		{"short token ttl", waitlistVars("TOLKOVO_WAITLIST_TOKEN_TTL", "10m"), "TOLKOVO_WAITLIST_TOKEN_TTL"},
		{"retention below ttl", waitlistVars("TOLKOVO_WAITLIST_PENDING_RETENTION", "24h"), "TOLKOVO_WAITLIST_PENDING_RETENTION"},
		{"bad tls mode", waitlistVars("TOLKOVO_SMTP_TLS", "ssl"), "TOLKOVO_SMTP_TLS"},
		{"bad port", waitlistVars("TOLKOVO_SMTP_PORT", "70000"), "TOLKOVO_SMTP_PORT"},
		{"bad from", waitlistVars("TOLKOVO_SMTP_FROM", "nobody"), "TOLKOVO_SMTP_FROM"},
		{"password without user", waitlistVars("TOLKOVO_SMTP_PASSWORD", "x"), "TOLKOVO_SMTP_USER"},
		{"clear text to a remote relay in production", waitlistVars(append(prod,
			"TOLKOVO_WAITLIST_SITE_URL", "https://tolkovo.dev", "TOLKOVO_SMTP_HOST", "smtp.example.com")...), "TOLKOVO_SMTP_TLS=none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(tt.vars)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestSMTPPassword_IsRedacted(t *testing.T) {
	cfg, err := LoadFrom(waitlistVars("TOLKOVO_SMTP_USER", "mailer", "TOLKOVO_SMTP_PASSWORD", "hunter2-smtp"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if out := fmt.Sprintf("%v %+v %#v", cfg.SMTP, cfg.SMTP, cfg.SMTP); strings.Contains(out, "hunter2-smtp") {
		t.Fatalf("password leaked: %s", out)
	}
	if cfg.SMTP.Password.Reveal() != "hunter2-smtp" {
		t.Fatal("password not loaded")
	}
}
