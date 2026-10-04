package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

var loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}

func get(t *testing.T, c *http.Client, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(req)
}

// getErr makes a request that is expected to fail and returns its error.
func getErr(t *testing.T, c *http.Client, url string) error {
	t.Helper()
	resp, err := get(t, c, url)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

func TestBlocked(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1",
		"0.0.0.0", "0.1.2.3", "224.0.0.1", "255.255.255.255", "198.18.0.1", "192.0.2.10",
		"::1", "::", "fe80::1", "fc00::1", "fd12::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
		"64:ff9b::a00:1", "2001:db8::1", "2002:a00:1::",
	}
	for _, s := range blocked {
		if !Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	public := []string{"1.1.1.1", "8.8.8.8", "140.82.112.3", "2606:4700:4700::1111", "::ffff:1.1.1.1"}
	for _, s := range public {
		if Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
}

func TestSafeClient_RefusesLoopbackAfterResolution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()
	c := NewSafeClient(Options{})
	// By IP and by a name that resolves to it: the check sees the dialled address.
	for _, url := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
		if err := getErr(t, c, url); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: want ErrBlockedAddress, got %v", url, err)
		}
	}
}

func TestSafeClient_ExplicitExceptionOpensARange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()
	resp, err := get(t, NewSafeClient(Options{AllowPrivate: loopback}), srv.URL)
	if err != nil {
		t.Fatalf("allowed range: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if b, _ := io.ReadAll(resp.Body); string(b) != "ok" {
		t.Fatalf("body %q", b)
	}
}

func TestSafeClient_RedirectToMetadataIsBlockedEvenWithExceptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	allowAll := append([]netip.Prefix{netip.MustParsePrefix("169.254.0.0/16")}, loopback...)
	c := NewSafeClient(Options{AllowPrivate: allowAll, Timeout: 5 * time.Second})
	if err := getErr(t, c, srv.URL); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("want ErrBlockedAddress, got %v", err)
	}
}

func TestSafeClient_LimitsRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()
	if err := getErr(t, NewSafeClient(Options{AllowPrivate: loopback, MaxRedirects: 2}), srv.URL); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("want ErrTooManyRedirects, got %v", err)
	}
	if err := getErr(t, NewSafeClient(Options{AllowPrivate: loopback, MaxRedirects: -1}), srv.URL); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("no redirects allowed: want ErrTooManyRedirects, got %v", err)
	}
}

func TestSafeClient_LimitsResponseSize(t *testing.T) {
	big := strings.Repeat("x", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chunked" {
			w.(http.Flusher).Flush() // no Content-Length: the limit must hold while reading
		}
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()
	c := NewSafeClient(Options{AllowPrivate: loopback, MaxResponseBytes: 100})

	if err := getErr(t, c, srv.URL); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("declared length: want ErrResponseTooLarge, got %v", err)
	}
	resp, err := get(t, c, srv.URL+"/chunked")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if !errors.Is(err, ErrResponseTooLarge) || len(b) != 100 {
		t.Fatalf("streamed body: read %d bytes, err %v", len(b), err)
	}

	exact := NewSafeClient(Options{AllowPrivate: loopback, MaxResponseBytes: 1000})
	resp, err = get(t, exact, srv.URL+"/chunked")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if b, err := io.ReadAll(resp.Body); err != nil || len(b) != 1000 {
		t.Fatalf("a body of exactly the limit must pass: %d %v", len(b), err)
	}
}

func TestSafeClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	start := time.Now()
	if err := getErr(t, NewSafeClient(Options{AllowPrivate: loopback, Timeout: 200 * time.Millisecond}), srv.URL); err == nil {
		t.Fatal("want a timeout")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout was not applied")
	}
}

func TestValidateURL(t *testing.T) {
	ok := []string{"https://api.openai.com/v1", "http://ollama.internal:11434", "https://[2606:4700::1111]/"}
	for _, u := range ok {
		if err := ValidateURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	invalid := []string{"", "ftp://x", "/relative", "https://user:pass@example.com", "https://", "javascript:alert(1)"}
	for _, u := range invalid {
		if err := ValidateURL(u); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("%q: want ErrInvalidURL, got %v", u, err)
		}
	}
	for _, u := range []string{"http://127.0.0.1:11434", "http://[::1]/", "http://169.254.169.254/"} {
		if err := ValidateURL(u); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: want ErrBlockedAddress, got %v", u, err)
		}
	}
	if err := ValidateURLAllowing("http://127.0.0.1:11434", loopback); err != nil {
		t.Errorf("allowed loopback: %v", err)
	}
}
