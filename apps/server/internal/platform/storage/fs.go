package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Permissions of stored data: only the server's user can read it.
const (
	fsDirPerm  fs.FileMode = 0o700
	fsFilePerm fs.FileMode = 0o600
)

// FSOptions configure NewFS.
type FSOptions struct {
	// Root is the directory that holds all objects. It is created if missing.
	Root string
	// LinkBaseURL is the public URL that signed links point to, e.g.
	// https://support.example.com/api/v1/files. The object key is appended.
	LinkBaseURL string
	// SigningKey authenticates links; at least MinSigningKeyLen bytes.
	SigningKey []byte
	// TTL of signed links; DefaultURLTTL when zero.
	TTL time.Duration
	// Now is injectable for tests; time.Now when nil.
	Now func() time.Time
}

// FS stores objects on the local disk under one root. It suits one node only:
// several api replicas need S3 (ADR-0009). Clients upload and download
// through the api process with signed links (see VerifyLink).
//
// All file access goes through os.Root, so even a key that slipped past
// validation cannot reach outside the root, including through symlinks.
type FS struct {
	root   *os.Root
	base   string
	signer signer
	ttl    time.Duration
	now    func() time.Time
}

var _ Storage = (*FS)(nil)

// NewFS opens (and creates) the root directory.
func NewFS(opts FSOptions) (*FS, error) {
	if strings.TrimSpace(opts.Root) == "" {
		return nil, errors.New("storage fs: root directory is required")
	}
	if len(opts.SigningKey) < MinSigningKeyLen {
		return nil, fmt.Errorf("storage fs: signing key must be at least %d bytes", MinSigningKeyLen)
	}
	base, err := url.Parse(opts.LinkBaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("storage fs: link base URL must be an absolute http(s) URL without query, got %q", opts.LinkBaseURL)
	}
	if opts.TTL == 0 {
		opts.TTL = DefaultURLTTL
	}
	if opts.TTL < 0 || opts.TTL > MaxURLTTL {
		return nil, fmt.Errorf("storage fs: TTL must be between 0 and %s", MaxURLTTL)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(opts.Root, fsDirPerm); err != nil {
		return nil, fmt.Errorf("storage fs: create root: %w", err)
	}
	root, err := os.OpenRoot(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("storage fs: open root: %w", err)
	}
	return &FS{
		root:   root,
		base:   strings.TrimRight(opts.LinkBaseURL, "/"),
		signer: signer{key: append([]byte(nil), opts.SigningKey...)},
		ttl:    opts.TTL,
		now:    opts.Now,
	}, nil
}

// Close releases the root directory handle.
func (s *FS) Close() error { return s.root.Close() }

// Put writes the object atomically: into a temporary file next to it, synced,
// then hard-linked to the final name. Linking fails if the name exists, so an
// upload link cannot overwrite a stored file and is effectively single-use.
func (s *FS) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Object, error) {
	if _, err := ParseKey(key); err != nil {
		return Object{}, err
	}
	if err := opts.validate(); err != nil {
		return Object{}, err
	}
	dir := path.Dir(key)
	if err := s.root.MkdirAll(dir, fsDirPerm); err != nil {
		return Object{}, fmt.Errorf("storage fs: create dir: %w", err)
	}
	if _, err := s.root.Lstat(key); err == nil {
		return Object{}, ErrExists // cheap check before reading the body
	}
	tmp := path.Join(dir, "."+uuid.NewString()+".tmp")
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fsFilePerm)
	if err != nil {
		return Object{}, fmt.Errorf("storage fs: create temp file: %w", err)
	}
	// The temporary name is removed in every case: after a successful link
	// the object lives on under its final name.
	defer func() { _ = s.root.Remove(tmp) }()

	n, err := copyChecked(ctx, f, r, opts)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Object{}, err
	}
	if err := s.root.Link(tmp, key); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Object{}, ErrExists
		}
		return Object{}, fmt.Errorf("storage fs: publish object: %w", err)
	}
	s.syncDir(dir)
	return Object{Key: key, Size: n, ContentType: opts.ContentType, ModTime: s.now()}, nil
}

