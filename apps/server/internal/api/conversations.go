package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

func (s *server) ListConversations(w http.ResponseWriter, r *http.Request, params oapi.ListConversationsParams) {
	p, err := authz.Require(r.Context(), authz.ConversationsView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	f := conversations.ListFilter{Cursor: deref(params.Cursor)}
	if f.Limit, err = pageLimit(params.Limit); err != nil {
		s.writeError(w, r, err)
		return
	}
	for _, st := range deref(params.Status) {
		f.Statuses = append(f.Statuses, conversations.Status(st))
	}
	switch a := deref(params.Assignee); a {
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

func (s *server) GetInboxCounts(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.ConversationsView)
	if !ok {
		return
	}
	n, err := s.conversations.InboxCounts(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.InboxCounts{Mine: n.Mine, Unassigned: n.Unassigned, Open: n.Open})
}

func (s *server) CreateConversation(w http.ResponseWriter, r *http.Request) {
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

func (s *server) GetConversation(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.ConversationsView)
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

func (s *server) ChangeConversationStatus(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.ConversationsReply)
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

func (s *server) AssignConversation(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, "")
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

func (s *server) ListMessages(w http.ResponseWriter, r *http.Request, id oapi.Id, params oapi.ListMessagesParams) {
	p, ok := s.requirePrincipal(w, r, authz.ConversationsView)
	if !ok {
		return
	}
	limit, err := pageLimit(params.Limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	page, err := s.conversations.Messages(r.Context(), p.WorkspaceID, id, deref(params.Cursor), limit)
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

func (s *server) PostMessage(w http.ResponseWriter, r *http.Request, id oapi.Id, params oapi.PostMessageParams) {
	p, ok := s.requirePrincipal(w, r, "")
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
		IdempotencyKey: deref(params.IdempotencyKey),
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

func (s *server) CreateAttachmentUpload(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, "")
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

func (s *server) GetAttachmentDownloadLink(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.ConversationsView)
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

func (s *server) ListContacts(w http.ResponseWriter, r *http.Request, params oapi.ListContactsParams) {
	p, err := authz.Require(r.Context(), authz.ContactsView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	limit, err := pageLimit(params.Limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	page, err := s.conversations.Contacts(r.Context(), p.WorkspaceID, deref(params.Cursor), limit)
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

func (s *server) CreateContact(w http.ResponseWriter, r *http.Request) {
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

func (s *server) GetContact(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.ContactsView)
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

func (s *server) UpdateContact(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.ContactsEdit)
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

// requirePrincipal checks the permission, or only that there is a caller
// when perm is empty and the handler decides.
func (s *server) requirePrincipal(w http.ResponseWriter, r *http.Request, perm authz.Permission) (authz.Principal, bool) {
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
		return authz.Principal{}, false
	}
	return p, true
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

func pageLimit(v *int) (int, error) {
	if v == nil {
		return 0, nil
	}
	if n := *v; n < 1 || n > conversations.MaxPageSize {
		return 0, fmt.Errorf("%w: limit must be between 1 and %d", errBadRequest, conversations.MaxPageSize)
	}
	return *v, nil
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
		LastMessage: previewBody(c.LastMessage),
	}
}

func previewBody(p *conversations.Preview) *oapi.ConversationPreview {
	if p == nil {
		return nil
	}
	return &oapi.ConversationPreview{
		SenderType: oapi.ConversationPreviewSenderType(p.SenderType), Kind: oapi.ConversationPreviewKind(p.Kind),
		Text: p.Text, Attachments: p.Attachments, CreatedAt: p.CreatedAt,
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
