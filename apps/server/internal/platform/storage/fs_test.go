package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestFS(t *testing.T) (*FS, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "files")
	s, err := NewFS(FSOptions{
		Root:        dir,
		LinkBaseURL: "https://support.example.com/api/v1/files",
		SigningKey:  []byte(strings.Repeat("k", MinSigningKeyLen)),
		Now:         func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func newTestKey(t *testing.T) string {
	t.Helper()
	key, err := NewKey(testWS, KindConversation)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestFS_PutOpenStatDelete(t *testing.T) {
	ctx := context.Background()
	s, dir := newTestFS(t)
	key := newTestKey(t)
	body := "hello, world"
	sum := sha256.Sum256([]byte(body))

	obj, err := s.Put(ctx, key, strings.NewReader(body), PutOptions{MaxSize: 100, Size: int64(len(body)), SHA256: sum[:]})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Size != int64(len(body)) || obj.Key != key {
		t.Fatalf("Put returned %+v", obj)
	}

	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(key)))
	if err != nil {
		t.Fatalf("file not at key path: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fsFilePerm {
		t.Fatalf("file perm %o, want %o", perm, fsFilePerm)
	}
	if dinfo, _ := os.Stat(filepath.Dir(filepath.Join(dir, filepath.FromSlash(key)))); dinfo.Mode().Perm() != fsDirPerm {
		t.Fatalf("dir perm %o, want %o", dinfo.Mode().Perm(), fsDirPerm)
	}
	assertNoTempFiles(t, dir)

	rc, got, err := s.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(data) != body || got.Size != int64(len(body)) {
		t.Fatalf("Open: %q %+v", data, got)
	}
	if st, err := s.Stat(ctx, key); err != nil || st.Size != int64(len(body)) {
		t.Fatalf("Stat: %+v %v", st, err)
	}

	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat after delete: want ErrNotFound, got %v", err)
	}
	if _, _, err := s.Open(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open after delete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("second Delete must be a no-op, got %v", err)
	}
}

func TestFS_PutRefusesOverwrite(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestFS(t)
	key := newTestKey(t)
	if _, err := s.Put(ctx, key, strings.NewReader("first"), PutOptions{MaxSize: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, key, strings.NewReader("second"), PutOptions{MaxSize: 10}); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	rc, _, _ := s.Open(ctx, key)
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(data) != "first" {
		t.Fatalf("object was overwritten: %q", data)
	}
}

func TestFS_PutEnforcesLimitsAndLeavesNothing(t *testing.T) {
	ctx := context.Background()
	other := sha256.Sum256([]byte("other"))
	tests := []struct {
		name string
		body string
		opts PutOptions
		want error
	}{
		{"over max size", "12345678901", PutOptions{MaxSize: 10}, ErrTooLarge},
		{"shorter than size", "123", PutOptions{MaxSize: 10, Size: 5}, ErrSizeMismatch},
		{"longer than size", "123456", PutOptions{MaxSize: 10, Size: 5}, ErrSizeMismatch},
		{"wrong checksum", "hello", PutOptions{MaxSize: 10, SHA256: other[:]}, ErrChecksumMismatch},
		{"no max size", "x", PutOptions{}, ErrInvalidOptions},
		{"bad content type", "x", PutOptions{MaxSize: 10, ContentType: "text/plain\r\nX: y"}, ErrInvalidOptions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newTestFS(t)
			key := newTestKey(t)
			if _, err := s.Put(ctx, key, strings.NewReader(tt.body), tt.opts); !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
			if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
				t.Fatalf("object must not exist after a failed Put, Stat: %v", err)
			}
			assertNoTempFiles(t, dir)
		})
	}
	t.Run("exactly max size", func(t *testing.T) {
		s, _ := newTestFS(t)
		if _, err := s.Put(ctx, newTestKey(t), strings.NewReader("1234567890"), PutOptions{MaxSize: 10}); err != nil {
			t.Fatalf("want success at the limit, got %v", err)
		}
	})
}

