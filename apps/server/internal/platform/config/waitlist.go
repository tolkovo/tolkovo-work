package config

import (
	"fmt"
	"net"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"
)

// SMTP TLS modes; they match internal/platform/mail, which config does not
// import to stay a leaf package.
const (
	SMTPStartTLS = "starttls"
	SMTPTLS      = "tls"
	SMTPNone     = "none"
)

// SMTP configures outgoing email: TOLKOVO_SMTP_*. An empty host disables
// email; features that need it refuse to start without it.
type SMTP struct {
	Host string `env:"HOST"`
	// Port 0 means the usual port of the TLS mode: 587, 465 or 25.
	Port     int    `env:"PORT" envDefault:"0"`
	User     string `env:"USER"`
	Password Secret `env:"PASSWORD"`
	// From is "Name <noreply@example.com>" or a bare address.
	From string `env:"FROM"`
	// TLS is starttls (mandatory upgrade), tls (implicit) or none.
	TLS     string        `env:"TLS" envDefault:"starttls"`
	Timeout time.Duration `env:"TIMEOUT" envDefault:"15s"`
}

// Waitlist configures the public waitlist of the landing with double
// opt-in: TOLKOVO_WAITLIST_* (docs/05 §6). Off by default: a self-hosted
// instance has no landing and should not accept addresses from strangers.
type Waitlist struct {
	Enabled bool `env:"ENABLED" envDefault:"false"`
	// SiteURL is the landing; confirmation links point to its pages.
	SiteURL string `env:"SITE_URL"`
	// Origins may call the waitlist API from a browser (CORS). Empty means
	// the origin of SiteURL.
	Origins []string `env:"ORIGINS" envSeparator:","`
	// TokenTTL is how long a confirmation link works.
	TokenTTL time.Duration `env:"TOKEN_TTL" envDefault:"48h"`
	// PendingRetention deletes unconfirmed entries this long after the last
	// submission (GDPR data minimisation).
	PendingRetention time.Duration `env:"PENDING_RETENTION" envDefault:"168h"`
}

func (w *Waitlist) applyDefaults() {
	if len(w.Origins) > 0 || w.SiteURL == "" {
		return
	}
	if u, err := url.Parse(w.SiteURL); err == nil && u.Host != "" {
		w.Origins = []string{u.Scheme + "://" + u.Host}
	}
}

func (c Config) validateSMTP() []error {
	s, p := c.SMTP, Prefix+"SMTP_"
	var errs []error
	if strings.TrimSpace(s.Host) == "" {
		if c.Waitlist.Enabled {
			errs = append(errs, fmt.Errorf("%sHOST is required with %sWAITLIST_ENABLED=true: the waitlist sends confirmation emails", p, Prefix))
		}
		return errs
	}
	if s.TLS != SMTPStartTLS && s.TLS != SMTPTLS && s.TLS != SMTPNone {
		errs = append(errs, fmt.Errorf("%sTLS must be %q, %q or %q, got %q", p, SMTPStartTLS, SMTPTLS, SMTPNone, s.TLS))
	}
	if s.Port < 0 || s.Port > 65535 {
		errs = append(errs, fmt.Errorf("%sPORT must be between 1 and 65535, or 0 for the default of the TLS mode", p))
	}
	if s.Timeout <= 0 || s.Timeout > 2*time.Minute {
		errs = append(errs, fmt.Errorf("%sTIMEOUT must be between 1ns and 2m", p))
	}
	if _, err := netmail.ParseAddress(s.From); err != nil {
		errs = append(errs, fmt.Errorf("%sFROM must be an address like \"Tolkovo <noreply@example.com>\"", p))
	}
	if s.Password != "" && s.User == "" {
		errs = append(errs, fmt.Errorf("%sPASSWORD is set but %sUSER is empty", p, p))
	}
	// Clear text is for Mailpit and a relay on the same host only.
	if s.TLS == SMTPNone && c.Env == EnvProduction && !isLoopback(s.Host) {
		errs = append(errs, fmt.Errorf("%sTLS=none is allowed in production only for a relay on localhost", p))
	}
	return errs
}

func (c Config) validateWaitlist() []error {
	w, p := c.Waitlist, Prefix+"WAITLIST_"
	var errs []error
	if w.TokenTTL < time.Hour || w.TokenTTL > 7*24*time.Hour {
		errs = append(errs, fmt.Errorf("%sTOKEN_TTL must be between 1h and 168h", p))
	}
	if w.PendingRetention < w.TokenTTL || w.PendingRetention > 90*24*time.Hour {
		errs = append(errs, fmt.Errorf("%sPENDING_RETENTION must be at least %sTOKEN_TTL and at most 2160h", p, p))
	}
	for _, o := range w.Origins {
		if u, err := url.Parse(o); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			errs = append(errs, fmt.Errorf("%sORIGINS: %q is not an origin like https://tolkovo.dev", p, o))
		}
	}
	if !w.Enabled {
		return errs
	}
	u, err := url.Parse(w.SiteURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		errs = append(errs, fmt.Errorf("%sSITE_URL must be an absolute http(s) URL of the landing, got %q", p, w.SiteURL))
	} else if c.Env == EnvProduction && u.Scheme != "https" {
		errs = append(errs, fmt.Errorf("%sSITE_URL must use https in production: links carry tokens", p))
	}
	if len(w.Origins) == 0 {
		errs = append(errs, fmt.Errorf("%sORIGINS must list at least one origin", p))
	}
	return errs
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
