package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// mountConversations routes the conversations and contacts operations of
// api/openapi.yaml by hand: generated parameter binding would need
// github.com/oapi-codegen/runtime, a dependency not yet approved
// (oapi-codegen.yaml). Request and response bodies are the generated
// models, and every route goes through authenticate like generated ones,
// which TestEveryRouteRequiresSessionExceptPublic checks.
func mountConversations(r chi.Router, s *server) {
	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Get("/api/v1/conversations", s.listConversations)
		r.Post("/api/v1/conversations", s.createConversation)
		r.Get("/api/v1/conversations/{id}", s.getConversation)
		r.Post("/api/v1/conversations/{id}/status", s.changeConversationStatus)
		r.Put("/api/v1/conversations/{id}/assignee", s.assignConversation)
		r.Get("/api/v1/conversations/{id}/messages", s.listMessages)
		r.Post("/api/v1/conversations/{id}/messages", s.postMessage)
		r.Post("/api/v1/conversations/{id}/attachments", s.createAttachmentUpload)
		r.Get("/api/v1/attachments/{id}/download-link", s.getAttachmentDownloadLink)
		r.Get("/api/v1/contacts", s.listContacts)
		r.Post("/api/v1/contacts", s.createContact)
		r.Get("/api/v1/contacts/{id}", s.getContact)
		r.Patch("/api/v1/contacts/{id}", s.updateContact)
	})
}

func (s *server) listConversations(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ConversationsView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	f := conversations.ListFilter{Cursor: q.Get("cursor")}
	if f.Limit, err = pageLimit(q.Get("limit")); err != nil {
		s.writeError(w, r, err)
		return
	}
	for _, st := range q["status"] {
		f.Statuses = append(f.Statuses, conversations.Status(st))
	}
	switch a := q.Get("assignee"); a {
	case "":
	case "me":
		f.ByAssignee, f.AssigneeID = true, &p.UserID
	case "none":
		f.ByAssignee = true
	default:
		id, err := uuid.Parse(a)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: assignee must be me, none or a user id", errBadRequest))
			return
		}
		f.ByAssignee, f.AssigneeID = true, &id
	}
	page, err := s.conversations.Conversations(r.Context(), p.WorkspaceID, f)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	body := oapi.ConversationPage{Items: make([]oapi.Conversation, 0, len(page.Items)), NextCursor: cursorBody(page.NextCursor)}
	for _, c := range page.Items {
		body.Items = append(body.Items, conversationBody(c))
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) createConversation(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ConversationsCreate)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.CreateConversationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.conversations.CreateConversation(r.Context(), p.WorkspaceID, conversations.CreateConversationInput{
		ContactID: req.ContactId, Subject: deref(req.Subject), Priority: deref(req.Priority),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, conversationBody(c))
}

func (s *server) getConversation(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, authz.ConversationsView)
	if !ok {
		return
	}
	c, err := s.conversations.Conversation(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, conversationBody(c))
}

func (s *server) changeConversationStatus(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, authz.ConversationsReply)
	if !ok {
		return
	}
	var req oapi.ChangeStatusRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.conversations.Transition(r.Context(), p.WorkspaceID, actorOf(p), id, conversations.Status(req.Status), req.Version)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, conversationBody(c))
}

func (s *server) assignConversation(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, "")
	if !ok {
		return
	}
	if !authz.Allowed(p.Role, authz.ConversationsTake) && !authz.Allowed(p.Role, authz.ConversationsAssign) {
		s.writeError(w, r, authz.ErrForbidden)
		return
	}
	var req oapi.AssignRequest
	if err := decodeRequired(w, r, &req, "assignee_id"); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.conversations.Assign(r.Context(), p.WorkspaceID, actorOf(p), id, req.AssigneeId, req.Version)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, conversationBody(c))
}

