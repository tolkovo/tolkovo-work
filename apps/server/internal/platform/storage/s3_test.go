package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newOfflineS3(t *testing.T) *S3 {
	t.Helper()
	s, err := NewS3(S3Options{
		Endpoint: "http://localhost:3900", Region: "garage", Bucket: "tolkovo",
		AccessKeyID: "GKtest", SecretAccessKey: "secret", UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return s
}

// Presigning is local computation, so the signed shape is checked without a
// server: path-style URL, expiry, and the headers that pin the upload.
func TestS3_PresignPutSignsTypeLengthAndChecksum(t *testing.T) {
	s := newOfflineS3(t)
	key := newTestKey(t)
	sum := sha256.Sum256([]byte("hello"))
	req, err := s.PresignPut(context.Background(), key, PresignPutOptions{
		ContentType: "text/plain", MaxSize: 100, Size: 5, SHA256: sum[:], TTL: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if req.Method != http.MethodPut || u.Host != "localhost:3900" || u.Path != "/tolkovo/"+key {
		t.Fatalf("want path-style PUT to /tolkovo/%s, got %s %s", key, req.Method, req.URL)
	}
	if q.Get("X-Amz-Expires") != "600" || q.Get("X-Amz-Signature") == "" ||
		!strings.Contains(q.Get("X-Amz-Credential"), "/garage/s3/") {
		t.Fatalf("unexpected presign query: %v", q)
	}
	signed := q.Get("X-Amz-SignedHeaders")
	for _, h := range []string{"content-length", "content-type", "host", "x-amz-checksum-sha256"} {
		if !strings.Contains(signed, h) {
			t.Errorf("%s is not signed: %q", h, signed)
		}
	}
	if strings.Contains(req.URL, "x-amz-sdk-checksum-algorithm") || strings.Contains(signed, "crc32") {
		t.Errorf("SDK default checksums leaked into the presigned URL: %s", req.URL)
	}
	if req.Header.Get("Content-Type") != "text/plain" || req.Header.Get("Content-Length") != "5" ||
		req.Header.Get("X-Amz-Checksum-Sha256") == "" || req.Header.Get("Host") != "" {
		t.Fatalf("client headers: %v", req.Header)
	}
}

func TestS3_PresignGetForcesAttachment(t *testing.T) {
	s := newOfflineS3(t)
	req, err := s.PresignGet(context.Background(), newTestKey(t), PresignGetOptions{Filename: "../crash.log"})
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	u, _ := url.Parse(req.URL)
	q := u.Query()
	if q.Get("response-content-disposition") != "attachment; filename=crash.log" ||
		q.Get("response-content-type") != "application/octet-stream" || q.Get("X-Amz-Expires") != "900" {
		t.Fatalf("unexpected presigned GET: %v", q)
	}
}

func TestS3_RejectsInvalidKeyWithoutNetwork(t *testing.T) {
	s := newOfflineS3(t)
	ctx := context.Background()
	if _, err := s.PresignGet(ctx, "ws/../x", PresignGetOptions{}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("PresignGet: want ErrInvalidKey, got %v", err)
	}
	if _, err := s.Put(ctx, "../x", strings.NewReader("x"), PutOptions{MaxSize: 1}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Put: want ErrInvalidKey, got %v", err)
	}
}

// TestS3_Integration runs against a real S3 API (Garage in development):
//
//	TOLKOVO_TEST_S3_ENDPOINT=http://localhost:3900
//	TOLKOVO_TEST_S3_REGION=garage
//	TOLKOVO_TEST_S3_BUCKET=tolkovo
//	TOLKOVO_TEST_S3_ACCESS_KEY_ID=...
//	TOLKOVO_TEST_S3_SECRET_ACCESS_KEY=...
//	TOLKOVO_TEST_S3_PATH_STYLE=true
//
// Without TOLKOVO_TEST_S3_ENDPOINT it is skipped.
func TestS3_Integration(t *testing.T) {
	endpoint := os.Getenv("TOLKOVO_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TOLKOVO_TEST_S3_ENDPOINT is not set")
	}
	pathStyle, _ := strconv.ParseBool(os.Getenv("TOLKOVO_TEST_S3_PATH_STYLE"))
	s, err := NewS3(S3Options{
		Endpoint:        endpoint,
		Region:          os.Getenv("TOLKOVO_TEST_S3_REGION"),
		Bucket:          os.Getenv("TOLKOVO_TEST_S3_BUCKET"),
		AccessKeyID:     os.Getenv("TOLKOVO_TEST_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("TOLKOVO_TEST_S3_SECRET_ACCESS_KEY"),
		UsePathStyle:    pathStyle,
		SpoolDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	t.Run("put open stat delete", func(t *testing.T) {
		key := newTestKey(t)
		body := []byte("integration body")
		if _, err := s.Put(ctx, key, bytes.NewReader(body), PutOptions{MaxSize: 100, ContentType: "text/plain"}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		t.Cleanup(func() { _ = s.Delete(context.Background(), key) })
		st, err := s.Stat(ctx, key)
		if err != nil || st.Size != int64(len(body)) || st.ContentType != "text/plain" {
			t.Fatalf("Stat: %+v %v", st, err)
		}
		rc, _, err := s.Open(ctx, key)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !bytes.Equal(got, body) {
			t.Fatalf("Open: %q", got)
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Stat after delete: want ErrNotFound, got %v", err)
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatalf("Delete of a missing key: %v", err)
		}
	})

	t.Run("put over limit uploads nothing", func(t *testing.T) {
		key := newTestKey(t)
		if _, err := s.Put(ctx, key, strings.NewReader("12345"), PutOptions{MaxSize: 4}); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("want ErrTooLarge, got %v", err)
		}
		if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("presigned put and get", func(t *testing.T) {
		key := newTestKey(t)
		body := []byte("uploaded by client")
		put, err := s.PresignPut(ctx, key, PresignPutOptions{ContentType: "text/plain", MaxSize: 100, Size: int64(len(body))})
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		t.Cleanup(func() { _ = s.Delete(context.Background(), key) })
		if code := send(t, ctx, put, body); code != http.StatusOK {
			t.Fatalf("presigned PUT: status %d", code)
		}
		get, err := s.PresignGet(ctx, key, PresignGetOptions{Filename: "client.log", ContentType: "text/plain"})
		if err != nil {
			t.Fatalf("PresignGet: %v", err)
		}
		resp := doSigned(t, ctx, get, nil)
		defer func() { _ = resp.Body.Close() }()
		got, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !bytes.Equal(got, body) ||
			resp.Header.Get("Content-Disposition") != "attachment; filename=client.log" {
			t.Fatalf("presigned GET: %d %q %v", resp.StatusCode, got, resp.Header)
		}
	})

	t.Run("presigned put rejects another body", func(t *testing.T) {
		key := newTestKey(t)
		put, err := s.PresignPut(ctx, key, PresignPutOptions{ContentType: "text/plain", MaxSize: 100, Size: 5})
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		t.Cleanup(func() { _ = s.Delete(context.Background(), key) })
		if code := send(t, ctx, put, []byte("123456")); code < 400 {
			t.Fatalf("a body of another length must be rejected, got %d", code)
		}
	})

	t.Run("presigned put with sha256", func(t *testing.T) {
		key := newTestKey(t)
		body := []byte("checksummed")
		sum := sha256.Sum256(body)
		put, err := s.PresignPut(ctx, key, PresignPutOptions{MaxSize: 100, Size: int64(len(body)), SHA256: sum[:]})
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		t.Cleanup(func() { _ = s.Delete(context.Background(), key) })
		if code := send(t, ctx, put, []byte("checksummeD")); code < 400 {
			t.Fatalf("a body with another digest must be rejected, got %d", code)
		}
		if code := send(t, ctx, put, body); code != http.StatusOK {
			t.Fatalf("matching body: status %d", code)
		}
	})
}

func send(t *testing.T, ctx context.Context, sr SignedRequest, body []byte) int {
	t.Helper()
	resp := doSigned(t, ctx, sr, body)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func doSigned(t *testing.T, ctx context.Context, sr SignedRequest, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, sr.Method, sr.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range sr.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue // net/http sets it from the body
		}
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", sr.Method, sr.URL, err)
	}
	return resp
}
