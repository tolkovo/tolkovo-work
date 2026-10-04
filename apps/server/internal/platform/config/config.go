// Package config loads the process configuration from TOLKOVO_* environment
// variables into one typed struct. An invalid configuration fails at startup,
// not on the first request.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Prefix is prepended to every environment variable name.
const Prefix = "TOLKOVO_"

// Environments the process can run in.
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// Config is the full process configuration. Every field has a safe default
// for self-host except the database URL. All variables are documented in
// deploy/compose/.env.example.
type Config struct {
	Env       string `env:"ENV" envDefault:"development"`
	HTTPAddr  string `env:"HTTP_ADDR" envDefault:":8080"`
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"`
	// LogLevel is parsed from LogLevelName in LoadFrom, so a typo produces an
	// error naming the variable instead of the Go field.
	LogLevel     slog.Level `env:"-"`
	LogLevelName string     `env:"LOG_LEVEL" envDefault:"info"`

	// DatabaseURL is used by the application role. That role must not own the
	// tables and must not have BYPASSRLS, otherwise RLS does not protect anything.
	DatabaseURL Secret `env:"DATABASE_URL,required"`
	// MigrateDatabaseURL is used by `tolkovo migrate` and must belong to the
	// table owner. Falls back to DatabaseURL for single-role setups.
	MigrateDatabaseURL Secret `env:"MIGRATE_DATABASE_URL"`
	// SystemDatabaseURL is used by system processes of the worker (outbox
	// relay, retention) and must belong to a member of tolkovo_system.
	// Required for roles worker and all; never falls back to DatabaseURL,
	// because the application role would silently see no events.
	SystemDatabaseURL Secret `env:"SYSTEM_DATABASE_URL"`

	// OutboxRetention is how long published events stay for realtime catch-up.
	OutboxRetention time.Duration `env:"OUTBOX_RETENTION" envDefault:"24h"`

	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`
}

// Load reads the configuration from the process environment.
func Load() (Config, error) {
	return LoadFrom(environ())
}

// LoadFrom reads the configuration from the given variables. Tests use it to
// avoid touching the process environment.
func LoadFrom(vars map[string]string) (Config, error) {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{
		Environment: vars,
		Prefix:      Prefix,
	})
	if err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.MigrateDatabaseURL == "" {
		cfg.MigrateDatabaseURL = cfg.DatabaseURL
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(cfg.LogLevelName)); err != nil {
		return Config{}, fmt.Errorf("invalid config: %sLOG_LEVEL must be debug, info, warn or error, got %q", Prefix, cfg.LogLevelName)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []error
	if c.Env != EnvDevelopment && c.Env != EnvProduction {
		errs = append(errs, fmt.Errorf("%sENV must be %q or %q, got %q", Prefix, EnvDevelopment, EnvProduction, c.Env))
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("%sLOG_FORMAT must be \"json\" or \"text\", got %q", Prefix, c.LogFormat))
	}
	if strings.TrimSpace(c.HTTPAddr) == "" {
		errs = append(errs, fmt.Errorf("%sHTTP_ADDR must not be empty", Prefix))
	}
	if c.OutboxRetention < time.Minute {
		errs = append(errs, fmt.Errorf("%sOUTBOX_RETENTION must be at least 1m", Prefix))
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("%sSHUTDOWN_TIMEOUT must be positive", Prefix))
	}
	return errors.Join(errs...)
}

func environ() map[string]string {
	vars := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	return vars
}
