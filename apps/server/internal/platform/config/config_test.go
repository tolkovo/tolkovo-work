package config

import (
	"bytes"
	"encoding/base64"
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
		"TOLKOVO_MASTER_KEY":          prodMasterKey,
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

func TestPanelBaseURLAndMail(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.PanelBaseURL() != cfg.PublicURL || cfg.MailEnabled() {
		t.Fatalf("panel URL must default to the public URL and mail be off: %q %v", cfg.PanelBaseURL(), cfg.MailEnabled())
	}
	cfg, err = LoadFrom(map[string]string{
		"TOLKOVO_DATABASE_URL": dsn, "TOLKOVO_PANEL_URL": "http://localhost:5173",
		"TOLKOVO_SMTP_HOST": "localhost", "TOLKOVO_SMTP_FROM": "Tolkovo <noreply@example.com>",
	})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.PanelBaseURL() != "http://localhost:5173" || !cfg.MailEnabled() {
		t.Fatalf("got %q %v", cfg.PanelBaseURL(), cfg.MailEnabled())
	}
}

func TestRegistrationMode(t *testing.T) {
	for _, tt := range []struct{ mode, set, want string }{
		{"selfhost", "", "first"},
		{"saas", "", "open"},
		{"selfhost", "open", "open"},
		{"saas", "closed", "closed"},
	} {
		vars := map[string]string{"TOLKOVO_DATABASE_URL": dsn, "TOLKOVO_MODE": tt.mode, "TOLKOVO_REGISTRATION": tt.set}
		if tt.mode == "saas" {
			vars["TOLKOVO_STORAGE_DRIVER"], vars["TOLKOVO_STORAGE_S3_REGION"], vars["TOLKOVO_STORAGE_S3_BUCKET"] = "s3", "eu", "b"
			vars["TOLKOVO_STORAGE_S3_ACCESS_KEY_ID"], vars["TOLKOVO_STORAGE_S3_SECRET_ACCESS_KEY"] = "id", "secret"
		}
		cfg, err := LoadFrom(vars)
		if err != nil {
			t.Fatalf("%+v: %v", tt, err)
		}
		if got := cfg.RegistrationMode(); got != tt.want {
			t.Errorf("%+v: got %q", tt, got)
		}
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
		{"relative panel url", "TOLKOVO_PANEL_URL", "/app", "TOLKOVO_PANEL_URL"},
		{"panel url with query", "TOLKOVO_PANEL_URL", "https://app.example.com/?x=1", "TOLKOVO_PANEL_URL"},
		{"unknown registration", "TOLKOVO_REGISTRATION", "invite", "TOLKOVO_REGISTRATION"},
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

func TestLoadFrom_MasterKey(t *testing.T) {
	key := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes
	cfg, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn})
	if err != nil || cfg.MasterKey.Key != "" || cfg.MasterKey.Version != 1 {
		t.Fatalf("defaults: %+v %v", cfg.MasterKey, err)
	}
	cfg, err = LoadFrom(map[string]string{
		"TOLKOVO_DATABASE_URL": dsn, "TOLKOVO_MASTER_KEY": key, "TOLKOVO_MASTER_KEY_VERSION": "2",
		"TOLKOVO_MASTER_KEYS_RETIRED": "1:" + DevMasterKey,
	})
	if err != nil {
		t.Fatalf("valid keyring: %v", err)
	}
	if cfg.MasterKey.Key.Reveal() != key || cfg.MasterKey.Version != 2 {
		t.Fatalf("parsed: %+v", cfg.MasterKey)
	}
	// The development example key works in development only.
	if _, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn, "TOLKOVO_MASTER_KEY": DevMasterKey}); err != nil {
		t.Fatalf("dev key in development: %v", err)
	}

	tests := []struct {
		name string
		vars map[string]string
	}{
		{"not base64", map[string]string{"TOLKOVO_MASTER_KEY": "%%%"}},
		{"short", map[string]string{"TOLKOVO_MASTER_KEY": "c2hvcnQ="}},
		{"version zero", map[string]string{"TOLKOVO_MASTER_KEY": key, "TOLKOVO_MASTER_KEY_VERSION": "0"}},
		{"retired without current", map[string]string{"TOLKOVO_MASTER_KEYS_RETIRED": "1:" + key}},
		{"retired reuses version", map[string]string{"TOLKOVO_MASTER_KEY": key, "TOLKOVO_MASTER_KEYS_RETIRED": "1:" + DevMasterKey}},
		{"retired malformed", map[string]string{"TOLKOVO_MASTER_KEY": key, "TOLKOVO_MASTER_KEY_VERSION": "2", "TOLKOVO_MASTER_KEYS_RETIRED": DevMasterKey}},
		{"no key in production", map[string]string{
			"TOLKOVO_ENV": "production", "TOLKOVO_STORAGE_SIGNING_KEY": strings.Repeat("k", 32),
		}},
		{"example key in production", map[string]string{
			"TOLKOVO_MASTER_KEY": DevMasterKey, "TOLKOVO_ENV": "production",
			"TOLKOVO_STORAGE_SIGNING_KEY": strings.Repeat("k", 32),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := map[string]string{"TOLKOVO_DATABASE_URL": dsn}
			for k, v := range tt.vars {
				vars[k] = v
			}
			_, err := LoadFrom(vars)
			if err == nil || !strings.Contains(err.Error(), "TOLKOVO_MASTER_") {
				t.Fatalf("want an error naming TOLKOVO_MASTER_*, got %v", err)
			}
			if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), DevMasterKey) {
				t.Fatalf("error leaks the key: %v", err)
			}
		})
	}
}

