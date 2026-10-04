package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Options configure NewS3.
type S3Options struct {
	// Endpoint of an S3-compatible service, e.g. http://localhost:3900 for the
	// dev Garage. Empty uses AWS endpoints for Region.
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	// UsePathStyle puts the bucket in the path instead of the host name;
	// Garage and most self-hosted services need it.
	UsePathStyle bool
	// TTL of presigned URLs; DefaultURLTTL when zero.
	TTL time.Duration
	// SpoolDir holds uploads while Put buffers them; os.TempDir when empty.
	SpoolDir string
	// HTTPClient is injectable for tests; the SDK default when nil.
	HTTPClient s3.HTTPClient
}

// S3 stores objects in a bucket of any S3-compatible service. Clients upload
// and download directly with presigned URLs.
type S3 struct {
	client   *s3.Client
	presign  *s3.PresignClient
	bucket   string
	ttl      time.Duration
	spoolDir string
}

var _ Storage = (*S3)(nil)

// NewS3 builds the client. It makes no network calls.
func NewS3(opts S3Options) (*S3, error) {
	switch {
	case opts.Region == "":
		return nil, errors.New("storage s3: region is required")
	case opts.Bucket == "":
		return nil, errors.New("storage s3: bucket is required")
	case opts.AccessKeyID == "" || opts.SecretAccessKey == "":
		return nil, errors.New("storage s3: access key id and secret access key are required")
	}
	if opts.TTL == 0 {
		opts.TTL = DefaultURLTTL
	}
	if opts.TTL < 0 || opts.TTL > MaxURLTTL {
		return nil, fmt.Errorf("storage s3: TTL must be between 0 and %s", MaxURLTTL)
	}
	s3opts := s3.Options{
		Region:       opts.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(opts.AccessKeyID, opts.SecretAccessKey, ""),
		UsePathStyle: opts.UsePathStyle,
		// Recent SDKs add CRC32 checksums to every request by default, which
		// breaks presigned uploads and many S3-compatible services.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		RetryMaxAttempts:           3,
	}
	if opts.Endpoint != "" {
		s3opts.BaseEndpoint = aws.String(strings.TrimRight(opts.Endpoint, "/"))
	}
	if opts.HTTPClient != nil {
		s3opts.HTTPClient = opts.HTTPClient
	}
	client := s3.New(s3opts)
	return &S3{
		client:   client,
		presign:  s3.NewPresignClient(client),
		bucket:   opts.Bucket,
		ttl:      opts.TTL,
		spoolDir: opts.SpoolDir,
	}, nil
}

// Put buffers the stream in a temporary file while checking the limits, then
// uploads it. Buffering lets the SDK sign the payload, retry, and send a
// known Content-Length, and nothing reaches the bucket if a limit fails.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Object, error) {
	if _, err := ParseKey(key); err != nil {
		return Object{}, err
	}
	if err := opts.validate(); err != nil {
		return Object{}, err
	}
	spool, err := os.CreateTemp(s.spoolDir, "tolkovo-upload-*")
	if err != nil {
		return Object{}, fmt.Errorf("storage s3: spool: %w", err)
	}
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spool.Name())
	}()
	n, err := copyChecked(ctx, spool, r, opts)
	if err != nil {
		return Object{}, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return Object{}, fmt.Errorf("storage s3: rewind spool: %w", err)
	}
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          spool,
		ContentLength: aws.Int64(n),
	}
	if opts.ContentType != "" {
		in.ContentType = aws.String(opts.ContentType)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return Object{}, fmt.Errorf("storage s3: put: %w", err)
	}
	return Object{Key: key, Size: n, ContentType: opts.ContentType, ModTime: time.Now()}, nil
}

// Open streams the object from the bucket.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	if _, err := ParseKey(key); err != nil {
		return nil, Object{}, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, Object{}, mapS3Error("get", err)
	}
	return out.Body, Object{
		Key: key, Size: aws.ToInt64(out.ContentLength),
		ContentType: aws.ToString(out.ContentType), ModTime: aws.ToTime(out.LastModified),
	}, nil
}

// Stat reads the object's metadata.
func (s *S3) Stat(ctx context.Context, key string) (Object, error) {
	if _, err := ParseKey(key); err != nil {
		return Object{}, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return Object{}, mapS3Error("head", err)
	}
	return Object{
		Key: key, Size: aws.ToInt64(out.ContentLength),
		ContentType: aws.ToString(out.ContentType), ModTime: aws.ToTime(out.LastModified),
	}, nil
}

// Delete removes the object. S3 answers 204 for a missing key, so any error
// (including 404 for a missing bucket) is a real failure.
func (s *S3) Delete(ctx context.Context, key string) error {
	if _, err := ParseKey(key); err != nil {
		return err
	}
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("storage s3: delete: %w", err)
	}
	return nil
}

// PresignPut returns a presigned PUT. Content-Type, Content-Length (when
// opts.Size is set) and x-amz-checksum-sha256 (when opts.SHA256 is set) are
// signed headers: the client must send them as returned, and the storage
// rejects a different file.
func (s *S3) PresignPut(ctx context.Context, key string, opts PresignPutOptions) (SignedRequest, error) {
	if _, err := ParseKey(key); err != nil {
		return SignedRequest{}, err
	}
	if err := opts.validate(); err != nil {
		return SignedRequest{}, err
	}
	in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	if opts.ContentType != "" {
		in.ContentType = aws.String(opts.ContentType)
	}
	if opts.Size > 0 {
		in.ContentLength = aws.Int64(opts.Size)
	}
	if len(opts.SHA256) > 0 {
		in.ChecksumSHA256 = aws.String(base64.StdEncoding.EncodeToString(opts.SHA256))
	}
	ttl := s.ttlOr(opts.TTL)
	req, err := s.presign.PresignPutObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return SignedRequest{}, fmt.Errorf("storage s3: presign put: %w", err)
	}
	return signedRequest(req.Method, req.URL, req.SignedHeader, ttl), nil
}

// PresignGet returns a presigned GET whose response carries
// Content-Disposition: attachment and the given type.
func (s *S3) PresignGet(ctx context.Context, key string, opts PresignGetOptions) (SignedRequest, error) {
	if _, err := ParseKey(key); err != nil {
		return SignedRequest{}, err
	}
	if err := opts.validate(); err != nil {
		return SignedRequest{}, err
	}
	in := &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentDisposition: aws.String(ContentDisposition(opts.Filename)),
		ResponseContentType:        aws.String(contentTypeOrDefault(opts.ContentType)),
	}
	ttl := s.ttlOr(opts.TTL)
	req, err := s.presign.PresignGetObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return SignedRequest{}, fmt.Errorf("storage s3: presign get: %w", err)
	}
	return signedRequest(req.Method, req.URL, req.SignedHeader, ttl), nil
}

func (s *S3) ttlOr(ttl time.Duration) time.Duration {
	if ttl == 0 {
		return s.ttl
	}
	return ttl
}

// signedRequest drops Host: the HTTP client sets it from the URL.
func signedRequest(method, url string, signed http.Header, ttl time.Duration) SignedRequest {
	h := signed.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Del("Host")
	return SignedRequest{Method: method, URL: url, Header: h, ExpiresAt: time.Now().Add(ttl)}
}

// mapS3Error turns HTTP 404 into ErrNotFound: HeadObject has no body, so the
// status is the only portable signal across S3 implementations.
func mapS3Error(op string, err error) error {
	var re *awshttp.ResponseError
	if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound {
		return ErrNotFound
	}
	return fmt.Errorf("storage s3: %s: %w", op, err)
}
