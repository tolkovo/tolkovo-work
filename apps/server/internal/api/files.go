package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// FilesPath is where signed links of the fs storage driver point
// (docs/02 §5.4). The object key follows it in the path; the signature and
// limits are in the query. With the s3 driver clients go to the bucket and
// these routes are not mounted.
const FilesPath = "/api/v1/files"

// fileTransferTimeout bounds one upload or download. A 25 MB report over a
// slow mobile link does not fit the API-wide 30 s timeout.
const fileTransferTimeout = 15 * time.Minute

// LocalFiles is the fs storage driver as the signed-link handlers use it.
type LocalFiles interface {
	VerifyLink(method, key string, q url.Values) (storage.Grant, error)
	Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.Object, error)
	Open(ctx context.Context, key string) (io.ReadCloser, storage.Object, error)
}

func mountFiles(r chi.Router, files LocalFiles, log *slog.Logger) {
	r.Options(FilesPath+"/*", uploadPreflight)
	r.Put(FilesPath+"/*", anyOrigin(uploadFile(files, log)))
	r.Get(FilesPath+"/*", downloadFile(files, log))
}

// Uploads come from pages of any origin: the widget runs on a company's
// site (W-02). The signature in the link is the permission and no cookie
// goes along, so CORS is open without credentials.

// uploadPreflight answers a browser's preflight of a signed PUT.
func uploadPreflight(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "PUT")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// anyOrigin lets a page of any origin read the answer to its upload.
func anyOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next(w, r)
	}
}

// uploadFile accepts the body of a signed PUT link. The link decides the
// key, the size limits, the digest and the content type; the request only
// supplies bytes.
func uploadFile(files LocalFiles, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "*")
		g, err := files.VerifyLink(http.MethodPut, key, r.URL.Query())
		if err != nil {
			writeLinkProblem(w, r, err)
			return
		}
		if g.ContentType != "" && r.Header.Get("Content-Type") != g.ContentType {
			writeProblem(w, r, Problem{
				Status: http.StatusUnsupportedMediaType, Code: "content_type_mismatch",
				Title: "Unsupported Media Type", Detail: "Content-Type must match the upload link",
			})
			return
		}
		if r.ContentLength > g.MaxSize {
			writeProblem(w, r, tooLarge(g.MaxSize))
			return
		}
		if g.Size > 0 && r.ContentLength >= 0 && r.ContentLength != g.Size {
			writeProblem(w, r, sizeMismatch())
			return
		}
		extendDeadlines(w)
		ctx, cancel := context.WithTimeout(r.Context(), fileTransferTimeout)
		defer cancel()

		body := http.MaxBytesReader(w, r.Body, g.MaxSize)
		_, err = files.Put(ctx, key, body, storage.PutOptions{
			ContentType: g.ContentType, MaxSize: g.MaxSize, Size: g.Size, SHA256: g.SHA256,
		})
		var maxErr *http.MaxBytesError
		switch {
		case err == nil:
			w.WriteHeader(http.StatusOK) // as S3 answers a PUT, so clients treat both drivers alike
		case errors.Is(err, storage.ErrTooLarge), errors.As(err, &maxErr):
			writeProblem(w, r, tooLarge(g.MaxSize))
		case errors.Is(err, storage.ErrSizeMismatch):
			writeProblem(w, r, sizeMismatch())
		case errors.Is(err, storage.ErrChecksumMismatch):
			writeProblem(w, r, Problem{
				Status: http.StatusBadRequest, Code: "checksum_mismatch",
				Title: "Bad Request", Detail: "file content does not match the SHA-256 of the upload link",
			})
		case errors.Is(err, storage.ErrExists):
			writeProblem(w, r, Problem{
				Status: http.StatusConflict, Code: "already_uploaded",
				Title: "Conflict", Detail: "this upload link has already been used",
			})
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// The client went away or was too slow; nothing was stored.
			writeProblem(w, r, Problem{Status: http.StatusRequestTimeout, Code: "upload_timeout", Title: "Request Timeout"})
		default:
			log.ErrorContext(r.Context(), "store uploaded file",
				slog.String("request_id", middleware.GetReqID(r.Context())), slog.Any("error", err))
			writeProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal", Title: "Internal Server Error"})
		}
	}
}

// downloadFile serves a signed GET link. The file never renders as a page:
// attachment disposition, nosniff and a sandbox CSP.
func downloadFile(files LocalFiles, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "*")
		g, err := files.VerifyLink(http.MethodGet, key, r.URL.Query())
		if err != nil {
			writeLinkProblem(w, r, err)
			return
		}
		extendDeadlines(w)
		ctx, cancel := context.WithTimeout(r.Context(), fileTransferTimeout)
		defer cancel()

		rc, obj, err := files.Open(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
			return
		}
		if err != nil {
			log.ErrorContext(r.Context(), "open stored file",
				slog.String("request_id", middleware.GetReqID(r.Context())), slog.Any("error", err))
			writeProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal", Title: "Internal Server Error"})
			return
		}
		defer func() { _ = rc.Close() }()

		h := w.Header()
		ct := g.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		h.Set("Content-Disposition", storage.ContentDisposition(g.Filename))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Cache-Control", "private, no-store")
		h.Set("Referrer-Policy", "no-referrer")
		if rs, ok := rc.(io.ReadSeeker); ok {
			http.ServeContent(w, r, "", obj.ModTime, rs) // ranges and conditional requests
			return
		}
		h.Set("Content-Length", strconv.FormatInt(obj.Size, 10))
		_, _ = io.Copy(w, rc)
	}
}

// requestTimeout applies the API-wide timeout to every route except signed
// file transfers, which bound themselves with fileTransferTimeout, and the
// WebSocket endpoint.
func requestTimeout(d time.Duration) func(http.Handler) http.Handler {
	timeout := middleware.Timeout(d)
	return func(next http.Handler) http.Handler {
		limited := timeout(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// File transfers set their own deadlines; a WebSocket lives for
			// hours and ends through the gateway, not a request timeout.
			if strings.HasPrefix(r.URL.Path, FilesPath+"/") || r.URL.Path == RealtimePath {
				next.ServeHTTP(w, r)
				return
			}
			limited.ServeHTTP(w, r)
		})
	}
}

// extendDeadlines lifts the server-wide read and write timeouts for one
// transfer. Writers that do not support deadlines (tests) are left alone.
func extendDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(fileTransferTimeout)
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
}

func writeLinkProblem(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, storage.ErrLinkExpired) {
		writeProblem(w, r, Problem{
			Status: http.StatusForbidden, Code: "link_expired",
			Title: "Forbidden", Detail: "the link has expired, request a new one",
		})
		return
	}
	writeProblem(w, r, Problem{Status: http.StatusForbidden, Code: "link_invalid", Title: "Forbidden"})
}

func tooLarge(limit int64) Problem {
	return Problem{
		Status: http.StatusRequestEntityTooLarge, Code: "file_too_large",
		Title: "Content Too Large", Detail: "the file exceeds " + strconv.FormatInt(limit, 10) + " bytes",
	}
}

func sizeMismatch() Problem {
	return Problem{
		Status: http.StatusBadRequest, Code: "size_mismatch",
		Title: "Bad Request", Detail: "file size does not match the upload link",
	}
}
