package config

import (
	"fmt"
	"net"
	netmail "net/mail"
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

func (c Config) validateSMTP() []error {
	s, p := c.SMTP, Prefix+"SMTP_"
	var errs []error
	if strings.TrimSpace(s.Host) == "" {
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

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
