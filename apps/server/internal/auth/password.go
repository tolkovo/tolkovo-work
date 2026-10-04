package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password length limits. The upper bound keeps hashing cost bounded.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 128
)

// HashParams are argon2id parameters. Defaults follow the OWASP password
// storage cheat sheet: 19 MiB, 2 passes, 1 lane.
type HashParams struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

// DefaultHashParams is used for new hashes.
var DefaultHashParams = HashParams{MemoryKiB: 19 * 1024, Time: 2, Threads: 1}

const (
	saltLen = 16
	keyLen  = 32
	// Upper bounds for parameters read back from a stored hash, so a
	// corrupted row cannot make one login allocate gigabytes.
	maxMemoryKiB = 256 * 1024
	maxTime      = 10
)

var errMalformedHash = errors.New("auth: malformed password hash")

// hasher hashes and verifies argon2id passwords. Each hash allocates
// MemoryKiB, so a semaphore bounds how many run at once and a burst of
// logins cannot exhaust memory on a small self-host.
type hasher struct {
	params HashParams
	slots  chan struct{}
}

func newHasher(p HashParams, concurrency int) *hasher {
	if concurrency < 1 {
		concurrency = 1
	}
	return &hasher{params: p, slots: make(chan struct{}, concurrency)}
}

func (h *hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for password hashing: %w", ctx.Err())
	}
}

func (h *hasher) release() { <-h.slots }

// Hash returns a PHC string: $argon2id$v=19$m=...,t=...,p=...$salt$key.
func (h *hasher) Hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password salt: %w", err)
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	p := h.params
	key := argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoryKiB, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether password matches the PHC hash. The key comparison
// takes constant time.
func (h *hasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, uint32(len(want))) //nolint:gosec // G115: len(want) is checked in decodeHash
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func decodeHash(encoded string) (HashParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return HashParams{}, nil, nil, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return HashParams{}, nil, nil, errMalformedHash
	}
	var p HashParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Time, &p.Threads); err != nil {
		return HashParams{}, nil, nil, errMalformedHash
	}
	if p.MemoryKiB == 0 || p.MemoryKiB > maxMemoryKiB || p.Time == 0 || p.Time > maxTime || p.Threads == 0 {
		return HashParams{}, nil, nil, errMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return HashParams{}, nil, nil, errMalformedHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return HashParams{}, nil, nil, errMalformedHash
	}
	return p, salt, key, nil
}

// validatePassword checks the length policy (NIST SP 800-63B: a minimum
// length and no composition rules).
func validatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLen || n > MaxPasswordLen {
		return fmt.Errorf("%w: password must be %d-%d characters", ErrInvalid, MinPasswordLen, MaxPasswordLen)
	}
	return nil
}
