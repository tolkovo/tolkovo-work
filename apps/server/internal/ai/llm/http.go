package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// maxAnswer bounds an answer read into memory.
const maxAnswer = 4 << 20

// maxCode bounds the provider's error code kept in an Error.
const maxCode = 64

// post sends body as JSON and decodes a 2xx answer into out. Any other
// answer becomes an *Error.
func post(ctx context.Context, hc HTTPClient, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return &Error{Kind: ErrRejected, cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Tolkovo")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		// The *url.Error names the URL, which holds no secret: keys travel
		// in headers.
		return &Error{Kind: ErrUnavailable, cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return &Error{Kind: ErrUnavailable, Status: resp.StatusCode, cause: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(resp, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &Error{Kind: ErrUnavailable, Status: resp.StatusCode, cause: errors.New("the answer is not the JSON expected")}
	}
	return nil
}

// statusError maps an error answer. Anthropic sends {"error": {"type"}},
// OpenAI {"error": {"type", "code"}}, some compatible servers
// {"error": "text"}; only the type or code is kept.
func statusError(resp *http.Response, raw []byte) *Error {
	e := &Error{Status: resp.StatusCode, Code: errorCode(raw)}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		e.Kind = ErrAuth
	case resp.StatusCode == http.StatusTooManyRequests && e.Code == "insufficient_quota":
		e.Kind = ErrRejected // out of credit: waiting does not help
	case resp.StatusCode == http.StatusTooManyRequests:
		e.Kind = ErrRateLimited
		e.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500:
		e.Kind = ErrUnavailable // 529 is Anthropic's "overloaded"
		e.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
	default:
		e.Kind = ErrRejected
	}
	return e
}

func errorCode(raw []byte) string {
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Error) == 0 {
		return ""
	}
	var detail struct {
		Type string          `json:"type"`
		Code json.RawMessage `json:"code"`
	}
	if json.Unmarshal(body.Error, &detail) != nil {
		return "" // a plain string: a message, not a code
	}
	var code string
	if json.Unmarshal(detail.Code, &code) != nil || code == "" {
		code = detail.Type
	}
	if len(code) > maxCode || !isCode(code) {
		return ""
	}
	return code
}

// isCode accepts identifiers like not_found_error or model-not-found, so
// nothing but a code ends up in logs.
func isCode(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

// retryAfter reads Retry-After in seconds; dates are rare from these APIs
// and are ignored.
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}
