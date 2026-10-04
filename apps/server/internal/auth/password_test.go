package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var cheap = HashParams{MemoryKiB: 64, Time: 1, Threads: 1}

func TestHasher_HashAndVerify(t *testing.T) {
	h := newHasher(cheap, 2)
	ctx := context.Background()
	enc, err := h.Hash(ctx, "correct horse")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("unexpected PHC string: %s", enc)
	}
	if ok, err := h.Verify(ctx, "correct horse", enc); err != nil || !ok {
		t.Fatalf("right password: %v %v", ok, err)
	}
	if ok, err := h.Verify(ctx, "correct horse!", enc); err != nil || ok {
		t.Fatalf("wrong password must not match: %v %v", ok, err)
	}
	again, _ := h.Hash(ctx, "correct horse")
	if again == enc {
		t.Fatal("each hash must use a fresh salt")
	}
}

// A hash made with other parameters still verifies: parameters are read
// from the stored string, so raising the defaults does not lock users out.
func TestHasher_VerifiesHashWithOtherParams(t *testing.T) {
	old, _ := newHasher(HashParams{MemoryKiB: 128, Time: 2, Threads: 2}, 1).Hash(context.Background(), "pw-123456")
	if ok, err := newHasher(cheap, 1).Verify(context.Background(), "pw-123456", old); err != nil || !ok {
		t.Fatalf("got %v %v", ok, err)
	}
}

func TestHasher_RejectsMalformedHashes(t *testing.T) {
	h := newHasher(cheap, 1)
	valid, _ := h.Hash(context.Background(), "x")
	parts := strings.Split(valid, "$")
	tests := map[string]string{
		"empty":         "",
		"bcrypt":        "$2a$10$abcdefghijklmnopqrstuu",
		"argon2i":       strings.Replace(valid, "argon2id", "argon2i", 1),
		"old version":   strings.Replace(valid, "v=19", "v=16", 1),
		"huge memory":   strings.Replace(valid, "m=64", "m=4194304", 1),
		"zero time":     strings.Replace(valid, "t=1", "t=0", 1),
		"bad salt":      strings.Join([]string{"", parts[1], parts[2], parts[3], "!!", parts[5]}, "$"),
		"short key":     strings.Join([]string{"", parts[1], parts[2], parts[3], parts[4], "AAAA"}, "$"),
		"missing parts": strings.Join(parts[:5], "$"),
	}
	for name, enc := range tests {
		t.Run(name, func(t *testing.T) {
			if ok, err := h.Verify(context.Background(), "x", enc); !errors.Is(err, errMalformedHash) || ok {
				t.Fatalf("want errMalformedHash, got %v %v", ok, err)
			}
		})
	}
}

func TestHasher_WaitRespectsContext(t *testing.T) {
	h := newHasher(cheap, 1)
	h.slots <- struct{}{} // the only slot is busy
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Hash(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestValidatePassword(t *testing.T) {
	tests := map[string]bool{
		"1234567":                false,
		"12345678":               true,
		"пароль12":               true, // 8 runes, 16 bytes
		strings.Repeat("a", 128): true,
		strings.Repeat("a", 129): false,
		strings.Repeat("я", 128): true,
		"":                       false,
		"        ":               true, // spaces are allowed, as NIST recommends
		"with emoji 🙂 inside ok": true,
	}
	for pw, ok := range tests {
		if err := validatePassword(pw); (err == nil) != ok {
			t.Errorf("validatePassword(%q) = %v, want ok=%v", pw, err, ok)
		}
	}
}
