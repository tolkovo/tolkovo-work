package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
	"github.com/tolkovo/tolkovo/apps/server/internal/ingest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
)

// The ingest API takes problem reports from company apps (docs/02 §11.6).
// Its routes are public operations: an ingest key in the Authorization
// header stands in for the session, and the ingest module checks it.

// CreateIngestReport implements POST /ingest/reports.
func (s *server) CreateIngestReport(w http.ResponseWriter, r *http.Request, params oapi.CreateIngestReportParams) {
	if s.ingest == nil {
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
		return
	}
	var req oapi.IngestReportRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeIngestError(w, r, err)
		return
	}
	out, err := s.ingest.Create(r.Context(), s.ingestCaller(r), deref(params.IdempotencyKey), reportInput(req))
	setRateHeaders(w, out.Rate)
	if err != nil {
		s.writeIngestError(w, r, err)
		return
	}
	uploads := make([]oapi.IngestUpload, 0, len(out.Uploads))
	for _, u := range out.Uploads {
		uploads = append(uploads, oapi.IngestUpload{Name: u.Name, Upload: signedBody(u.Request)})
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, oapi.IngestReportCreated{
		ReportId: out.ReportID, ReportToken: out.Token, ContactVerified: out.ContactVerified, Uploads: uploads,
	})
}

// CompleteIngestReport implements POST /ingest/reports/{id}/complete.
func (s *server) CompleteIngestReport(w http.ResponseWriter, r *http.Request, id oapi.Id, params oapi.CompleteIngestReportParams) {
	if s.ingest == nil {
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
		return
	}
	if err := s.ingest.Complete(r.Context(), s.ingestCaller(r), id, params.XReportToken); err != nil {
		s.writeIngestError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ListIngestReportReplies implements GET /ingest/reports/{id}/replies.
func (s *server) ListIngestReportReplies(w http.ResponseWriter, r *http.Request, id oapi.Id, params oapi.ListIngestReportRepliesParams) {
	if s.ingest == nil {
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
		return
	}
	page, err := s.ingest.Replies(r.Context(), s.ingestCaller(r), id, params.XReportToken, deref(params.Since))
	setRateHeaders(w, page.Rate)
	if err != nil {
		s.writeIngestError(w, r, err)
		return
	}
	items := make([]oapi.IngestReply, 0, len(page.Items))
	for _, m := range page.Items {
		files := make([]oapi.IngestReplyFile, 0, len(m.Files))
		for _, f := range m.Files {
			files = append(files, oapi.IngestReplyFile{
				Name: f.FileName, ContentType: f.ContentType, Size: f.Size, Download: signedBody(f.Download),
			})
		}
		items = append(items, oapi.IngestReply{
			Id: m.ID, Sender: oapi.IngestReplySender(m.SenderType), Body: m.Body, CreatedAt: m.CreatedAt, Files: files,
		})
	}
	writeJSON(w, http.StatusOK, oapi.IngestReplies{Items: items, Cursor: page.Cursor, More: page.More})
}

// PostIngestReportMessage implements POST /ingest/reports/{id}/messages.
func (s *server) PostIngestReportMessage(w http.ResponseWriter, r *http.Request, id oapi.Id, params oapi.PostIngestReportMessageParams) {
	if s.ingest == nil {
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
		return
	}
	var req oapi.IngestMessageRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeIngestError(w, r, err)
		return
	}
	out, err := s.ingest.Reply(r.Context(), s.ingestCaller(r), id, params.XReportToken, deref(params.IdempotencyKey), req.Message)
	setRateHeaders(w, out.Rate)
	if err != nil {
		s.writeIngestError(w, r, err)
		return
	}
	status := http.StatusCreated
	if !out.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, oapi.IngestMessage{Id: out.MessageID, CreatedAt: out.CreatedAt})
}

// ingestCaller reads the bearer key and the client address. Only the
// Bearer scheme counts (case-insensitive, RFC 9110 §11.1).
func (s *server) ingestCaller(r *http.Request) ingest.Caller {
	key := ""
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		key = strings.TrimSpace(h[7:])
	}
	return ingest.Caller{Key: key, IP: s.clients.of(r)}
}

func reportInput(req oapi.IngestReportRequest) ingest.Report {
	in := ingest.Report{Message: req.Message}
	if c := req.Contact; c != nil {
		in.Contact = ingest.Contact{
			ExternalID: deref(c.ExternalId), Email: deref(c.Email), Name: deref(c.Name), IdentityToken: deref(c.IdentityToken),
		}
	}
	if a := req.App; a != nil {
		in.App = ingest.App{
			Name: deref(a.Name), Version: deref(a.Version), OS: deref(a.Os), OSVersion: deref(a.OsVersion),
			Locale: deref(a.Locale), Extra: deref(a.Extra),
		}
	}
	if req.Files != nil {
		for _, f := range *req.Files {
			in.Files = append(in.Files, ingest.File{Name: f.Name, Size: f.Size, ContentType: f.ContentType, SHA256: f.Sha256})
		}
	}
	return in
}

// setRateHeaders reports a rate of the key or the report (RateLimit-* draft
// headers).
func setRateHeaders(w http.ResponseWriter, rate ratelimit.Result) {
	if rate.Limit == 0 {
		return
	}
	h := w.Header()
	h.Set("RateLimit-Limit", strconv.Itoa(rate.Limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(rate.Remaining))
	h.Set("RateLimit-Reset", strconv.Itoa(seconds(rate.Reset)))
}

func seconds(d time.Duration) int { return int(math.Ceil(d.Seconds())) }

// writeIngestError answers the ingest module's errors; anything else goes
// to writeError. Bodies name the broken limit, never the user's data.
func (s *server) writeIngestError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		limited    *ingest.RateLimitedError
		tooLarge   *ingest.TooLargeError
		processing *ingest.ProcessingError
	)
	switch {
	case errors.Is(err, channels.ErrInvalidKey):
		w.Header().Set("WWW-Authenticate", `Bearer realm="tolkovo-ingest"`)
		writeProblem(w, r, Problem{
			Status: http.StatusUnauthorized, Code: "invalid_key", Title: "Unauthorized",
			Detail: "send a valid ingest key as Authorization: Bearer tk_ingest_…",
		})
	case errors.As(err, &limited):
		setRateHeaders(w, limited.Rate)
		w.Header().Set("Retry-After", strconv.Itoa(seconds(limited.RetryAfter)))
		if limited.Quota {
			writeProblem(w, r, Problem{
				Status: http.StatusTooManyRequests, Code: "daily_quota_exceeded", Title: "Too Many Requests",
				Detail: "the channel's daily quota of reports or megabytes is used up",
			})
			return
		}
		writeProblem(w, r, Problem{Status: http.StatusTooManyRequests, Code: "too_many_requests", Title: "Too Many Requests"})
	case errors.As(err, &tooLarge):
		code := map[string]string{
			ingest.TooLargeFile: "file_too_large", ingest.TooLargeFiles: "too_many_files", ingest.TooLargeReport: "report_too_large",
		}[tooLarge.What]
		limit := strconv.FormatInt(tooLarge.Limit, 10)
		if tooLarge.What != ingest.TooLargeFiles {
			limit += " bytes"
		}
		writeProblem(w, r, Problem{
			Status: http.StatusRequestEntityTooLarge, Code: code, Title: "Content Too Large", Detail: "the limit is " + limit,
		})
	case errors.Is(err, ingest.ErrUnsupportedType):
		writeProblem(w, r, Problem{
			Status: http.StatusUnsupportedMediaType, Code: "unsupported_type", Title: "Unsupported Media Type",
			Detail: ingestDetail(err, ingest.ErrUnsupportedType) + ": allowed are text/plain, text/csv, application/json, " +
				"application/xml, text/xml, application/zip, application/gzip, image/png, image/jpeg",
		})
	case errors.Is(err, ingest.ErrNotUploaded):
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "upload_incomplete", Title: "Conflict",
			Detail: "not uploaded: " + ingestDetail(err, ingest.ErrNotUploaded),
		})
	case errors.As(err, &processing):
		w.Header().Set("Retry-After", strconv.Itoa(seconds(processing.RetryAfter)))
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "report_processing", Title: "Conflict",
			Detail: "the report is not a conversation yet, try again shortly",
		})
	case errors.Is(err, ingest.ErrSubmitted):
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "report_submitted", Title: "Conflict",
			Detail: "the report of this Idempotency-Key is already completed",
		})
	case errors.Is(err, ingest.ErrIdempotencyMismatch):
		writeProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Code: "idempotency_key_reused", Title: "Unprocessable Content",
			Detail: "the Idempotency-Key was used with a different request",
		})
	case errors.Is(err, ingest.ErrNotFound):
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
	case errors.Is(err, ingest.ErrInvalid):
		writeProblem(w, r, Problem{
			Status: http.StatusBadRequest, Code: "invalid_request", Title: "Bad Request", Detail: ingestDetail(err, ingest.ErrInvalid),
		})
	default:
		s.writeError(w, r, err)
	}
}

// ingestDetail is the text after "<sentinel>: " anywhere in the chain.
func ingestDetail(err, sentinel error) string {
	msg := err.Error()
	if i := strings.Index(msg, sentinel.Error()+": "); i >= 0 {
		return msg[i+len(sentinel.Error())+2:]
	}
	return ""
}
