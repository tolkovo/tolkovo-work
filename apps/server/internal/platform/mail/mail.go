// Package mail sends transactional email over SMTP (github.com/wneessen/go-mail).
// Senders are called from River jobs, never inside a database transaction
// (docs/adr/0004-river-background-jobs.md). Recipients and bodies are
// personal data: nothing here logs them.
package mail

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// Message is one email with a plain text body and an optional HTML
// alternative. Both bodies are complete: some clients show only one.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// TLS modes of the SMTP connection.
const (
	// TLSStartTLS upgrades a plain connection and fails if the server
	// cannot (port 587).
	TLSStartTLS = "starttls"
	// TLSImplicit speaks TLS from the first byte (port 465).
	TLSImplicit = "tls"
	// TLSNone sends in clear text: a local relay or Mailpit only.
	TLSNone = "none"
)

// DefaultPort returns the usual port of a TLS mode.
func DefaultPort(mode string) int {
	switch mode {
	case TLSImplicit:
		return 465
	case TLSNone:
		return 25
	default:
		return 587
	}
}

// SMTPOptions configure NewSMTP.
type SMTPOptions struct {
	Host string
	// Port defaults to DefaultPort(TLS).
	Port     int
	Username string
	// Password is used only together with Username.
	Password string
	// From is the sender, "Name <addr@example.com>" or a bare address.
	From string
	// TLS is TLSStartTLS (default), TLSImplicit or TLSNone.
	TLS string
	// Timeout bounds connecting and every SMTP command; default 15s.
	Timeout time.Duration
}

// SMTP sends each message over a new connection: transactional email is
// rare, and a fresh connection never hits a server-side idle timeout.
type SMTP struct {
	opts SMTPOptions
	from string
}

// NewSMTP validates the options; it does not connect.
func NewSMTP(o SMTPOptions) (*SMTP, error) {
	if strings.TrimSpace(o.Host) == "" {
		return nil, errors.New("mail: SMTP host is required")
	}
	if o.TLS == "" {
		o.TLS = TLSStartTLS
	}
	if o.TLS != TLSStartTLS && o.TLS != TLSImplicit && o.TLS != TLSNone {
		return nil, fmt.Errorf("mail: unknown TLS mode %q", o.TLS)
	}
	if o.Port == 0 {
		o.Port = DefaultPort(o.TLS)
	}
	if o.Port < 1 || o.Port > 65535 {
		return nil, fmt.Errorf("mail: port %d is out of range", o.Port)
	}
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	from, err := netmail.ParseAddress(o.From)
	if err != nil {
		return nil, errors.New("mail: from must be an email address, optionally with a name")
	}
	return &SMTP{opts: o, from: from.String()}, nil
}

// Send delivers one message. The error never contains the recipient.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	msg, err := s.build(m)
	if err != nil {
		return err
	}
	client, err := gomail.NewClient(s.opts.Host, s.clientOptions()...)
	if err != nil {
		return fmt.Errorf("mail: client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mail: send via %s:%d: %w", s.opts.Host, s.opts.Port, redactSendError(err))
	}
	return nil
}

func (s *SMTP) clientOptions() []gomail.Option {
	opts := []gomail.Option{
		gomail.WithPort(s.opts.Port),
		gomail.WithTimeout(s.opts.Timeout),
	}
	switch s.opts.TLS {
	case TLSImplicit:
		opts = append(opts, gomail.WithSSL())
	case TLSNone:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	if s.opts.Username != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(s.opts.Username),
			gomail.WithPassword(s.opts.Password),
		)
	}
	return opts
}

// build renders the MIME message: text/plain, plus multipart/alternative
// with text/html when HTML is set.
func (s *SMTP) build(m Message) (*gomail.Msg, error) {
	if m.To == "" || m.Subject == "" || m.Text == "" {
		return nil, errors.New("mail: message needs a recipient, a subject and a text body")
	}
	msg := gomail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return nil, fmt.Errorf("mail: from: %w", err)
	}
	if err := msg.To(m.To); err != nil {
		return nil, errors.New("mail: invalid recipient address")
	}
	msg.Subject(m.Subject)
	msg.SetMessageID()
	msg.SetDate()
	// Transactional mail: auto-responders must not answer it (RFC 3834).
	msg.SetGenHeader(gomail.Header("Auto-Submitted"), "auto-generated")
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}
	return msg, nil
}

// redactSendError keeps only the reason and status codes of go-mail's
// SendError: its text lists the recipients, and server replies often quote
// the address too ("550 <anna@example.com>: no such user").
func redactSendError(err error) error {
	var se *gomail.SendError
	if errors.As(err, &se) {
		return fmt.Errorf("%s: smtp code %d %s (temporary: %t)",
			se.Reason, se.ErrorCode(), se.EnhancedStatusCode(), se.IsTemp())
	}
	return err
}
