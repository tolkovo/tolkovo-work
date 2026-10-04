package crypto

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func testKey(b byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, KeySize))
}

func newSealer(t *testing.T, current string, version uint32, retired string) *Sealer {
	t.Helper()
	keys, err := ParseLocalKeys(current, version, retired)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	s, err := NewSealer(keys)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSealOpen_RoundTrip(t *testing.T) {
	s := newSealer(t, testKey(1), 1, "")
	ctx := context.Background()
	aad := AAD{WorkspaceID: uuid.New(), SecretID: uuid.New()}
	token := "123456:AAE-bot-token-value"

	sealed, err := s.Seal(ctx, []byte(token), aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(token)) {
		t.Fatal("sealed value contains the plaintext")
	}
	again, _ := s.Seal(ctx, []byte(token), aad)
	if bytes.Equal(sealed, again) {
		t.Fatal("sealing twice must give different values (fresh DEK and nonces)")
	}
	got, err := s.Open(ctx, sealed, aad)
	if err != nil || got.Reveal() != token {
		t.Fatalf("open: %q %v", got.Reveal(), err)
	}
	empty, err := s.Seal(ctx, nil, aad)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Open(ctx, empty, aad); err != nil || !got.Empty() {
		t.Fatalf("empty value: %v %v", got.Empty(), err)
	}
}

func TestOpen_FailsOutsideItsPlace(t *testing.T) {
	s := newSealer(t, testKey(1), 1, "")
	ctx := context.Background()
	aad := AAD{WorkspaceID: uuid.New(), SecretID: uuid.New()}
	sealed, err := s.Seal(ctx, []byte("secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]AAD{
		"another workspace": {WorkspaceID: uuid.New(), SecretID: aad.SecretID},
		"another row":       {WorkspaceID: aad.WorkspaceID, SecretID: uuid.New()},
	}
	for name, other := range cases {
		if _, err := s.Open(ctx, sealed, other); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: want ErrDecrypt, got %v", name, err)
		}
		if _, err := s.Rewrap(ctx, sealed, other); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: rewrap must fail too, got %v", name, err)
		}
	}
	if _, err := s.Seal(ctx, []byte("x"), AAD{WorkspaceID: aad.WorkspaceID}); err == nil {
		t.Error("sealing without a secret ID must fail")
	}
}

func TestOpen_RejectsTamperingAndOtherKeys(t *testing.T) {
	s := newSealer(t, testKey(1), 1, "")
	ctx := context.Background()
	aad := AAD{WorkspaceID: uuid.New(), SecretID: uuid.New()}
	sealed, _ := s.Seal(ctx, []byte("secret"), aad)

	for i := range sealed {
		damaged := bytes.Clone(sealed)
		damaged[i] ^= 0x01
		if _, err := s.Open(ctx, damaged, aad); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("flipping byte %d: want ErrDecrypt, got %v", i, err)
		}
	}
	for _, short := range [][]byte{nil, {formatV1}, sealed[:len(sealed)-1], sealed[:10]} {
		if _, err := s.Open(ctx, short, aad); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("truncated %d bytes: want ErrDecrypt, got %v", len(short), err)
		}
	}
	other := newSealer(t, testKey(2), 1, "")
	if _, err := other.Open(ctx, sealed, aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("another master key: want ErrDecrypt, got %v", err)
	}
}

func TestRotation_RetiredKeyOpensAndRewraps(t *testing.T) {
	ctx := context.Background()
	aad := AAD{WorkspaceID: uuid.New(), SecretID: uuid.New()}
	old := newSealer(t, testKey(1), 1, "")
	sealed, _ := old.Seal(ctx, []byte("secret"), aad)

	rotated := newSealer(t, testKey(2), 2, "1:"+testKey(1))
	if !rotated.NeedsRewrap(sealed) {
		t.Fatal("a value under key 1 needs a rewrap when key 2 is current")
	}
	if got, err := rotated.Open(ctx, sealed, aad); err != nil || got.Reveal() != "secret" {
		t.Fatalf("retired key must still open: %v", err)
	}
	rewrapped, err := rotated.Rewrap(ctx, sealed, aad)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.NeedsRewrap(rewrapped) {
		t.Fatal("rewrapped value must use the current key")
	}
	// Once key 1 is dropped, only the rewrapped value opens.
	onlyNew := newSealer(t, testKey(2), 2, "")
	if _, err := onlyNew.Open(ctx, sealed, aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("value under a dropped key: want ErrDecrypt, got %v", err)
	}
	if got, err := onlyNew.Open(ctx, rewrapped, aad); err != nil || got.Reveal() != "secret" {
		t.Fatalf("rewrapped value: %v", err)
	}
}

func TestParseLocalKeys_Errors(t *testing.T) {
	cases := []struct {
		name, current, retired string
		version                uint32
	}{
		{"empty key", "", "", 1},
		{"not base64", "%%%", "", 1},
		{"short key", base64.StdEncoding.EncodeToString([]byte("short")), "", 1},
		{"version zero", testKey(1), "", 0},
		{"retired without version", testKey(1), testKey(2), 2},
		{"retired version zero", testKey(1), "0:" + testKey(2), 2},
		{"duplicate version", testKey(1), "1:" + testKey(2), 1},
		{"bad retired key", testKey(1), "1:abc", 2},
	}
	for _, tc := range cases {
		_, err := ParseLocalKeys(tc.current, tc.version, tc.retired)
		if err == nil {
			t.Errorf("%s: want an error", tc.name)
			continue
		}
		if strings.Contains(err.Error(), testKey(1)) || strings.Contains(err.Error(), testKey(2)) {
			t.Errorf("%s: error leaks key material: %v", tc.name, err)
		}
	}
	if _, err := ParseLocalKeys(testKey(3), 3, " 1:"+testKey(1)+" , 2:"+testKey(2)+","); err != nil {
		t.Fatalf("valid keyring: %v", err)
	}
}

func TestGenerateKey(t *testing.T) {
	a, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateKey()
	if a == b {
		t.Fatal("keys must be random")
	}
	if _, err := ParseLocalKeys(a, 1, ""); err != nil {
		t.Fatalf("a generated key must be a valid master key: %v", err)
	}
}

func TestSecret_NeverPrints(t *testing.T) {
	s := NewSecret("sk-live-very-secret-1234")
	type holder struct {
		Token Secret `json:"token"`
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("x", "token", s, "holder", holder{s})
	j, _ := json.Marshal(holder{s})
	outputs := []string{
		fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s %q %x %d %08b", s, s, s, s, s, s, s, s),
		fmt.Sprintf("%+v %#v", holder{s}, holder{s}), logs.String(), string(j),
	}
	for _, out := range outputs {
		if strings.Contains(out, "very-secret") || !strings.Contains(out, redacted) {
			t.Fatalf("secret leaked or not redacted: %s", out)
		}
	}
	if s.Reveal() != "sk-live-very-secret-1234" {
		t.Fatal("Reveal must return the value")
	}
	if got := s.Mask(); got != "…1234" {
		t.Fatalf("mask: %q", got)
	}
	if got := NewSecret("short").Mask(); got != "…" {
		t.Fatalf("short values are fully hidden: %q", got)
	}
}
