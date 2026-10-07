package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai"
	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// The knowledge base of the AI bot (ADR-0018): knowledge.manage reads and
// changes it.

// maxImportBytes bounds a knowledge file: 1000 entries of a few thousand
// characters.
const maxImportBytes = 16 << 20

// excerptChars is the start of a found entry shown by the search.
const excerptChars = 300

func (s *server) ListKBArticles(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	articles, err := s.ai.Articles(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	body := oapi.KBArticleList{Items: make([]oapi.KBArticle, 0, len(articles))}
	for _, a := range articles {
		body.Items = append(body.Items, kbArticleBody(a))
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) CreateKBArticle(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	var req oapi.KBArticleInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	a, err := s.ai.CreateArticle(r.Context(), p.WorkspaceID, kbArticleInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, kbArticleBody(a))
}

func (s *server) GetKBArticle(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	a, err := s.ai.Article(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, kbArticleBody(a))
}

func (s *server) UpdateKBArticle(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	var req oapi.KBArticleInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	a, err := s.ai.UpdateArticle(r.Context(), p.WorkspaceID, id, kbArticleInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, kbArticleBody(a))
}

func (s *server) DeleteKBArticle(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	if err := s.ai.DeleteArticle(r.Context(), p.WorkspaceID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) SearchKB(w http.ResponseWriter, r *http.Request, params oapi.SearchKBParams) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	if utf8.RuneCountInString(params.Q) > 1000 {
		s.writeError(w, r, fmt.Errorf("%w: q is up to 1000 characters", errBadRequest))
		return
	}
	hits, err := s.ai.Search(r.Context(), p.WorkspaceID, params.Q, params.Topic, ai.SearchLimit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	body := oapi.KBSearchResult{Items: make([]oapi.KBHit, 0, len(hits))}
	for _, h := range hits {
		body.Items = append(body.Items, oapi.KBHit{
			Id: h.ID, Kind: oapi.KBArticleKind(h.Kind), Title: h.Title, Url: optional(h.URL), Excerpt: excerpt(h.Body),
		})
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) GetKBGuide(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	g, err := s.ai.Guide(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, kbGuideBody(g))
}

func (s *server) SetKBGuide(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	var req oapi.KBGuideInput
	if err := decodeRequired(w, r, &req, "about", "handoff_rules", "task_checklist"); err != nil {
		s.writeError(w, r, err)
		return
	}
	g, err := s.ai.SetGuide(r.Context(), p.WorkspaceID, ai.Guide{About: req.About, HandoffRules: req.HandoffRules, TaskChecklist: req.TaskChecklist})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, kbGuideBody(g))
}

func (s *server) ImportKB(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.KnowledgeManage)
	if !ok {
		return
	}
	var req oapi.KBImport
	if err := decodeLenient(w, r, &req, maxImportBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	if req.Format != oapi.TolkovoKnowledge1 {
		s.writeError(w, r, fmt.Errorf("%w: format must be %s", errBadRequest, oapi.TolkovoKnowledge1))
		return
	}
	// Topics are named in the file: they are resolved here, so the ai
	// module does not read the topics of the conversations module.
	topics, err := s.conversations.Topics(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	byName := make(map[string]oapi.Id, len(topics))
	for _, t := range topics {
		byName[strings.ToLower(t.Name)] = t.ID
	}
	in := ai.Import{About: req.About, HandoffRules: req.HandoffRules, TaskChecklist: req.TaskChecklist, Articles: make([]ai.ArticleInput, 0, len(req.Articles))}
	unknown := []string{}
	for _, a := range req.Articles {
		article := ai.ArticleInput{
			Kind: string(a.Kind), Title: a.Title, Body: a.Body, URL: deref(a.Url), Published: a.Published == nil || *a.Published,
		}
		for _, name := range deref(a.Topics) {
			name = strings.TrimSpace(name)
			if id, ok := byName[strings.ToLower(name)]; ok {
				article.TopicIDs = append(article.TopicIDs, id)
			} else if name != "" && !containsFold(unknown, name) {
				unknown = append(unknown, name)
			}
		}
		in.Articles = append(in.Articles, article)
	}
	res, err := s.ai.Import(r.Context(), p.WorkspaceID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.KBImportResult{Created: res.Created, Updated: res.Updated, UnknownTopics: unknown})
}

// decodeLenient reads one JSON object like decodeJSON but lets unknown
// fields through: a knowledge file written by an agent may carry notes.
func decodeLenient(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return fmt.Errorf("%w: Content-Type must be application/json", errBadRequest)
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: body is larger than %d bytes", errBadRequest, limit)
		}
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "json: "))
	}
	if dec.More() {
		return fmt.Errorf("%w: body must be a single JSON object", errBadRequest)
	}
	return nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func excerpt(body string) string {
	if utf8.RuneCountInString(body) <= excerptChars {
		return body
	}
	return string([]rune(body)[:excerptChars])
}

func kbArticleInput(req oapi.KBArticleInput) ai.ArticleInput {
	return ai.ArticleInput{
		Kind: string(req.Kind), Title: req.Title, Body: req.Body, URL: deref(req.Url),
		Published: req.Published == nil || *req.Published, TopicIDs: deref(req.TopicIds),
	}
}

func kbArticleBody(a ai.Article) oapi.KBArticle {
	return oapi.KBArticle{
		Id: a.ID, Kind: oapi.KBArticleKind(a.Kind), Title: a.Title, Body: a.Body, Url: optional(a.URL),
		Published: a.Published, TopicIds: nonNil(a.TopicIDs), CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}

func kbGuideBody(g ai.Guide) oapi.KBGuide {
	return oapi.KBGuide{About: g.About, HandoffRules: g.HandoffRules, TaskChecklist: g.TaskChecklist, UpdatedAt: g.UpdatedAt}
}

// optional is nil for "": an absent text field.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