// syncDir makes the new directory entry durable. A failure here does not
// lose data that was already synced, so it is not reported.
func (s *FS) syncDir(dir string) {
	d, err := s.root.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// Open returns the file. The returned reader is an *os.File, so it also
// implements io.Seeker (the download handler serves ranges with it).
func (s *FS) Open(_ context.Context, key string) (io.ReadCloser, Object, error) {
	if _, err := ParseKey(key); err != nil {
		return nil, Object{}, err
	}
	f, err := s.root.Open(key)
	if err != nil {
		return nil, Object{}, mapFSError(err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Object{}, fmt.Errorf("storage fs: stat: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, Object{}, ErrNotFound
	}
	return f, Object{Key: key, Size: info.Size(), ModTime: info.ModTime()}, nil
}

// Stat returns the object's size and modification time.
func (s *FS) Stat(_ context.Context, key string) (Object, error) {
	if _, err := ParseKey(key); err != nil {
		return Object{}, err
	}
	info, err := s.root.Lstat(key)
	if err != nil {
		return Object{}, mapFSError(err)
	}
	if !info.Mode().IsRegular() {
		return Object{}, ErrNotFound
	}
	return Object{Key: key, Size: info.Size(), ModTime: info.ModTime()}, nil
}

// Delete removes the file; a missing file is not an error.
func (s *FS) Delete(_ context.Context, key string) error {
	if _, err := ParseKey(key); err != nil {
		return err
	}
	if err := s.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage fs: delete: %w", err)
	}
	return nil
}

// PresignPut returns a signed PUT link to the api process.
func (s *FS) PresignPut(_ context.Context, key string, opts PresignPutOptions) (SignedRequest, error) {
	if _, err := ParseKey(key); err != nil {
		return SignedRequest{}, err
	}
	if err := opts.validate(); err != nil {
		return SignedRequest{}, err
	}
	g := Grant{
		Method: http.MethodPut, Key: key, Expires: s.expiry(opts.TTL),
		ContentType: opts.ContentType, MaxSize: opts.MaxSize, Size: opts.Size, SHA256: opts.SHA256,
	}
	req := s.link(g)
	if opts.ContentType != "" {
		req.Header.Set("Content-Type", opts.ContentType)
	}
	return req, nil
}

// PresignGet returns a signed GET link to the api process.
func (s *FS) PresignGet(_ context.Context, key string, opts PresignGetOptions) (SignedRequest, error) {
	if _, err := ParseKey(key); err != nil {
		return SignedRequest{}, err
	}
	if err := opts.validate(); err != nil {
		return SignedRequest{}, err
	}
	g := Grant{
		Method: http.MethodGet, Key: key, Expires: s.expiry(opts.TTL),
		ContentType: opts.ContentType, Filename: SanitizeFilename(opts.Filename),
	}
	return s.link(g), nil
}

// VerifyLink checks a link presented to the api: method, key (from the URL
// path) and query must carry a valid, unexpired signature.
func (s *FS) VerifyLink(method, key string, q url.Values) (Grant, error) {
	return s.signer.verify(method, key, q, s.now())
}

func (s *FS) expiry(ttl time.Duration) time.Time {
	if ttl == 0 {
		ttl = s.ttl
	}
	// Whole seconds: the link carries a Unix timestamp.
	return s.now().Add(ttl).Truncate(time.Second)
}

func (s *FS) link(g Grant) SignedRequest {
	return SignedRequest{
		Method:    g.Method,
		URL:       s.base + "/" + g.Key + "?" + s.signer.sign(g).Encode(),
		Header:    http.Header{},
		ExpiresAt: g.Expires,
	}
}

func mapFSError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return fmt.Errorf("storage fs: %w", err)
}
