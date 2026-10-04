package storage

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func testSigner() signer { return signer{key: []byte(strings.Repeat("k", MinSigningKeyLen))} }

func testPutGrant(t *testing.T) Grant {
	t.Helper()
	key, err := NewKey(testWS, KindReports)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	return Grant{
		Method: http.MethodPut, Key: key, Expires: testNow.Add(15 * time.Minute),
		ContentType: "text/plain", MaxSize: 100, Size: 5, SHA256: sum[:],
	}
}

func TestSigner_AcceptsUntamperedLink(t *testing.T) {
	s, g := testSigner(), testPutGrant(t)
	got, err := s.verify(http.MethodPut, g.Key, s.sign(g), testNow)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Key != g.Key || got.MaxSize != 100 || got.Size != 5 || got.ContentType != "text/plain" ||
		string(got.SHA256) != string(g.SHA256) || !got.Expires.Equal(g.Expires) {
		t.Fatalf("grant mismatch: %+v", got)
	}
}

func TestSigner_RejectsExpiredLink(t *testing.T) {
	s, g := testSigner(), testPutGrant(t)
	q := s.sign(g)
	if _, err := s.verify(http.MethodPut, g.Key, q, g.Expires); !errors.Is(err, ErrLinkExpired) {
		t.Fatalf("at expiry: want ErrLinkExpired, got %v", err)
	}
	if _, err := s.verify(http.MethodPut, g.Key, q, g.Expires.Add(-time.Second)); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
}

func TestSigner_RejectsTamperedFields(t *testing.T) {
	s, g := testSigner(), testPutGrant(t)
	other, _ := NewKey(testWS, KindReports)
	tamper := map[string]func(q url.Values){
		"extend expiry":    func(q url.Values) { q.Set(qExpires, "9999999999") },
		"raise max size":   func(q url.Values) { q.Set(qMaxSize, "1000000000") },
		"change size":      func(q url.Values) { q.Set(qSize, "6") },
		"drop size":        func(q url.Values) { q.Del(qSize) },
		"change checksum":  func(q url.Values) { q.Set(qSHA256, strings.Repeat("0", 64)) },
		"drop checksum":    func(q url.Values) { q.Del(qSHA256) },
		"change type":      func(q url.Values) { q.Set(qContentType, "text/html") },
		"drop type":        func(q url.Values) { q.Del(qContentType) },
		"add filename":     func(q url.Values) { q.Set(qFilename, "x.html") },
		"drop signature":   func(q url.Values) { q.Del(qSignature) },
		"garbage sig":      func(q url.Values) { q.Set(qSignature, "!!!") },
		"flip sig byte":    func(q url.Values) { b := []byte(q.Get(qSignature)); b[0] ^= 1; q.Set(qSignature, string(b)) },
		"shift into field": func(q url.Values) { q.Set(qContentType, "text/plain5"); q.Del(qSize) },
	}
	for name, mutate := range tamper {
		t.Run(name, func(t *testing.T) {
			q := s.sign(g)
			mutate(q)
			if _, err := s.verify(http.MethodPut, g.Key, q, testNow); !errors.Is(err, ErrLinkInvalid) {
				t.Fatalf("want ErrLinkInvalid, got %v", err)
			}
		})
	}
	t.Run("other key", func(t *testing.T) {
		if _, err := s.verify(http.MethodPut, other, s.sign(g), testNow); !errors.Is(err, ErrLinkInvalid) {
			t.Fatalf("want ErrLinkInvalid, got %v", err)
		}
	})
	t.Run("other signing key", func(t *testing.T) {
		evil := signer{key: []byte(strings.Repeat("e", MinSigningKeyLen))}
		if _, err := s.verify(http.MethodPut, g.Key, evil.sign(g), testNow); !errors.Is(err, ErrLinkInvalid) {
			t.Fatalf("want ErrLinkInvalid, got %v", err)
		}
	})
}

func TestSigner_RejectsWrongMethod(t *testing.T) {
	s, g := testSigner(), testPutGrant(t)
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, "put"} {
		if _, err := s.verify(m, g.Key, s.sign(g), testNow); !errors.Is(err, ErrLinkInvalid) {
			t.Errorf("PUT link used as %s: want ErrLinkInvalid, got %v", m, err)
		}
	}
	get := Grant{Method: http.MethodGet, Key: g.Key, Expires: g.Expires, Filename: "a.txt"}
	if _, err := s.verify(http.MethodPut, g.Key, s.sign(get), testNow); !errors.Is(err, ErrLinkInvalid) {
		t.Fatalf("GET link used as PUT: want ErrLinkInvalid, got %v", err)
	}
}

func TestSigner_RejectsInvalidKeyBeforeMAC(t *testing.T) {
	s := testSigner()
	g := Grant{Method: http.MethodGet, Key: "ws/../../etc/passwd", Expires: testNow.Add(time.Minute)}
	if _, err := s.verify(http.MethodGet, g.Key, s.sign(g), testNow); !errors.Is(err, ErrLinkInvalid) {
		t.Fatalf("want ErrLinkInvalid, got %v", err)
	}
}
