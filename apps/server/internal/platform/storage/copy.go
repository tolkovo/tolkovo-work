package storage

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
)

// copyChecked copies src into dst and enforces the size limit, the exact
// size and the digest from opts. Read errors are wrapped with %w so callers
// can recognise e.g. *http.MaxBytesError.
func copyChecked(ctx context.Context, dst io.Writer, src io.Reader, opts PutOptions) (int64, error) {
	h := sha256.New()
	// One byte past the limit tells "exactly MaxSize" from "too large".
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(ctxReader{ctx, src}, opts.MaxSize+1))
	if err != nil {
		return n, fmt.Errorf("copy object: %w", err)
	}
	if n > opts.MaxSize {
		return n, ErrTooLarge
	}
	if opts.Size > 0 && n != opts.Size {
		return n, ErrSizeMismatch
	}
	if len(opts.SHA256) > 0 && subtle.ConstantTimeCompare(h.Sum(nil), opts.SHA256) != 1 {
		return n, ErrChecksumMismatch
	}
	return n, nil
}

// ctxReader stops a long copy when the request or job is cancelled. It lives
// only for one io.Copy call, so holding the context is safe.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
