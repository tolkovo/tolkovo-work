package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

const dsn = "postgres://app:s3cret@localhost:5432/tolkovo"

func TestLoadFrom_AppliesDefaults(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Env != EnvDevelopment || cfg.HTTPAddr != ":8080" || cfg.LogFormat != "json" ||
		cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 20*time.Second ||
		cfg.OutboxRetention != 24*time.Hour {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.MigrateDatabaseURL.Reveal() != dsn {
		t.Fatalf("MigrateDatabaseURL should fall back to DatabaseURL")
	}
	if cfg.SystemDatabaseURL != "" {
		t.Fatalf("SystemDatabaseURL must never fall back to DatabaseURL")
	}
	if cfg.RedisURL.Reveal() != "redis://localhost:6379/0" || cfg.SessionIdleTimeout != 168*time.Hour ||
		cfg.SessionMaxAge != 720*time.Hour || len(cfg.TrustedOrigins) != 0 || cfg.SecureCookies() {
		t.Fatalf("unexpected session defaults: %+v", cfg)
	}
}

func TestLoadFrom_SecureCookiesOutsideDevelopment(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{
		"TOLKOVO_DATABASE_URL": dsn, "TOLKOVO_ENV": "production",
		"TOLKOVO_STORAGE_SIGNING_KEY": strings.Repeat("k", 32),
		"TOLKOVO_TRUSTED_ORIGINS":     "https://panel.example.com,http://localhost:5173",
	})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !cfg.SecureCookies() || len(cfg.TrustedOrigins) != 2 {
		t.Fatalf("want secure cookies and 2 origins, got %v %v", cfg.SecureCookies(), cfg.TrustedOrigins)
	}
	cfg.PublicURL = "https://support.example.com/base/"
	if got := cfg.PublicOrigins(); len(got) != 1 || got[0] != "https://support.example.com" {
		t.Fatalf("PublicOrigins: %v", got)
	}
}

func TestLoadFrom_RequiresDatabaseURL(t *testing.T) {
	_, err := LoadFrom(map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "TOLKOVO_DATABASE_URL") {
		t.Fatalf("want error naming TOLKOVO_DATABASE_URL, got %v", err)
	}
}

