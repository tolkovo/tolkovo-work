package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// MinSigningKeyLen is the shortest accepted HMAC key for FS links.
const MinSigningKeyLen = 32

// Query parameters of an FS link. Short names keep URLs readable.
const (
	qExpires     = "exp"
	qSize        = "size"
	qMaxSize     = "max"
	qContentType = "type"
	qSHA256      = "sha256"
	qFilename    = "name"
	qSignature   = "sig"
)

// signatureVersion is mixed into every MAC so a future format cannot be
// confused with this one.
const signatureVersion = "tolkovo-storage-link-v1"

// Grant is what a verified FS link allows: one method on one key until
// Expires, with the limits that were signed.
type Grant struct {
	Method      string
	Key         string
	Expires     time.Time
	ContentType string
	MaxSize     int64 // PUT only
	Size        int64 // PUT only, 0 when not fixed
	SHA256      []byte
	Filename    string // GET only, already sanitized
}

// signer issues and verifies HMAC-SHA256 links. The MAC covers the method,
// the key, the expiry and every limit, so changing any of them, or replaying
// a GET link as PUT, invalidates the link.
type signer struct {
	key []byte
}

// sign returns the query string for a grant.
func (s signer) sign(g Grant) url.Values {
	q := url.Values{}
	q.Set(qExpires, strconv.FormatInt(g.Expires.Unix(), 10))
	if g.Method == http.MethodPut {
		q.Set(qMaxSize, strconv.FormatInt(g.MaxSize, 10))
		if g.Size > 0 {
			q.Set(qSize, strconv.FormatInt(g.Size, 10))
		}
		if len(g.SHA256) > 0 {
			q.Set(qSHA256, hex.EncodeToString(g.SHA256))
		}
	}
	if g.ContentType != "" {
		q.Set(qContentType, g.ContentType)
	}
	if g.Filename != "" {
		q.Set(qFilename, g.Filename)
	}
	q.Set(qSignature, base64.RawURLEncoding.EncodeToString(s.mac(g.Method, g.Key, q)))
	return q
}

// verify checks the MAC first, in constant time, and only then trusts and
// parses the parameters.
func (s signer) verify(method, key string, q url.Values, now time.Time) (Grant, error) {
	if method != http.MethodGet && method != http.MethodPut {
		return Grant{}, ErrLinkInvalid
	}
	if _, err := ParseKey(key); err != nil {
		return Grant{}, ErrLinkInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(q.Get(qSignature))
	if err != nil || !hmac.Equal(got, s.mac(method, key, q)) {
		return Grant{}, ErrLinkInvalid
	}
	g, err := parseGrant(method, key, q)
	if err != nil {
		return Grant{}, ErrLinkInvalid
	}
	if !now.Before(g.Expires) {
		return Grant{}, ErrLinkExpired
	}
	return g, nil
}

func parseGrant(method, key string, q url.Values) (Grant, error) {
	exp, err := strconv.ParseInt(q.Get(qExpires), 10, 64)
	if err != nil {
		return Grant{}, err
	}
	g := Grant{
		Method:      method,
		Key:         key,
		Expires:     time.Unix(exp, 0),
		ContentType: q.Get(qContentType),
		Filename:    q.Get(qFilename),
	}
	if method == http.MethodGet {
		return g, nil
	}
	if g.MaxSize, err = strconv.ParseInt(q.Get(qMaxSize), 10, 64); err != nil {
		return Grant{}, err
	}
	if v := q.Get(qSize); v != "" {
		if g.Size, err = strconv.ParseInt(v, 10, 64); err != nil {
			return Grant{}, err
		}
	}
	if v := q.Get(qSHA256); v != "" {
		if g.SHA256, err = hex.DecodeString(v); err != nil {
			return Grant{}, err
		}
	}
	if err := validateLimits(g.ContentType, g.MaxSize, g.Size, g.SHA256); err != nil {
		return Grant{}, errors.New("signed limits are inconsistent")
	}
	return g, nil
}

// mac authenticates the exact strings of the link. Every field is
// length-prefixed, so values cannot be shifted between fields, and absent
// fields are authenticated as empty, so they cannot be added later.
func (s signer) mac(method, key string, q url.Values) []byte {
	m := hmac.New(sha256.New, s.key)
	for _, f := range []string{
		signatureVersion, method, key,
		q.Get(qExpires), q.Get(qMaxSize), q.Get(qSize), q.Get(qSHA256),
		q.Get(qContentType), q.Get(qFilename),
	} {
		m.Write([]byte(strconv.Itoa(len(f))))
		m.Write([]byte{':'})
		m.Write([]byte(f))
	}
	return m.Sum(nil)
}
