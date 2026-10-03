package config

import "log/slog"

const redacted = "[redacted]"

// Secret holds a sensitive string (DSN with a password, token, key). It never
// prints its value: not through fmt, not through slog. Call Reveal at the
// point of use.
type Secret string

// Reveal returns the underlying value.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer so %#v does not leak the value either.
func (s Secret) GoString() string { return redacted }

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText keeps the value out of JSON and other text encodings.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// UnmarshalText lets the env parser fill the value.
func (s *Secret) UnmarshalText(b []byte) error {
	*s = Secret(b)
	return nil
}
