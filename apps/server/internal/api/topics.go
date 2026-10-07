package api

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// Topics of questions (docs/02 §7.3): the panel's settings, the inbox and
// the tasks section read them; routing.manage changes them.

func (s *server) ListTopics(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, "")
	if !ok {
		return
	}
	if !p.Can(authz.ConversationsView) && !p.Can(authz.TasksView) {
		s.writeError(w, r, authz.ErrForbidden)
		return
	}
	topics, err := s.conversations.Topics(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, topicList(topics))
}

func (s *server) CreateTopic(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.RoutingManage)
	if !ok {
		return
	}
	var req oapi.TopicInput
	if err := decodeRequired(w, r, &req, "name"); err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := s.conversations.CreateTopic(r.Context(), p.WorkspaceID, topicInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, topicBody(t))
}

func (s *server) UpdateTopic(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.RoutingManage)
	if !ok {
		return
	}
	var req oapi.TopicInput
	if err := decodeRequired(w, r, &req, "name"); err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := s.conversations.UpdateTopic(r.Context(), p.WorkspaceID, id, topicInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, topicBody(t))
}

func (s *server) DeleteTopic(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.RoutingManage)
	if !ok {
		return
	}
	if err := s.conversations.DeleteTopic(r.Context(), p.WorkspaceID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) ReorderTopics(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.RoutingManage)
	if !ok {
		return
	}
	var req oapi.TopicOrder
	if err := decodeRequired(w, r, &req, "ids"); err != nil {
		s.writeError(w, r, err)
		return
	}
	topics, err := s.conversations.ReorderTopics(r.Context(), p.WorkspaceID, req.Ids)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, topicList(topics))
}

func (s *server) SetConversationTopic(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.ConversationsTag)
	if !ok {
		return
	}
	var req oapi.SetTopicRequest
	if err := decodeRequired(w, r, &req, "topic_id"); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.conversations.SetTopic(r.Context(), p.WorkspaceID, p.UserID, id, req.TopicId, req.Version)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, conversationBody(c))
}

// topicFilter reads the topic parameter of a list: "" any, "none" without
// a topic, or a topic id.
func topicFilter(v string) (by bool, id *uuid.UUID, err error) {
	switch v {
	case "":
		return false, nil, nil
	case "none":
		return true, nil, nil
	}
	t, err := uuid.Parse(v)
	if err != nil {
		return false, nil, fmt.Errorf("%w: topic must be none or a topic id", errBadRequest)
	}
	return true, &t, nil
}

func topicInput(req oapi.TopicInput) conversations.TopicInput {
	return conversations.TopicInput{
		Name: req.Name, Color: string(deref(req.Color)), Priority: deref(req.Priority),
		TaskQueueID: req.TaskQueueId, MemberIDs: deref(req.MemberIds),
	}
}

func topicList(topics []conversations.Topic) oapi.TopicList {
	body := oapi.TopicList{Items: make([]oapi.Topic, 0, len(topics))}
	for _, t := range topics {
		body.Items = append(body.Items, topicBody(t))
	}
	return body
}

func topicBody(t conversations.Topic) oapi.Topic {
	return oapi.Topic{
		Id: t.ID, Name: t.Name, Color: oapi.TagColor(t.Color), Position: t.Position, Priority: t.Priority,
		TaskQueueId: t.TaskQueueID, MemberIds: nonNil(t.MemberIDs),
	}
}
