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
		cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 20*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.MigrateDatabaseURL.Reveal() != dsn {
		t.Fatalf("MigrateDatabaseURL should fall back to DatabaseURL")
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
