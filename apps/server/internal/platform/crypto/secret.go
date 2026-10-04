package crypto

import (
	"fmt"
	"io"
	"log/slog"
	"unicode/utf8"
)

// redacted replaces a secret wherever it would be printed.
const redacted = "[redacted]"

// Secret is a decrypted value. It prints as "[redacted]" with fmt, slog and
// encoding/json, so it cannot leak into logs or API answers by accident;
// Reveal is the one explicit way to the value. Callers use it right before
// a request to the provider and do not keep it longer than needed
// (docs/02 §13.3).
type Secret struct {
	b []byte
}

// NewSecret wraps a value that is already in memory, e.g. a token the
// admin has just typed, before it is sealed.
func NewSecret(value string) Secret { return Secret{b: []byte(value)} }

// Reveal returns the value.
func (s Secret) Reveal() string { return string(s.b) }

// Empty reports whether there is no value.
func (s Secret) Empty() bool { return len(s.b) == 0 }

// Mask returns "…" and the last four characters, the only form an API may
// show (docs/02 §13.3); short values are fully hidden.
func (s Secret) Mask() string {
	if utf8.RuneCount(s.b) < 12 {
		return "…"
	}
	r := []rune(string(s.b))
	return "…" + string(r[len(r)-4:])
}

// String implements fmt.Stringer.
func (Secret) String() string { return redacted }

// Format implements fmt.Formatter for every verb: without it %d or %08b
// would print the bytes of the field.
func (Secret) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, redacted) }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON keeps the value out of JSON.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText keeps the value out of text encodings (and map keys).
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