func TestFS_PutStopsOnCancelledContext(t *testing.T) {
	s, dir := newTestFS(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, newTestKey(t), strings.NewReader("data"), PutOptions{MaxSize: 10}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	assertNoTempFiles(t, dir)
}

func TestFS_RejectsTraversalKeys(t *testing.T) {
	ctx := context.Background()
	s, dir := newTestFS(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../outside.txt", "ws/../../outside.txt", "/etc/passwd", "ws/" + testWS.String() + "/conv/.."} {
		if _, err := s.Put(ctx, key, strings.NewReader("x"), PutOptions{MaxSize: 10}); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Put(%q): want ErrInvalidKey, got %v", key, err)
		}
		if _, _, err := s.Open(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Open(%q): want ErrInvalidKey, got %v", key, err)
		}
		if _, err := s.Stat(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Stat(%q): want ErrInvalidKey, got %v", key, err)
		}
		if err := s.Delete(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Delete(%q): want ErrInvalidKey, got %v", key, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("file outside the root was touched: %v", err)
	}
}

// A symlink planted inside the root must not lead outside it: os.Root
// refuses to follow it even for a valid key.
func TestFS_DoesNotFollowSymlinkOutOfRoot(t *testing.T) {
	ctx := context.Background()
	s, dir := newTestFS(t)
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "x"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := newTestKey(t)
	k, _ := ParseKey(key)
	wsDir := filepath.Join(dir, "ws", testWS.String())
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(wsDir, string(k.Kind))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, key, strings.NewReader("x"), PutOptions{MaxSize: 10}); err == nil {
		t.Fatalf("Put through a symlink out of the root must fail")
	}
	if entries, _ := os.ReadDir(outsideDir); len(entries) != 1 {
		t.Fatalf("something was written outside the root: %v", entries)
	}
}

func TestFS_PresignPutProducesVerifiableLink(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestFS(t)
	key := newTestKey(t)
	req, err := s.PresignPut(ctx, key, PresignPutOptions{ContentType: "text/plain", MaxSize: 100, Size: 5})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPut || u.Host != "support.example.com" || u.Path != "/api/v1/files/"+key {
		t.Fatalf("unexpected link %s %s", req.Method, req.URL)
	}
	if req.Header.Get("Content-Type") != "text/plain" || !req.ExpiresAt.Equal(testNow.Add(DefaultURLTTL)) {
		t.Fatalf("unexpected header or expiry: %v %v", req.Header, req.ExpiresAt)
	}
	g, err := s.VerifyLink(http.MethodPut, key, u.Query())
	if err != nil {
		t.Fatalf("VerifyLink: %v", err)
	}
	if g.MaxSize != 100 || g.Size != 5 || g.ContentType != "text/plain" {
		t.Fatalf("grant %+v", g)
	}
}

func TestFS_PresignGetSanitizesFilename(t *testing.T) {
	s, _ := newTestFS(t)
	key := newTestKey(t)
	req, err := s.PresignGet(context.Background(), key, PresignGetOptions{Filename: "../../evil\r\n.log", TTL: time.Minute})
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	u, _ := url.Parse(req.URL)
	g, err := s.VerifyLink(http.MethodGet, key, u.Query())
	if err != nil {
		t.Fatalf("VerifyLink: %v", err)
	}
	if g.Filename != "evil.log" || !g.Expires.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("grant %+v", g)
	}
}

func TestNewFS_RejectsWeakConfig(t *testing.T) {
	base := FSOptions{Root: t.TempDir(), LinkBaseURL: "https://x.example/api/v1/files", SigningKey: []byte(strings.Repeat("k", 32))}
	cases := map[string]func(o *FSOptions){
		"short key":    func(o *FSOptions) { o.SigningKey = []byte("short") },
		"no root":      func(o *FSOptions) { o.Root = "" },
		"relative url": func(o *FSOptions) { o.LinkBaseURL = "/api/v1/files" },
		"url w/ query": func(o *FSOptions) { o.LinkBaseURL = "https://x.example/f?a=b" },
		"huge ttl":     func(o *FSOptions) { o.TTL = 30 * 24 * time.Hour },
	}
	for name, mutate := range cases {
		o := base
		mutate(&o)
		if _, err := NewFS(o); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(d.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
}