func TestLoadFrom_RejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name, key, value, want string
	}{
		{"unknown env", "TOLKOVO_ENV", "staging", "TOLKOVO_ENV"},
		{"unknown log format", "TOLKOVO_LOG_FORMAT", "xml", "TOLKOVO_LOG_FORMAT"},
		{"empty http addr", "TOLKOVO_HTTP_ADDR", " ", "TOLKOVO_HTTP_ADDR"},
		{"zero shutdown timeout", "TOLKOVO_SHUTDOWN_TIMEOUT", "0s", "TOLKOVO_SHUTDOWN_TIMEOUT"},
		{"bad log level", "TOLKOVO_LOG_LEVEL", "loud", "TOLKOVO_LOG_LEVEL"},
		{"tiny outbox retention", "TOLKOVO_OUTBOX_RETENTION", "10s", "TOLKOVO_OUTBOX_RETENTION"},
		{"redis url scheme", "TOLKOVO_REDIS_URL", "localhost:6379", "TOLKOVO_REDIS_URL"},
		{"idle above max age", "TOLKOVO_SESSION_IDLE_TIMEOUT", "1000h", "TOLKOVO_SESSION_IDLE_TIMEOUT"},
		{"origin with path", "TOLKOVO_TRUSTED_ORIGINS", "https://panel.example.com/app", "TOLKOVO_TRUSTED_ORIGINS"},
		{"origin without scheme", "TOLKOVO_TRUSTED_ORIGINS", "panel.example.com", "TOLKOVO_TRUSTED_ORIGINS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn, tt.key: tt.value})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestSecret_NeverPrintsValue(t *testing.T) {
	s := Secret("s3cret")
	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("x", "dsn", s)
	js, _ := json.Marshal(struct{ S Secret }{s})

	outputs := []string{
		fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s", s, s, s, s),
		fmt.Sprintf("%+v", Config{DatabaseURL: s}),
		logBuf.String(), string(js),
	}
	for _, out := range outputs {
		if strings.Contains(out, "s3cret") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	if s.Reveal() != "s3cret" {
		t.Fatalf("Reveal must return the value")
	}
}

func TestLoadFrom_StorageDefaultsToFSInSelfHost(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Mode != ModeSelfHost || cfg.Storage.Driver != StorageFS || cfg.Storage.FSRoot == "" ||
		cfg.Storage.URLTTL != 15*time.Minute || cfg.PublicURL != "http://localhost:8080" {
		t.Fatalf("unexpected storage defaults: mode=%q %+v public=%q", cfg.Mode, cfg.Storage, cfg.PublicURL)
	}
}

func TestLoadFrom_RejectsInvalidStorage(t *testing.T) {
	s3 := map[string]string{
		"TOLKOVO_STORAGE_DRIVER":               "s3",
		"TOLKOVO_STORAGE_S3_REGION":            "garage",
		"TOLKOVO_STORAGE_S3_BUCKET":            "tolkovo",
		"TOLKOVO_STORAGE_S3_ACCESS_KEY_ID":     "GK1",
		"TOLKOVO_STORAGE_S3_SECRET_ACCESS_KEY": "s3-very-private",
	}
	key := strings.Repeat("k", 32)
	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"saas with fs", map[string]string{"TOLKOVO_MODE": "saas", "TOLKOVO_STORAGE_SIGNING_KEY": key}, "TOLKOVO_STORAGE_DRIVER=fs"},
		{"unknown mode", map[string]string{"TOLKOVO_MODE": "cloud"}, "TOLKOVO_MODE"},
		{"unknown driver", map[string]string{"TOLKOVO_STORAGE_DRIVER": "minio"}, "TOLKOVO_STORAGE_DRIVER"},
		{"fs without key in production", map[string]string{"TOLKOVO_ENV": "production"}, "TOLKOVO_STORAGE_SIGNING_KEY"},
		{"short signing key", map[string]string{"TOLKOVO_STORAGE_SIGNING_KEY": "short"}, "TOLKOVO_STORAGE_SIGNING_KEY"},
		{"empty fs root", map[string]string{"TOLKOVO_STORAGE_FS_ROOT": " "}, "TOLKOVO_STORAGE_FS_ROOT"},
		{"ttl too long", map[string]string{"TOLKOVO_STORAGE_URL_TTL": "48h"}, "TOLKOVO_STORAGE_URL_TTL"},
		{"relative public url", map[string]string{"TOLKOVO_PUBLIC_URL": "/x"}, "TOLKOVO_PUBLIC_URL"},
		{"s3 without bucket", with(s3, "TOLKOVO_STORAGE_S3_BUCKET", ""), "TOLKOVO_STORAGE_S3_BUCKET"},
		{"s3 without secret", with(s3, "TOLKOVO_STORAGE_S3_SECRET_ACCESS_KEY", ""), "TOLKOVO_STORAGE_S3_SECRET_ACCESS_KEY"},
		{"s3 bad endpoint", with(s3, "TOLKOVO_STORAGE_S3_ENDPOINT", "localhost:3900"), "TOLKOVO_STORAGE_S3_ENDPOINT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(with(tt.vars, "TOLKOVO_DATABASE_URL", dsn))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}

	t.Run("saas with s3 is accepted", func(t *testing.T) {
		cfg, err := LoadFrom(with(with(s3, "TOLKOVO_MODE", "saas"), "TOLKOVO_DATABASE_URL", dsn))
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if cfg.Storage.S3SecretAccessKey.Reveal() != "s3-very-private" {
			t.Fatalf("s3 secret not loaded")
		}
		if strings.Contains(fmt.Sprintf("%+v", cfg), "s3-very-private") {
			t.Fatalf("s3 secret leaked through fmt")
		}
	})
}

func with(vars map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(vars)+1)
	for key, val := range vars {
		out[key] = val
	}
	out[k] = v
	return out
}