func (s *server) listMessages(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, authz.ConversationsView)
	if !ok {
		return
	}
	limit, err := pageLimit(r.URL.Query().Get("limit"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	page, err := s.conversations.Messages(r.Context(), p.WorkspaceID, id, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	body := oapi.MessagePage{Items: make([]oapi.Message, 0, len(page.Items)), NextCursor: cursorBody(page.NextCursor)}
	for _, m := range page.Items {
		body.Items = append(body.Items, messageBody(m))
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) postMessage(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, "")
	if !ok {
		return
	}
	var req oapi.PostMessageRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	private := deref(req.Private)
	need := authz.ConversationsReply
	if private {
		need = authz.ConversationsNote
	}
	if !authz.Allowed(p.Role, need) {
		s.writeError(w, r, authz.ErrForbidden)
		return
	}
	m, created, err := s.conversations.PostMessage(r.Context(), p.WorkspaceID, actorOf(p), id, conversations.MessageInput{
		Body: deref(req.Body), Private: private, AttachmentIDs: deref(req.AttachmentIds),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, messageBody(m))
}

func (s *server) createAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, "")
	if !ok {
		return
	}
	if !authz.Allowed(p.Role, authz.ConversationsReply) && !authz.Allowed(p.Role, authz.ConversationsNote) {
		s.writeError(w, r, authz.ErrForbidden)
		return
	}
	var req oapi.CreateUploadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	var sum []byte
	if req.Sha256 != nil {
		var err error
		if sum, err = hex.DecodeString(*req.Sha256); err != nil {
			s.writeError(w, r, fmt.Errorf("%w: sha256 must be hex", errBadRequest))
			return
		}
	}
	up, err := s.conversations.CreateUpload(r.Context(), p.WorkspaceID, actorOf(p), id, conversations.UploadInput{
		FileName: req.FileName, ContentType: req.ContentType, Size: req.Size, SHA256: sum,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.AttachmentUpload{
		Attachment: attachmentBody(up.Attachment), Upload: signedBody(up.Request),
	})
}

func (s *server) getAttachmentDownloadLink(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, authz.ConversationsView)
	if !ok {
		return
	}
	req, err := s.conversations.DownloadLink(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, signedBody(req))
}

func (s *server) listContacts(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ContactsView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	limit, err := pageLimit(r.URL.Query().Get("limit"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	page, err := s.conversations.Contacts(r.Context(), p.WorkspaceID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	body := oapi.ContactPage{Items: make([]oapi.Contact, 0, len(page.Items)), NextCursor: cursorBody(page.NextCursor)}
	for _, c := range page.Items {
		body.Items = append(body.Items, contactBody(c))
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) createContact(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ContactsEdit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.ContactInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.conversations.CreateContact(r.Context(), p.WorkspaceID, conversations.ContactInput{
		Name: deref(req.Name), Email: deref(req.Email), Phone: deref(req.Phone), Locale: deref(req.Locale),
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, contactBody(c))
}

func (s *server) getContact(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, authz.ContactsView)
	if !ok {
		return
	}
	c, err := s.conversations.Contact(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, contactBody(c))
}

func (s *server) updateContact(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.requireWithID(w, r, authz.ContactsEdit)
	if !ok {
		return
	}
	var req oapi.ContactInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.conversations.UpdateContact(r.Context(), p.WorkspaceID, id, conversations.ContactPatch{
		Name: req.Name, Email: req.Email, Phone: req.Phone, Locale: req.Locale,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, contactBody(c))
}

// requireWithID checks the permission (none when perm is empty: the
// handler decides) and parses the {id} path parameter. A malformed id is
// 404 like a missing resource.
func (s *server) requireWithID(w http.ResponseWriter, r *http.Request, perm authz.Permission) (authz.Principal, uuid.UUID, bool) {
	var (
		p   authz.Principal
		err error
	)
	if perm == "" {
		var ok bool
		if p, ok = authz.PrincipalFrom(r.Context()); !ok {
			err = authz.ErrUnauthenticated
		}
	} else {
		p, err = authz.Require(r.Context(), perm)
	}
	if err != nil {
		s.writeError(w, r, err)
		return authz.Principal{}, uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.writeError(w, r, conversations.ErrNotFound)
		return authz.Principal{}, uuid.Nil, false
	}
	return p, id, true
}

// actorOf turns the caller's role into what the conversations module may
// let them do beyond their own conversations.
func actorOf(p authz.Principal) conversations.Actor {
	return conversations.Actor{
		UserID:          p.UserID,
		AnyConversation: authz.Allowed(p.Role, authz.ConversationsManage),
		AssignOthers:    authz.Allowed(p.Role, authz.ConversationsAssign),
	}
}

func pageLimit(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > conversations.MaxPageSize {
		return 0, fmt.Errorf("%w: limit must be between 1 and %d", errBadRequest, conversations.MaxPageSize)
	}
	return n, nil
}

// decodeRequired decodes like decodeJSON and also demands that the named
// fields are present, which a Go pointer cannot tell from an explicit null.
func decodeRequired(w http.ResponseWriter, r *http.Request, dst any, fields ...string) error {
	var raw json.RawMessage
	if err := decodeJSON(w, r, &raw); err != nil {
		return err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return fmt.Errorf("%w: body must be a JSON object", errBadRequest)
	}
	for _, f := range fields {
		if _, ok := present[f]; !ok {
			return fmt.Errorf("%w: %s is required", errBadRequest, f)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		// encoding/json errors name fields and types, never the values.
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "json: "))
	}
	return nil
}

func cursorBody(c string) *string {
	if c == "" {
		return nil
	}
	return &c
}

func conversationBody(c conversations.Conversation) oapi.Conversation {
	return oapi.Conversation{
		Id: c.ID, Number: c.Number, Status: oapi.ConversationStatus(c.Status), Priority: c.Priority, LineLevel: c.LineLevel,
		Contact: oapi.ContactRef{Id: c.ContactID, Name: c.ContactName}, AssigneeId: c.AssigneeID, ChannelId: c.ChannelID,
		Subject: c.Subject, FirstResponseAt: c.FirstResponseAt, LastMessageAt: c.LastMessageAt,
		ResolvedAt: c.ResolvedAt, ClosedAt: c.ClosedAt, Version: c.Version, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func messageBody(m conversations.Message) oapi.Message {
	atts := make([]oapi.Attachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		atts = append(atts, attachmentBody(a))
	}
	return oapi.Message{
		Id: m.ID, ConversationId: m.ConversationID, Direction: oapi.MessageDirection(m.Direction),
		SenderType: oapi.MessageSenderType(m.SenderType), SenderId: m.SenderID, Kind: oapi.MessageKind(m.Kind),
		Body: m.Body, IsPrivate: m.IsPrivate, DeliveryStatus: m.DeliveryStatus, Attachments: atts, CreatedAt: m.CreatedAt,
	}
}

func attachmentBody(a conversations.Attachment) oapi.Attachment {
	return oapi.Attachment{
		Id: a.ID, FileName: a.FileName, ContentType: a.ContentType, Size: a.Size, Status: oapi.AttachmentStatus(a.Status),
	}
}

func signedBody(req storage.SignedRequest) oapi.SignedRequest {
	headers := make(map[string]string, len(req.Header))
	for k := range req.Header {
		headers[k] = req.Header.Get(k)
	}
	return oapi.SignedRequest{Method: req.Method, Url: req.URL, Headers: headers, ExpiresAt: req.ExpiresAt}
}

func contactBody(c conversations.Contact) oapi.Contact {
	return oapi.Contact{
		Id: c.ID, Name: c.Name, Email: c.Email, Phone: c.Phone, Locale: c.Locale,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
