package storage

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var testWS = uuid.MustParse("0192f0a0-0000-7000-8000-000000000001")

func TestNewKey_RoundTripsThroughParseKey(t *testing.T) {
	key, err := NewKey(testWS, KindReports)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	k, err := ParseKey(key)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", key, err)
	}
	if k.WorkspaceID != testWS || k.Kind != KindReports || k.ID.Version() != 4 || k.String() != key {
		t.Fatalf("unexpected key %+v from %q", k, key)
	}
}

func TestNewKey_RejectsNilWorkspaceAndUnknownKind(t *testing.T) {
	if _, err := NewKey(uuid.Nil, KindConversation); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil workspace: want ErrInvalidKey, got %v", err)
	}
	if _, err := NewKey(testWS, Kind("../etc")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("unknown kind: want ErrInvalidKey, got %v", err)
	}
}

func TestParseKey_RejectsNonCanonicalAndTraversal(t *testing.T) {
	ws := testWS.String()
	id := uuid.NewString()
	bad := []string{
		"",
		"ws",
		"ws/" + ws + "/conv",
		"ws/" + ws + "/conv/" + id + "/extra",
		"/ws/" + ws + "/conv/" + id,
		"ws/" + ws + "/conv/" + id + "/",
		"ws/" + ws + "/conv/..",
		"ws/" + ws + "/../" + id,
		"ws/../../etc/passwd",
		"ws/" + ws + "/conv/../../" + id,
		"ws/" + ws + "/conv/" + strings.ToUpper(id),
		"ws/{" + ws + "}/conv/" + id,
		"ws/urn:uuid:" + ws + "/conv/" + id,
		"ws/" + strings.ReplaceAll(ws, "-", "") + "/conv/" + id,
		"ws/" + uuid.Nil.String() + "/conv/" + id,
		"ws/" + ws + "/other/" + id,
		"ws\\" + ws + "\\conv\\" + id,
		"WS/" + ws + "/conv/" + id,
		"ws/" + ws + "/conv/" + id + "\x00",
	}
	for _, key := range bad {
		if _, err := ParseKey(key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("ParseKey(%q): want ErrInvalidKey, got %v", key, err)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct{ in, want string }{
		{"report.log", "report.log"},
		{"../../etc/passwd", "passwd"},
		{`C:\Users\x\crash.dmp`, "crash.dmp"},
		{"a\r\nX-Evil: 1.txt", "aX-Evil: 1.txt"},
		{"evil\u202egpj.exe", "evilgpj.exe"},
		{`say "hi".txt`, "say hi.txt"},
		{"", "file"},
		{"..", "file"},
		{"   ", "file"},
		{"отчёт.txt", "отчёт.txt"},
		{"bad\xffutf8.txt", "badutf8.txt"},
	}
	for _, tt := range tests {
		if got := SanitizeFilename(tt.in); got != tt.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	long := strings.Repeat("ж", 200) // 400 bytes
	if got := SanitizeFilename(long); len(got) > maxFilenameBytes || !strings.HasPrefix(long, got) {
		t.Errorf("long name not truncated on a rune boundary: %d bytes", len(got))
	}
}

func TestContentDisposition_IsAttachmentAndEncodesUnicode(t *testing.T) {
	if got := ContentDisposition("log.txt"); got != `attachment; filename=log.txt` {
		t.Fatalf("ascii: %q", got)
	}
	got := ContentDisposition("отчёт.txt")
	if !strings.HasPrefix(got, "attachment; filename*=utf-8''") || strings.ContainsAny(got, "\r\n") {
		t.Fatalf("unicode: %q", got)
	}
}

func TestValidateContentType_RejectsHeaderInjection(t *testing.T) {
	for _, ct := range []string{"text/plain\r\nX-Evil: 1", "not a type", "text/plain; charset=\x01"} {
		if err := validateContentType(ct); !errors.Is(err, ErrInvalidOptions) {
			t.Errorf("%q: want ErrInvalidOptions, got %v", ct, err)
		}
	}
	for _, ct := range []string{"", "text/plain", "application/json; charset=utf-8"} {
		if err := validateContentType(ct); err != nil {
			t.Errorf("%q: %v", ct, err)
		}
	}
}
