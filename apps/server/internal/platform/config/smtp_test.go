package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// smtpVars is a minimal valid configuration with email on (Mailpit).
func smtpVars(extra ...string) map[string]string {
	vars := map[string]string{
		"TOLKOVO_DATABASE_URL": dsn,
		"TOLKOVO_SMTP_HOST":    "localhost",
		"TOLKOVO_SMTP_PORT":    "1025",
		"TOLKOVO_SMTP_TLS":     "none",
		"TOLKOVO_SMTP_FROM":    "Tolkovo <noreply@tolkovo.test>",
	}
	for i := 0; i+1 < len(extra); i += 2 {
		vars[extra[i]] = extra[i+1]
	}
	return vars
}

func TestLoadFrom_SMTPIsOffByDefault(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if s := cfg.SMTP; s.Host != "" || s.TLS != SMTPStartTLS || s.Port != 0 || s.Timeout != 15*time.Second {
		t.Fatalf("unexpected smtp defaults: %+v", s)
	}
}

func TestLoadFrom_ProductionSMTPOverTLS(t *testing.T) {
	_, err := LoadFrom(smtpVars(
		"TOLKOVO_ENV", "production", "TOLKOVO_STORAGE_SIGNING_KEY", strings.Repeat("k", 32), "TOLKOVO_MASTER_KEY", prodMasterKey,
		"TOLKOVO_SMTP_HOST", "smtp.example.com", "TOLKOVO_SMTP_PORT", "0", "TOLKOVO_SMTP_TLS", "starttls",
		"TOLKOVO_SMTP_USER", "mailer", "TOLKOVO_SMTP_PASSWORD", "s3cret",
	))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
}

func TestLoadFrom_RejectsInvalidSMTP(t *testing.T) {
	prod := []string{"TOLKOVO_ENV", "production", "TOLKOVO_STORAGE_SIGNING_KEY", strings.Repeat("k", 32)}
	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"bad tls mode", smtpVars("TOLKOVO_SMTP_TLS", "ssl"), "TOLKOVO_SMTP_TLS"},
		{"bad port", smtpVars("TOLKOVO_SMTP_PORT", "70000"), "TOLKOVO_SMTP_PORT"},
		{"bad from", smtpVars("TOLKOVO_SMTP_FROM", "nobody"), "TOLKOVO_SMTP_FROM"},
		{"password without user", smtpVars("TOLKOVO_SMTP_PASSWORD", "x"), "TOLKOVO_SMTP_USER"},
		{"clear text to a remote relay in production", smtpVars(append(prod,
			"TOLKOVO_SMTP_HOST", "smtp.example.com")...), "TOLKOVO_SMTP_TLS=none"},
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
	cfg, err := LoadFrom(smtpVars("TOLKOVO_SMTP_USER", "mailer", "TOLKOVO_SMTP_PASSWORD", "hunter2-smtp"))
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
