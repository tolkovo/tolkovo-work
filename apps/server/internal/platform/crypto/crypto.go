// Package crypto encrypts secrets at rest: bot tokens, BYOK keys, webhook
// secrets (docs/02 §13.3, docs/06 S-01). Envelope encryption: every value
// gets its own data key (DEK, AES-256-GCM), and the DEK is wrapped by the
// key-encryption key (KEK) of a KeyProvider. Self-host keeps the KEK in
// TOLKOVO_MASTER_KEY (LocalKeys); SaaS can plug a KMS into KeyProvider.
//
// Both layers authenticate the same associated data, workspace_id and
// secret_id, so a sealed value copied to another workspace or another row
// does not open. Opened values are Secret, which never prints itself.
package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// KeySize is the size of the KEK and of every DEK: AES-256.
const KeySize = 32

// ErrDecrypt means a sealed value does not open: wrong workspace or secret
// ID, a damaged value or an unknown key version. The cause is deliberately
// not detailed.
var ErrDecrypt = errors.New("crypto: cannot decrypt the secret")

// AAD binds a sealed value to its place: the workspace and the row (or any
// stable ID) that holds it. Opening with another AAD fails.
type AAD struct {
	WorkspaceID uuid.UUID
	SecretID    uuid.UUID
}

func (a AAD) bytes() []byte {
	b := make([]byte, 0, 32)
	b = append(b, a.WorkspaceID[:]...)
	return append(b, a.SecretID[:]...)
}

func (a AAD) valid() bool { return a.WorkspaceID != uuid.Nil && a.SecretID != uuid.Nil }

// KeyProvider wraps and unwraps data keys with the KEK. Version names the
// KEK, so values sealed before a rotation still open.
type KeyProvider interface {
	// Wrap encrypts a DEK with the current KEK and returns its version.
	Wrap(ctx context.Context, dek, aad []byte) (wrapped []byte, version uint32, err error)
	// Unwrap decrypts a DEK wrapped by the KEK of that version.
	Unwrap(ctx context.Context, wrapped []byte, version uint32, aad []byte) ([]byte, error)
	// CurrentVersion is the version Wrap uses now.
	CurrentVersion() uint32
}

// Sealer seals and opens secrets.
type Sealer struct {
	keys KeyProvider
}

// NewSealer returns a Sealer over the key provider.
func NewSealer(keys KeyProvider) (*Sealer, error) {
	if keys == nil {
		return nil, errors.New("crypto: a key provider is required")
	}
	return &Sealer{keys: keys}, nil
}

// Sealed layout, one bytea column:
//
//	format (1) | KEK version (4, big endian) | wrapped DEK length (2) |
//	wrapped DEK | nonce (12) | ciphertext with GCM tag
const (
	formatV1  = 1
	headerLen = 1 + 4 + 2
)

// Seal encrypts plaintext for the place named by aad.
func (s *Sealer) Seal(ctx context.Context, plaintext []byte, aad AAD) ([]byte, error) {
	if !aad.valid() {
		return nil, errors.New("crypto: workspace and secret IDs are required")
	}
	dek := make([]byte, KeySize)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("crypto: generate data key: %w", err)
	}
	defer clear(dek)
	ad := aad.bytes()
	wrapped, version, err := s.keys.Wrap(ctx, dek, ad)
	if err != nil {
		return nil, fmt.Errorf("crypto: wrap data key: %w", err)
	}
	if len(wrapped) > 0xFFFF {
		return nil, errors.New("crypto: wrapped data key is too long")
	}
	nonceAndCT, err := gcmSeal(dek, plaintext, ad)
	if err != nil {
		return nil, err
	}
	out := make([]byte, headerLen, headerLen+len(wrapped)+len(nonceAndCT))
	out[0] = formatV1
	binary.BigEndian.PutUint32(out[1:5], version)
	binary.BigEndian.PutUint16(out[5:7], uint16(len(wrapped))) //nolint:gosec // G115: checked above
	out = append(out, wrapped...)
	return append(out, nonceAndCT...), nil
}