func TestLoadFrom_EgressAllowPrivate(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{
		"TOLKOVO_DATABASE_URL": dsn, "TOLKOVO_EGRESS_ALLOW_PRIVATE": "127.0.0.1, 172.18.0.0/16,fd00::5",
	})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	got := cfg.EgressAllowPrivatePrefixes()
	if len(got) != 3 || got[0].String() != "127.0.0.1/32" || got[1].String() != "172.18.0.0/16" || got[2].String() != "fd00::5/128" {
		t.Fatalf("prefixes: %v", got)
	}
	for name, vars := range map[string]map[string]string{
		"garbage":    {"TOLKOVO_EGRESS_ALLOW_PRIVATE": "ollama"},
		"everything": {"TOLKOVO_EGRESS_ALLOW_PRIVATE": "0.0.0.0/0"},
		"metadata":   {"TOLKOVO_EGRESS_ALLOW_PRIVATE": "169.254.169.254"},
		"wide":       {"TOLKOVO_EGRESS_ALLOW_PRIVATE": "128.0.0.0/1"},
		"saas": {
			"TOLKOVO_EGRESS_ALLOW_PRIVATE": "127.0.0.1", "TOLKOVO_MODE": "saas", "TOLKOVO_STORAGE_DRIVER": "s3",
			"TOLKOVO_STORAGE_S3_REGION": "eu", "TOLKOVO_STORAGE_S3_BUCKET": "b",
			"TOLKOVO_STORAGE_S3_ACCESS_KEY_ID": "id", "TOLKOVO_STORAGE_S3_SECRET_ACCESS_KEY": "secret",
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := map[string]string{"TOLKOVO_DATABASE_URL": dsn}
			for k, val := range vars {
				v[k] = val
			}
			if _, err := LoadFrom(v); err == nil || !strings.Contains(err.Error(), "TOLKOVO_EGRESS_ALLOW_PRIVATE") {
				t.Fatalf("want an error naming TOLKOVO_EGRESS_ALLOW_PRIVATE, got %v", err)
			}
		})
	}
}

// prodMasterKey is a valid key that is not the public example.
var prodMasterKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32)))

func TestLoadFrom_Telegram(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.TelegramUserEnabled() || cfg.Telegram.BotAPIURL != "https://api.telegram.org" {
		t.Fatalf("defaults: %+v", cfg.Telegram)
	}
	hash := "0123456789abcdef0123456789abcdef"
	cfg, err = LoadFrom(map[string]string{
		"TOLKOVO_DATABASE_URL": dsn, "TOLKOVO_TELEGRAM_API_ID": "12345", "TOLKOVO_TELEGRAM_API_HASH": hash,
		"TOLKOVO_TELEGRAM_BOT_API_URL": "http://telegram-bot-api:8081",
	})
	if err != nil || !cfg.TelegramUserEnabled() || cfg.Telegram.APIID != 12345 {
		t.Fatalf("configured: %+v %v", cfg.Telegram, err)
	}

	for name, vars := range map[string]map[string]string{
		"id without hash": {"TOLKOVO_TELEGRAM_API_ID": "12345"},
		"hash without id": {"TOLKOVO_TELEGRAM_API_HASH": hash},
		"short hash":      {"TOLKOVO_TELEGRAM_API_ID": "12345", "TOLKOVO_TELEGRAM_API_HASH": "abc"},
		"bad bot api url": {"TOLKOVO_TELEGRAM_BOT_API_URL": "api.telegram.org"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFrom(with(vars, "TOLKOVO_DATABASE_URL", dsn))
			if err == nil || !strings.Contains(err.Error(), "TOLKOVO_TELEGRAM_") {
				t.Fatalf("want an error naming TOLKOVO_TELEGRAM_*, got %v", err)
			}
			if strings.Contains(err.Error(), hash) {
				t.Fatalf("error leaks the api hash: %v", err)
			}
		})
	}
}
