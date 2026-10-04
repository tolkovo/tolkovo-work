// Package mailtest provides an in-memory mail.Sender for tests: nothing
// leaves the process.
package mailtest

import (
	"context"
	"sync"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail"
)

// Sender records messages instead of sending them. The zero value is ready.
type Sender struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

var _ mail.Sender = (*Sender)(nil)

// Send implements mail.Sender.
func (s *Sender) Send(_ context.Context, m mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, m)
	return nil
}

// SetErr changes the error Send returns; nil makes it succeed again.
func (s *Sender) SetErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// Sent returns a copy of the recorded messages, oldest first.
func (s *Sender) Sent() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.sent...)
}

// Forget drops the recorded messages to the address, so a test can wait for
// the next one.
func (s *Sender) Forget(to string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.sent[:0]
	for _, m := range s.sent {
		if m.To != to {
			kept = append(kept, m)
		}
	}
	s.sent = kept
}