// Open decrypts a sealed value; any mismatch is ErrDecrypt.
func (s *Sealer) Open(ctx context.Context, sealed []byte, aad AAD) (Secret, error) {
	version, wrapped, body, err := parse(sealed)
	if err != nil {
		return Secret{}, err
	}
	ad := aad.bytes()
	dek, err := s.keys.Unwrap(ctx, wrapped, version, ad)
	if err != nil {
		return Secret{}, ErrDecrypt
	}
	defer clear(dek)
	plain, err := gcmOpen(dek, body, ad)
	if err != nil {
		return Secret{}, ErrDecrypt
	}
	return Secret{b: plain}, nil
}

// NeedsRewrap reports whether the value was sealed with an older KEK. A
// rotation job rewraps such values; malformed ones report false.
func (s *Sealer) NeedsRewrap(sealed []byte) bool {
	version, _, _, err := parse(sealed)
	return err == nil && version != s.keys.CurrentVersion()
}

// Rewrap re-encrypts only the data key with the current KEK; the value
// itself is not decrypted.
func (s *Sealer) Rewrap(ctx context.Context, sealed []byte, aad AAD) ([]byte, error) {
	version, wrapped, body, err := parse(sealed)
	if err != nil {
		return nil, err
	}
	ad := aad.bytes()
	dek, err := s.keys.Unwrap(ctx, wrapped, version, ad)
	if err != nil {
		return nil, ErrDecrypt
	}
	defer clear(dek)
	// The DEK must still open the value under this AAD, so a rewrap cannot
	// launder a value that belongs elsewhere.
	if _, err := gcmOpen(dek, body, ad); err != nil {
		return nil, ErrDecrypt
	}
	rewrapped, newVersion, err := s.keys.Wrap(ctx, dek, ad)
	if err != nil {
		return nil, fmt.Errorf("crypto: wrap data key: %w", err)
	}
	if len(rewrapped) > 0xFFFF {
		return nil, errors.New("crypto: wrapped data key is too long")
	}
	out := make([]byte, headerLen, headerLen+len(rewrapped)+len(body))
	out[0] = formatV1
	binary.BigEndian.PutUint32(out[1:5], newVersion)
	binary.BigEndian.PutUint16(out[5:7], uint16(len(rewrapped))) //nolint:gosec // G115: checked above
	out = append(out, rewrapped...)
	return append(out, body...), nil
}

func parse(sealed []byte) (version uint32, wrapped, body []byte, err error) {
	if len(sealed) < headerLen || sealed[0] != formatV1 {
		return 0, nil, nil, ErrDecrypt
	}
	version = binary.BigEndian.Uint32(sealed[1:5])
	n := int(binary.BigEndian.Uint16(sealed[5:7]))
	if len(sealed) < headerLen+n+nonceLen+tagLen {
		return 0, nil, nil, ErrDecrypt
	}
	return version, sealed[headerLen : headerLen+n], sealed[headerLen+n:], nil
}

const (
	nonceLen = 12
	tagLen   = 16
)

// gcmSeal returns nonce || ciphertext || tag under a fresh random nonce.
// Every key seals few values (one per DEK, one DEK per KEK wrap), far below
// the random-nonce limit of AES-GCM.
func gcmSeal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen, nonceLen+len(plaintext)+tagLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: generate nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

func gcmOpen(key, nonceAndCT, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonceAndCT) < nonceLen+tagLen {
		return nil, ErrDecrypt
	}
	return aead.Open(nil, nonceAndCT[:nonceLen], nonceAndCT[nonceLen:], aad)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes", KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	return cipher.NewGCM(block)
}

// GenerateKey returns a new random KEK, base64 encoded, for `tolkovo keygen`.
func GenerateKey() (string, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("crypto: generate key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
