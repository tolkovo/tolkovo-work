// Package httpx makes outgoing HTTP requests to addresses that users
// configure: webhooks, the base_url of an OpenAI-compatible provider, the
// GitHub API, knowledge base imports (docs/00 §9.5, docs/06 S-02).
//
// SafeClient refuses private, loopback, link-local and other special
// addresses. The check runs in net.Dialer.Control, on the address actually
// dialled after DNS resolution and for every redirect, so DNS rebinding and
// redirects to internal hosts do not get through. Proxies from the
// environment are ignored: through a proxy the check would see only the
// proxy's address.
package httpx

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// Errors of SafeClient. They wrap into the *url.Error that http.Client
// returns, so check them with errors.Is.
var (
	ErrBlockedAddress   = errors.New("httpx: destination address is not allowed")
	ErrResponseTooLarge = errors.New("httpx: response body is too large")
	ErrTooManyRedirects = errors.New("httpx: too many redirects")
	ErrInvalidURL       = errors.New("httpx: URL must be absolute http(s) without credentials")
)

// Defaults of Options.
const (
	DefaultTimeout          = 30 * time.Second
	DefaultMaxRedirects     = 3
	DefaultMaxResponseBytes = 10 << 20
)

// Options configure NewSafeClient. Zero values take the defaults.
type Options struct {
	// Timeout bounds the whole request, body included.
	Timeout time.Duration
	// MaxRedirects is how many redirects are followed; negative: none.
	MaxRedirects int
	// MaxResponseBytes bounds the response body; reading past it fails
	// with ErrResponseTooLarge.
	MaxResponseBytes int64
	// AllowPrivate opens these ranges despite the block list: a self-host
	// operator's explicit exception for a local Ollama or vLLM
	// (TOLKOVO_EGRESS_ALLOW_PRIVATE). Link-local addresses (cloud metadata)
	// stay closed even if listed.
	AllowPrivate []netip.Prefix
}

// NewSafeClient returns an http.Client that enforces the rules above.
func NewSafeClient(o Options) *http.Client {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = DefaultMaxRedirects
	}
	if o.MaxResponseBytes <= 0 {
		o.MaxResponseBytes = DefaultMaxResponseBytes
	}
	allow := append([]netip.Prefix(nil), o.AllowPrivate...)
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			return checkDialAddress(address, allow)
		},
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           50,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  o.Timeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Timeout:   o.Timeout,
		Transport: &limitTransport{next: transport, max: o.MaxResponseBytes},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if o.MaxRedirects < 0 || len(via) > o.MaxRedirects {
				return ErrTooManyRedirects
			}
			return validateURL(req.URL.String(), allow)
		},
	}
}

// ValidateURL checks a user-supplied URL before it is saved: absolute
// http or https, a host, no user:password (it would end up in logs). A
// literal IP is checked against the block list too; host names are checked
// when dialled.
func ValidateURL(raw string) error {
	return validateURL(raw, nil)
}

// ValidateURLAllowing is ValidateURL with the operator's exceptions.
func ValidateURLAllowing(raw string, allow []netip.Prefix) error {
	return validateURL(raw, allow)
}

func validateURL(raw string, allow []netip.Prefix) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ErrInvalidURL
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !allowed(ip, allow) {
		return ErrBlockedAddress
	}
	return nil
}

func checkDialAddress(address string, allow []netip.Prefix) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return ErrBlockedAddress // not an IP:port: nothing to check, so refuse
	}
	if !allowed(ap.Addr(), allow) {
		return ErrBlockedAddress
	}
	return nil
}

// allowed reports whether ip may be dialled.
func allowed(ip netip.Addr, allow []netip.Prefix) bool {
	ip = ip.Unmap()
	if !Blocked(ip) {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false // cloud metadata (169.254.169.254) is never an exception
	}
	for _, p := range allow {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// blockedPrefixes are special-purpose ranges (RFC 6890 and successors)
// besides what netip classifies itself.
var blockedPrefixes = func() []netip.Prefix {
	list := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16",
	}
	out := make([]netip.Prefix, len(list))
	for i, s := range list {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}()

// Blocked reports whether ip is private, loopback, link-local, multicast,
// unspecified or otherwise not a public unicast address.
func Blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// limitTransport caps response bodies.
type limitTransport struct {
	next http.RoundTripper
	max  int64
}

func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > t.max {
		_ = resp.Body.Close()
		return nil, ErrResponseTooLarge
	}
	resp.Body = &limitedBody{rc: resp.Body, left: t.max}
	return resp, nil
}

// limitedBody fails once more than its limit is read, instead of silently
// truncating like io.LimitReader.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrResponseTooLarge
	}
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n - int(-b.left), ErrResponseTooLarge
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }
