package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime"
	"github.com/tolkovo/tolkovo/apps/server/internal/widget"
)

// The widget API (docs/04 §6, W-01) is called by @tolkovo/widget from a
// company's site or app, so it is cross-origin by design: the session-
// cookie checks (sameOrigin, authenticate) do not apply. A session token
// in the Authorization header stands in for the cookie, and the widget
// module answers only pages whose Origin the widget's channel allows. CORS
// is open without credentials: the browser sends no cookies, and the
// Origin check is the server's, on every request.

const (
	// widgetPrefix is where the widget's REST routes live.
	widgetPrefix = "/api/v1/widget/"
	// WidgetRealtimePath is the widget's WebSocket endpoint.
	WidgetRealtimePath = "/api/v1/widget/realtime"
)

// WidgetRealtime serves widget connections; *realtime.Gateway implements it.
type WidgetRealtime interface {
	ServeVisitor(w http.ResponseWriter, r *http.Request, v realtime.Visitor) error
}

// isWidgetRoute reports whether a request goes to the widget API.
func isWidgetRoute(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, widgetPrefix) }

// widgetCORS answers the widget's preflights and lets pages read the
// answers. It runs before routing, for widget paths only.
func widgetCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !isWidgetRoute(r) || origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Expose-Headers", "Retry-After")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// widgetCaller is the page that calls and the client's address.
func (s *server) widgetCaller(r *http.Request) widget.Caller {
	return widget.Caller{Origin: r.Header.Get("Origin"), IP: s.clients.of(r)}
}

// widgetToken reads the session token: Authorization: Bearer for REST; on
// a WebSocket handshake, the subprotocol after tolkovo.widget.v1.
func widgetToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	for _, line := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(line, ",") {
			if p = strings.TrimSpace(p); strings.HasPrefix(p, "tk_wsess_") {
				return p
			}
		}
	}
	return ""
}

func (s *server) widgetOff(w http.ResponseWriter, r *http.Request) bool {
	if s.widget == nil {
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
		return true
	}
	return false
}

// StartWidgetSession implements POST /widget/session.
func (s *server) StartWidgetSession(w http.ResponseWriter, r *http.Request) {
	if s.widgetOff(w, r) {
		return
	}
	var req oapi.WidgetSessionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	in := widget.Start{WidgetKey: req.WidgetKey, Token: deref(req.SessionToken)}
	if u := req.User; u != nil {
		in.User = widget.User{ExternalID: deref(u.ExternalId), UserHash: deref(u.UserHash), Name: deref(u.Name), Email: deref(u.Email)}
	}
	sess, err := s.widget.Start(r.Context(), s.widgetCaller(r), in)
	if err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.WidgetSession{
		SessionToken: sess.Token, ExpiresAt: sess.ExpiresAt, Identity: oapi.WidgetSessionIdentity(sess.Identity),
		ChannelName: sess.ChannelName, OperatorsOnline: sess.OperatorsOnline, Analytics: widgetAnalyticsBody(sess.Analytics),
		Topics: widgetTopics(sess.Topics),
	})
}

// GetWidgetAvailability implements GET /widget/availability.
func (s *server) GetWidgetAvailability(w http.ResponseWriter, r *http.Request) {
	if s.widgetOff(w, r) {
		return
	}
	online, err := s.widget.OperatorsOnline(r.Context(), s.widgetCaller(r), widgetToken(r))
	if err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.WidgetAvailability{OperatorsOnline: online})
}

// EndWidgetSession implements DELETE /widget/session.
func (s *server) EndWidgetSession(w http.ResponseWriter, r *http.Request) {
	if s.widgetOff(w, r) {
		return
	}
	if err := s.widget.End(r.Context(), s.widgetCaller(r), widgetToken(r)); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListWidgetMessages implements GET /widget/messages.
func (s *server) ListWidgetMessages(w http.ResponseWriter, r *http.Request, params oapi.ListWidgetMessagesParams) {
	if s.widgetOff(w, r) {
		return
	}
	page, err := s.widget.Messages(r.Context(), s.widgetCaller(r), widgetToken(r), deref(params.Since))
	if err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	items := make([]oapi.WidgetMessage, 0, len(page.Items))
	for _, m := range page.Items {
		items = append(items, widgetMessageBody(m))
	}
	body := oapi.WidgetMessages{Items: items, Cursor: page.Cursor, More: page.More}
	if c := page.Conversation; c != nil {
		status := oapi.WidgetConversationStatusOpen
		if c.Resolved {
			status = oapi.WidgetConversationStatusResolved
		}
		body.Conversation = &oapi.WidgetConversation{Id: c.ID, Status: status, Rating: ratingBody(c.Rating), TopicId: c.TopicID}
	}
	writeJSON(w, http.StatusOK, body)
}

// RateWidgetConversation implements POST /widget/rating.
func (s *server) RateWidgetConversation(w http.ResponseWriter, r *http.Request) {
	if s.widgetOff(w, r) {
		return
	}
	var req oapi.WidgetRatingRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	rating, err := s.widget.Rate(r.Context(), s.widgetCaller(r), widgetToken(r), req.ConversationId, req.Score, deref(req.Comment))
	if err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ratingBody(&rating))
}

// ChooseWidgetTopic implements POST /widget/topic.
func (s *server) ChooseWidgetTopic(w http.ResponseWriter, r *http.Request) {
	if s.widgetOff(w, r) {
		return
	}
	var req oapi.WidgetTopicRequest
	if err := decodeRequired(w, r, &req, "conversation_id", "topic_id"); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	if err := s.widget.ChooseTopic(r.Context(), s.widgetCaller(r), widgetToken(r), req.ConversationId, req.TopicId); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SendWidgetMessage implements POST /widget/messages.
func (s *server) SendWidgetMessage(w http.ResponseWriter, r *http.Request, params oapi.SendWidgetMessageParams) {
	if s.widgetOff(w, r) {
		return
	}
	var req oapi.WidgetMessageRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	sent, err := s.widget.Send(r.Context(), s.widgetCaller(r), widgetToken(r), widget.Message{
		Key: deref(params.IdempotencyKey), Body: deref(req.Message), Files: deref(req.AttachmentIds), TopicID: req.TopicId,
	})
	if err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	status := http.StatusCreated
	if !sent.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, widgetMessageBody(sent.VisitorMessage))
}

// CreateWidgetUpload implements POST /widget/uploads.
func (s *server) CreateWidgetUpload(w http.ResponseWriter, r *http.Request) {
	if s.widgetOff(w, r) {
		return
	}
	var req oapi.WidgetUploadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	up, err := s.widget.CreateUpload(r.Context(), s.widgetCaller(r), widgetToken(r), widget.UploadInput{
		FileName: req.FileName, ContentType: req.ContentType, Size: req.Size,
	})
	if err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.WidgetUpload{Id: up.ID, Upload: signedBody(up.Request)})
}

