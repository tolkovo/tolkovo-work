// Package config loads the process configuration from TOLKOVO_* environment
// variables into one typed struct. An invalid configuration fails at startup,
// not on the first request.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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

// Deployment modes. SaaS forbids single-node shortcuts such as the fs
// storage driver (docs/02 §5.4).
const (
	ModeSelfHost = "selfhost"
	ModeSaaS     = "saas"
)

// Storage drivers (docs/adr/0009-file-storage.md).
const (
	StorageFS = "fs"
	StorageS3 = "s3"
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

	// RedisURL (redis:// or rediss://) holds sessions and rate limits.
	RedisURL Secret `env:"REDIS_URL" envDefault:"redis://localhost:6379/0"`
	// SessionIdleTimeout ends a session after this long without requests;
	// SessionMaxAge ends it this long after sign-in (docs/02 §13.1).
	SessionIdleTimeout time.Duration `env:"SESSION_IDLE_TIMEOUT" envDefault:"168h"`
	SessionMaxAge      time.Duration `env:"SESSION_MAX_AGE" envDefault:"720h"`
	// TrustedOrigins may send unsafe API requests besides the API's own
	// origin, e.g. a panel served from another host.
	TrustedOrigins []string `env:"TRUSTED_ORIGINS" envSeparator:","`
	// TrustedProxies are reverse proxies (IPs or CIDRs) whose
	// X-Forwarded-For names the client; without them the client is the TCP
	// peer, and behind a proxy all visitors share its rate limits.
	TrustedProxies []string `env:"TRUSTED_PROXIES" envSeparator:","`

	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`

	// Mode is selfhost or saas; saas refuses single-node shortcuts.
	Mode string `env:"MODE" envDefault:"selfhost"`
	// PublicURL is where clients reach the api; signed fs storage links
	// point here.
	PublicURL string `env:"PUBLIC_URL" envDefault:"http://localhost:8080"`
	// PanelURL is where people open the panel; links in emails (invitation,
	// password reset) point here. Defaults to PublicURL: in production the
	// panel and the api share one host (docs/02 §16.2). In development the
	// Vite server runs elsewhere, e.g. http://localhost:5173.
	PanelURL string  `env:"PANEL_URL"`
	Storage  Storage `envPrefix:"STORAGE_"`

	SMTP SMTP `envPrefix:"SMTP_"`

	MasterKey MasterKey `envPrefix:"MASTER_"`

	Telegram Telegram `envPrefix:"TELEGRAM_"`

	// EgressAllowPrivate opens private ranges (IPs or CIDRs) to requests
	// to user-configured URLs, e.g. a local Ollama on self-host
	// (docs/00 §9.5). Link-local stays closed; not allowed in SaaS.
	EgressAllowPrivate []string `env:"EGRESS_ALLOW_PRIVATE" envSeparator:","`

	// ClamAVAddr is the host:port of clamd (TCP). When set, the worker scans
	// files of app reports before they reach the inbox (docs/02 §11.6).
	ClamAVAddr string `env:"CLAMAV_ADDR"`
}

// Storage configures file storage: TOLKOVO_STORAGE_*.
type Storage struct {
	Driver string `env:"DRIVER" envDefault:"fs"`
	// FSRoot is the directory of the fs driver.
	FSRoot string `env:"FS_ROOT" envDefault:"./data/files"`
	// SigningKey authenticates fs upload and download links. Required in
	// production; in development an empty key is replaced by a random one at
	// startup, so links stop working after a restart.
	SigningKey Secret `env:"SIGNING_KEY"`
	// URLTTL is the lifetime of signed links for both drivers.
	URLTTL time.Duration `env:"URL_TTL" envDefault:"15m"`

	S3Endpoint        string `env:"S3_ENDPOINT"`
	S3Region          string `env:"S3_REGION"`
	S3Bucket          string `env:"S3_BUCKET"`
	S3AccessKeyID     string `env:"S3_ACCESS_KEY_ID"`
	S3SecretAccessKey Secret `env:"S3_SECRET_ACCESS_KEY"`
	S3PathStyle       bool   `env:"S3_PATH_STYLE" envDefault:"false"`
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

// SecureCookies reports whether cookies get the Secure flag: everywhere
// except development, which runs over plain HTTP on localhost.
func (c Config) SecureCookies() bool { return c.Env != EnvDevelopment }

// PanelBaseURL returns PanelURL, or PublicURL when it is not set.
func (c Config) PanelBaseURL() string {
	if c.PanelURL != "" {
		return c.PanelURL
	}
	return c.PublicURL
}

// MailEnabled reports whether outgoing email is configured.
func (c Config) MailEnabled() bool { return strings.TrimSpace(c.SMTP.Host) != "" }

// PublicOrigins returns the origin (scheme://host) of PublicURL, the one
// browsers use to reach the api. validate guarantees it parses.
func (c Config) PublicOrigins() []string {
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" {
		return nil
	}
	return []string{u.Scheme + "://" + u.Host}
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
	if c.Mode != ModeSelfHost && c.Mode != ModeSaaS {
		errs = append(errs, fmt.Errorf("%sMODE must be %q or %q, got %q", Prefix, ModeSelfHost, ModeSaaS, c.Mode))
	}
	if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		errs = append(errs, fmt.Errorf("%sPUBLIC_URL must be an absolute http(s) URL without query, got %q", Prefix, c.PublicURL))
	}
	if c.PanelURL != "" {
		if u, err := url.Parse(c.PanelURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
			u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			errs = append(errs, fmt.Errorf("%sPANEL_URL must be an absolute http(s) URL without query, got %q", Prefix, c.PanelURL))
		}
	}
	errs = append(errs, c.validateStorage()...)
	errs = append(errs, c.validateSMTP()...)
	errs = append(errs, c.validateMasterKey()...)
	errs = append(errs, c.validateTelegram()...)
	errs = append(errs, c.validateEgress()...)
	errs = append(errs, c.validateTrustedProxies()...)
	if !strings.HasPrefix(c.RedisURL.Reveal(), "redis://") && !strings.HasPrefix(c.RedisURL.Reveal(), "rediss://") {
		errs = append(errs, fmt.Errorf("%sREDIS_URL must start with redis:// or rediss://", Prefix))
	}
	if c.SessionIdleTimeout < time.Minute || c.SessionMaxAge < c.SessionIdleTimeout {
		errs = append(errs, fmt.Errorf("%sSESSION_IDLE_TIMEOUT must be at least 1m and not above %sSESSION_MAX_AGE", Prefix, Prefix))
	}
	for _, o := range c.TrustedOrigins {
		if u, err := url.Parse(o); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			errs = append(errs, fmt.Errorf("%sTRUSTED_ORIGINS: %q is not an origin like https://panel.example.com", Prefix, o))
		}
	}
	return errors.Join(errs...)
}

// minSigningKeyLen matches storage.MinSigningKeyLen; config does not import
// storage to stay a leaf package.
const minSigningKeyLen = 32

func (c Config) validateStorage() []error {
	s, p := c.Storage, Prefix+"STORAGE_"
	var errs []error
	if s.URLTTL < time.Minute || s.URLTTL > 24*time.Hour {
		errs = append(errs, fmt.Errorf("%sURL_TTL must be between 1m and 24h", p))
	}
	switch s.Driver {
	case StorageFS:
		if c.Mode == ModeSaaS {
			errs = append(errs, fmt.Errorf("%sDRIVER=fs is single-node and not allowed with %sMODE=saas: use s3", p, Prefix))
		}
		if strings.TrimSpace(s.FSRoot) == "" {
			errs = append(errs, fmt.Errorf("%sFS_ROOT must not be empty", p))
		}
		if s.SigningKey == "" && c.Env == EnvProduction {
			errs = append(errs, fmt.Errorf("%sSIGNING_KEY is required for the fs driver in production (generate with `openssl rand -hex 32`)", p))
		}
		if s.SigningKey != "" && len(s.SigningKey.Reveal()) < minSigningKeyLen {
			errs = append(errs, fmt.Errorf("%sSIGNING_KEY must be at least %d characters", p, minSigningKeyLen))
		}
	case StorageS3:
		for _, f := range []struct{ name, value string }{
			{"S3_REGION", s.S3Region}, {"S3_BUCKET", s.S3Bucket},
			{"S3_ACCESS_KEY_ID", s.S3AccessKeyID}, {"S3_SECRET_ACCESS_KEY", s.S3SecretAccessKey.Reveal()},
		} {
			if f.value == "" {
				errs = append(errs, fmt.Errorf("%s%s is required for the s3 driver", p, f.name))
			}
		}
		if s.S3Endpoint != "" {
			if u, err := url.Parse(s.S3Endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				errs = append(errs, fmt.Errorf("%sS3_ENDPOINT must be an absolute http(s) URL, got %q", p, s.S3Endpoint))
			}
		}
	default:
		errs = append(errs, fmt.Errorf("%sDRIVER must be %q or %q, got %q", p, StorageFS, StorageS3, s.Driver))
	}
	return errs
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
