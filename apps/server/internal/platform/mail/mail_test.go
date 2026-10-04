package mail_test

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail"
)

// fakeSMTP is a minimal in-process SMTP server on 127.0.0.1. It offers no
// STARTTLS and no AUTH; nothing leaves the machine.
type fakeSMTP struct {
	addr string
	// rcptReply answers RCPT TO; default "250 ok".
	rcptReply string
	data      chan string
}

func startSMTP(t *testing.T, rcptReply string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &fakeSMTP{addr: ln.Addr().String(), rcptReply: rcptReply, data: make(chan string, 1)}
	if s.rcptReply == "" {
		s.rcptReply = "250 ok"
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.serve(conn)
		}
	}()
	return s
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			reply("250-fake")
			reply("250 8BITMIME")
		case strings.HasPrefix(cmd, "RCPT"):
			reply(s.rcptReply)
		case cmd == "DATA":
			reply("354 go on")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.data <- b.String()
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default: // HELO, MAIL, RSET, NOOP
			reply("250 ok")
		}
	}
}

func (s *fakeSMTP) hostPort(t *testing.T) (string, int) {
	t.Helper()
	host, port, _ := net.SplitHostPort(s.addr)
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return host, p
}

func newSender(t *testing.T, srv *fakeSMTP, tlsMode string) *mail.SMTP {
	t.Helper()
	host, port := srv.hostPort(t)
	s, err := mail.NewSMTP(mail.SMTPOptions{
		Host: host, Port: port, From: "Tolkovo <noreply@tolkovo.test>", TLS: tlsMode, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	return s
}

func TestSMTP_SendsTextAndHTMLAlternatives(t *testing.T) {
	srv := startSMTP(t, "")
	err := newSender(t, srv, mail.TLSNone).Send(context.Background(), mail.Message{
		To: "anna@example.test", Subject: "Confirm", Text: "plain body", HTML: "<p>html body</p>",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var data string
	select {
	case data = <-srv.data:
	case <-time.After(5 * time.Second):
		t.Fatal("server got no message")
	}
	for _, want := range []string{
		"From: \"Tolkovo\" <noreply@tolkovo.test>", "To: <anna@example.test>", "Subject: Confirm",
		"Auto-Submitted: auto-generated", "multipart/alternative", "text/plain", "plain body",
		"text/html", "<p>html body</p>", "Message-ID: <",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("message lacks %q:\n%s", want, data)
		}
	}
}

// Server replies often quote the recipient; the error must not, since it
// ends up in River's job errors and logs.
func TestSMTP_ErrorDoesNotLeakRecipient(t *testing.T) {
	srv := startSMTP(t, "550 5.1.1 <anna@example.test>: no such user")
	err := newSender(t, srv, mail.TLSNone).Send(context.Background(), mail.Message{
		To: "anna@example.test", Subject: "Confirm", Text: "body",
	})
	if err == nil {
		t.Fatal("want an error for a rejected recipient")
	}
	if strings.Contains(err.Error(), "anna") {
		t.Fatalf("error leaks the recipient: %v", err)
	}
	if !strings.Contains(err.Error(), "550") {
		t.Fatalf("error should keep the SMTP code: %v", err)
	}
}

// starttls is mandatory: a server without STARTTLS must not get the message
// in clear text.
func TestSMTP_StartTLSDoesNotFallBackToPlainText(t *testing.T) {
	srv := startSMTP(t, "")
	err := newSender(t, srv, mail.TLSStartTLS).Send(context.Background(), mail.Message{
		To: "anna@example.test", Subject: "Confirm", Text: "body",
	})
	if err == nil {
		t.Fatal("want an error: the server offers no STARTTLS")
	}
	select {
	case <-srv.data:
		t.Fatal("message was sent without TLS")
	default:
	}
}

func TestNewSMTP_Validates(t *testing.T) {
	base := mail.SMTPOptions{Host: "smtp.example.test", From: "noreply@tolkovo.test"}
	tests := []struct {
		name string
		edit func(*mail.SMTPOptions)
		ok   bool
	}{
		{"defaults", func(*mail.SMTPOptions) {}, true},
		{"named from", func(o *mail.SMTPOptions) { o.From = "Tolkovo <noreply@tolkovo.test>" }, true},
		{"no host", func(o *mail.SMTPOptions) { o.Host = " " }, false},
		{"bad from", func(o *mail.SMTPOptions) { o.From = "not an address" }, false},
		{"bad tls", func(o *mail.SMTPOptions) { o.TLS = "ssl" }, false},
		{"bad port", func(o *mail.SMTPOptions) { o.Port = 70000 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := base
			tt.edit(&o)
			_, err := mail.NewSMTP(o)
			if (err == nil) != tt.ok {
				t.Fatalf("want ok=%t, got %v", tt.ok, err)
			}
		})
	}
}

func TestDefaultPort(t *testing.T) {
	for mode, want := range map[string]int{mail.TLSStartTLS: 587, mail.TLSImplicit: 465, mail.TLSNone: 25, "": 587} {
		if got := mail.DefaultPort(mode); got != want {
			t.Errorf("DefaultPort(%q) = %d, want %d", mode, got, want)
		}
	}
}

func TestSMTP_RejectsIncompleteMessage(t *testing.T) {
	s, err := mail.NewSMTP(mail.SMTPOptions{Host: "smtp.example.test", From: "noreply@tolkovo.test"})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	// Fails before dialling, so no network is touched.
	if err := s.Send(context.Background(), mail.Message{To: "anna@example.test"}); err == nil {
		t.Fatal("want an error for a message without subject and body")
	}
}