// MarkWidgetReceipt implements POST /widget/receipts.
func (s *server) MarkWidgetReceipt(w http.ResponseWriter, r *http.Request) {
	if s.widgetOff(w, r) {
		return
	}
	var req oapi.WidgetReceiptRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	err := s.widget.Receipt(r.Context(), s.widgetCaller(r), widgetToken(r), conversations.VisitorReceipt{
		DeliveredUpTo: deref(req.DeliveredUpTo), ReadUpTo: deref(req.ReadUpTo),
	})
	if err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) SubmitWidgetContact(w http.ResponseWriter, r *http.Request) {
	if s.widgetOff(w, r) {
		return
	}
	var req oapi.WidgetContactRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	in := widget.Intro{Name: deref(req.Name), Email: deref(req.Email), Phone: deref(req.Phone)}
	if req.Form != nil && !req.Form.Valid() {
		s.writeWidgetError(w, r, fmt.Errorf("%w: form is prechat or offline", widget.ErrInvalid))
		return
	}
	in.Offline = req.Form != nil && *req.Form == oapi.WidgetContactRequestFormOffline
	if req.Consent != nil {
		in.PolicyURL = req.Consent.PolicyUrl
	}
	if err := s.widget.Introduce(r.Context(), s.widgetCaller(r), widgetToken(r), in); err != nil {
		s.writeWidgetError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func widgetMessageBody(m conversations.VisitorMessage) oapi.WidgetMessage {
	files := make([]oapi.IngestReplyFile, 0, len(m.Files))
	for _, f := range m.Files {
		files = append(files, oapi.IngestReplyFile{Name: f.FileName, ContentType: f.ContentType, Size: f.Size, Download: signedBody(f.Download)})
	}
	return oapi.WidgetMessage{
		Id: m.ID, ConversationId: m.ConversationID, Sender: oapi.WidgetMessageSender(m.SenderType), Body: m.Body,
		CreatedAt: m.CreatedAt, Files: files,
	}
}

// widgetRealtimeHandler authenticates a widget's handshake by its session
// token and the page's origin, and hands it to the gateway. The token
// stays in this closure for re-checks and is never logged.
func (s *server) widgetRealtimeHandler(gw WidgetRealtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s.widgetOff(w, r) {
			return
		}
		c, token := s.widgetCaller(r), widgetToken(r)
		if err := s.widget.AllowConnect(r.Context(), c.IP); err != nil {
			s.writeWidgetError(w, r, err)
			return
		}
		v, err := s.widget.Authenticate(r.Context(), c, token)
		if err != nil {
			s.writeWidgetError(w, r, err)
			return
		}
		setLogWorkspace(r.Context(), v.WorkspaceID.String())
		err = gw.ServeVisitor(w, r, realtime.Visitor{
			WorkspaceID: v.WorkspaceID, SessionID: v.SessionID, ContactID: v.ContactID,
			Recheck: func(ctx context.Context) (uuid.UUID, error) {
				v, err := s.widget.Authenticate(ctx, c, token)
				if errors.Is(err, widget.ErrSessionEnded) || errors.Is(err, widget.ErrOriginNotAllowed) {
					return uuid.Nil, realtime.ErrSessionEnded
				}
				return v.ContactID, err
			},
		})
		if errors.Is(err, realtime.ErrUnavailable) {
			writeProblem(w, r, Problem{
				Status: http.StatusServiceUnavailable, Code: "dependency_unavailable",
				Title: "Service Unavailable", Detail: "realtime is not available, retry later",
			})
		}
	}
}

// writeWidgetError answers the widget module's errors; anything else goes
// to writeError. Bodies never repeat what the visitor sent.
func (s *server) writeWidgetError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		limited *widget.RateLimitedError
		file    *widget.FileError
	)
	switch {
	case errors.Is(err, widget.ErrInvalidKey):
		writeProblem(w, r, Problem{
			Status: http.StatusUnauthorized, Code: "invalid_widget_key", Title: "Unauthorized",
			Detail: "the widget key is not a key of a connected widget",
		})
	case errors.Is(err, widget.ErrSessionEnded):
		w.Header().Set("WWW-Authenticate", `Bearer realm="tolkovo-widget"`)
		writeProblem(w, r, Problem{
			Status: http.StatusUnauthorized, Code: "widget_session_ended", Title: "Unauthorized",
			Detail: "start a new session with POST /widget/session",
		})
	case errors.Is(err, widget.ErrOriginNotAllowed):
		writeProblem(w, r, Problem{
			Status: http.StatusForbidden, Code: "origin_not_allowed", Title: "Forbidden",
			Detail: "add this page's origin to the widget's allowed origins in the panel",
		})
	case errors.As(err, &limited):
		w.Header().Set("Retry-After", strconv.Itoa(seconds(limited.RetryAfter)))
		writeProblem(w, r, Problem{Status: http.StatusTooManyRequests, Code: "too_many_requests", Title: "Too Many Requests"})
	case errors.Is(err, widget.ErrNotFound):
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
	case errors.Is(err, widget.ErrConversationOpen):
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "conversation_open", Title: "Conflict",
			Detail: "the conversation is not resolved; it may have reopened",
		})
	case errors.Is(err, widget.ErrIdempotencyMismatch):
		writeProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Code: "idempotency_key_reused", Title: "Unprocessable Content",
			Detail: "the Idempotency-Key was used with a different message",
		})
	case errors.As(err, &file):
		p := Problem{
			Status: http.StatusUnprocessableEntity, Title: "Unprocessable Content", UploadID: file.UploadID.String(),
			Code: "file_type_mismatch", Detail: "the file is not what its type says; it was deleted",
		}
		switch file.Reason {
		case widget.FileNotUploaded:
			p.Status, p.Title, p.Code = http.StatusConflict, "Conflict", "attachment_not_uploaded"
			p.Detail = "the file is not uploaded whole; upload it again"
		case widget.FileInfected:
			p.Code, p.Detail = "file_infected", "the antivirus found a threat in the file; it was deleted"
		}
		writeProblem(w, r, p)
	case errors.Is(err, widget.ErrScanUnavailable):
		w.Header().Set("Retry-After", "30")
		writeProblem(w, r, Problem{
			Status: http.StatusServiceUnavailable, Code: "scan_unavailable", Title: "Service Unavailable",
			Detail: "files cannot be checked right now, retry later",
		})
	case errors.Is(err, widget.ErrInvalid):
		writeProblem(w, r, Problem{
			Status: http.StatusBadRequest, Code: "invalid_request", Title: "Bad Request", Detail: ingestDetail(err, widget.ErrInvalid),
		})
	default:
		s.writeError(w, r, err)
	}
}

// widgetTopics names the topics for the visitor: no members, queues or
// priorities leave the panel.
func widgetTopics(topics []conversations.Topic) []oapi.WidgetTopic {
	out := make([]oapi.WidgetTopic, 0, len(topics))
	for _, t := range topics {
		out = append(out, oapi.WidgetTopic{Id: t.ID, Name: t.Name})
	}
	return out
}
