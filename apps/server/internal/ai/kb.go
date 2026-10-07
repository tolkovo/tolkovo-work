package ai

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/store"
)

// The knowledge base of the bot (ADR-0018, docs/03 §4): articles and
// questions with their answers. The bot answers only from published
// entries; Search picks a few of them for each turn, so the model never
// reads the whole base. The guide (about the company, when to call a
// person, what to collect for a task of the IT team) goes into every prompt.

// Limits of the knowledge base.
const (
	MaxArticles     = 1000
	MaxArticleTitle = 200
	MaxArticleBody  = 20000
	MaxArticleURL   = 500
	MaxGuideText    = 3000
	// MaxArticleTopics: a workspace has at most 20 topics.
	MaxArticleTopics = 20
	// SearchLimit is how many entries the bot gets for a turn.
	SearchLimit    = 5
	maxSearchLimit = 20
	maxSearchWords = 24
)

// Kinds of entries.
const (
	KindArticle = "article"
	KindQA      = "qa"
)

// ErrNotFound means the workspace has no such entry.
var ErrNotFound = errors.New("ai: not found")

// Article is an entry of the knowledge base. In a list, Body holds only
// the start of the text.
type Article struct {
	ID uuid.UUID
	// Kind is KindArticle or KindQA; Title is the question of a QA entry,
	// Body its answer.
	Kind      string
	Title     string
	Body      string
	URL       string
	Published bool
	// TopicIDs are the topics the entry serves; none: all of them.
	TopicIDs  []uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ArticleInput creates or replaces an entry.
type ArticleInput struct {
	Kind      string
	Title     string
	Body      string
	URL       string
	Published bool
	TopicIDs  []uuid.UUID
}

// Hit is an entry found by Search.
type Hit struct {
	ID    uuid.UUID
	Kind  string
	Title string
	Body  string
	URL   string
	Rank  float32
}

// Guide is what the bot always knows: the company and its product, when
// to call a person and what to collect before a task for the IT team.
type Guide struct {
	About        string
	HandoffRules string
	// TaskChecklist is one item per line.
	TaskChecklist string
	// UpdatedAt is nil until the guide is saved.
	UpdatedAt *time.Time
}

// Articles returns the entries, the last changed first.
func (s *Service) Articles(ctx context.Context, workspaceID uuid.UUID) ([]Article, error) {
	var out []Article
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListArticles(ctx, store.ListArticlesParams{WorkspaceID: workspaceID, Lim: MaxArticles})
		if err != nil {
			return err
		}
		out = make([]Article, 0, len(rows))
		for _, r := range rows {
			out = append(out, Article{
				ID: r.ID, Kind: r.Kind, Title: r.Title, Body: r.Excerpt, URL: deref(r.Url), Published: r.Published,
				TopicIDs: r.TopicIds, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list articles: %w", err)
	}
	return out, nil
}

// Article returns one entry with its whole text.
func (s *Service) Article(ctx context.Context, workspaceID, id uuid.UUID) (Article, error) {
	var out Article
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		out, err = getArticle(ctx, store.New(tx), workspaceID, id)
		return err
	})
	if err != nil {
		return Article{}, kbError(err, "get article")
	}
	return out, nil
}

// CreateArticle adds an entry.
func (s *Service) CreateArticle(ctx context.Context, workspaceID uuid.UUID, in ArticleInput) (Article, error) {
	in, err := cleanArticle(in)
	if err != nil {
		return Article{}, err
	}
	var out Article
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		id, err := s.insertArticle(ctx, q, workspaceID, in)
		if err != nil {
			return err
		}
		out, err = getArticle(ctx, q, workspaceID, id)
		return err
	})
	if err != nil {
		return Article{}, kbError(err, "create article")
	}
	return out, nil
}

// UpdateArticle replaces an entry.
func (s *Service) UpdateArticle(ctx context.Context, workspaceID, id uuid.UUID, in ArticleInput) (Article, error) {
	in, err := cleanArticle(in)
	if err != nil {
		return Article{}, err
	}
	var out Article
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := s.replaceArticle(ctx, q, workspaceID, id, in); err != nil {
			return err
		}
		out, err = getArticle(ctx, q, workspaceID, id)
		return err
	})
	if err != nil {
		return Article{}, kbError(err, "update article")
	}
	return out, nil
}

// DeleteArticle removes an entry.
func (s *Service) DeleteArticle(ctx context.Context, workspaceID, id uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		n, err := store.New(tx).DeleteArticle(ctx, store.DeleteArticleParams{WorkspaceID: workspaceID, ID: id})
		if err == nil && n == 0 {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return kbError(err, "delete article")
	}
	return nil
}

// Search finds published entries matching any word of text, the best
// first: with a topic, the entries of that topic and the ones without
// topics. No words, no entries.
func (s *Service) Search(ctx context.Context, workspaceID uuid.UUID, text string, topicID *uuid.UUID, limit int) ([]Hit, error) {
	query := searchQuery(text)
	if query == "" {
		return []Hit{}, nil
	}
	limit = min(max(limit, 1), maxSearchLimit)
	var out []Hit
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).SearchArticles(ctx, store.SearchArticlesParams{
			Query: query, WorkspaceID: workspaceID, TopicID: topicID, Lim: int32(limit), //nolint:gosec // bounded above
		})
		if err != nil {
			return err
		}
		out = make([]Hit, 0, len(rows))
		for _, r := range rows {
			out = append(out, Hit{ID: r.ID, Kind: r.Kind, Title: r.Title, Body: r.Body, URL: deref(r.Url), Rank: r.Rank})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search articles: %w", err)
	}
	return out, nil
}

// searchQuery turns text into a to_tsquery of its words joined by "or":
// letters and digits only, so nothing in it is an operator; one-letter
// words are dropped, repeats too, and ё is written as е, as in the index.
func searchQuery(text string) string {
	var words []string
	text = strings.ReplaceAll(strings.ToLower(text), "ё", "е")
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(w) < 2 && !unicode.IsDigit([]rune(w)[0]) {
			continue
		}
		if !slices.Contains(words, w) {
			words = append(words, w)
		}
		if len(words) == maxSearchWords {
			break
		}
	}
	return strings.Join(words, " | ")
}

// Guide returns what the bot always knows; empty until saved.
func (s *Service) Guide(ctx context.Context, workspaceID uuid.UUID) (Guide, error) {
	var out Guide
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetBotGuide(ctx, workspaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = Guide{About: row.About, HandoffRules: row.HandoffRules, TaskChecklist: row.TaskChecklist, UpdatedAt: &row.UpdatedAt}
		return nil
	})
	if err != nil {
		return Guide{}, fmt.Errorf("get bot guide: %w", err)
	}
	return out, nil
}

// SetGuide saves what the bot always knows.
func (s *Service) SetGuide(ctx context.Context, workspaceID uuid.UUID, in Guide) (Guide, error) {
	in, err := cleanGuide(in)
	if err != nil {
		return Guide{}, err
	}
	var out Guide
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		out, err = s.setGuide(ctx, store.New(tx), workspaceID, in)
		return err
	})
	if err != nil {
		return Guide{}, fmt.Errorf("set bot guide: %w", err)
	}
	return out, nil
}

func (s *Service) setGuide(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, in Guide) (Guide, error) {
	row, err := q.SetBotGuide(ctx, store.SetBotGuideParams{
		WorkspaceID: workspaceID, About: in.About, HandoffRules: in.HandoffRules, TaskChecklist: in.TaskChecklist, Now: s.now(),
	})
	if err != nil {
		return Guide{}, err
	}
	return Guide{About: row.About, HandoffRules: row.HandoffRules, TaskChecklist: row.TaskChecklist, UpdatedAt: &row.UpdatedAt}, nil
}

// Import is a knowledge file (guides/knowledge-base-brief.md): the guide
// and the entries an AI agent wrote from the app and its developers.
type Import struct {
	// About, HandoffRules and TaskChecklist replace the guide's fields when
	// not nil.
	About         *string
	HandoffRules  *string
	TaskChecklist *string
	Articles      []ArticleInput
}

// ImportResult counts the entries an import added and updated.
type ImportResult struct {
	Created int
	Updated int
}

// Import adds the file's entries in one transaction: an entry with the
// same kind and title (ignoring case) is replaced, so the file can be
// imported again after it changed. Nothing is saved when an entry is
// invalid or the base would grow over MaxArticles.
func (s *Service) Import(ctx context.Context, workspaceID uuid.UUID, in Import) (ImportResult, error) {
	if len(in.Articles) > MaxArticles {
		return ImportResult{}, fmt.Errorf("%w: a file holds up to %d entries", ErrInvalid, MaxArticles)
	}
	articles := make([]ArticleInput, 0, len(in.Articles))
	seen := map[string]bool{}
	for i, a := range in.Articles {
		a, err := cleanArticle(a)
		if err != nil {
			return ImportResult{}, fmt.Errorf("%w (entry %d)", err, i+1)
		}
		k := titleKey(a.Kind, a.Title)
		if seen[k] {
			return ImportResult{}, fmt.Errorf("%w: entry %d repeats the title %q", ErrInvalid, i+1, a.Title)
		}
		seen[k] = true
		articles = append(articles, a)
	}
	guide := Guide{}
	changesGuide := in.About != nil || in.HandoffRules != nil || in.TaskChecklist != nil
	if changesGuide {
		current, err := s.Guide(ctx, workspaceID)
		if err != nil {
			return ImportResult{}, err
		}
		guide = Guide{About: current.About, HandoffRules: current.HandoffRules, TaskChecklist: current.TaskChecklist}
		if in.About != nil {
			guide.About = *in.About
		}
		if in.HandoffRules != nil {
			guide.HandoffRules = *in.HandoffRules
		}
		if in.TaskChecklist != nil {
			guide.TaskChecklist = *in.TaskChecklist
		}
		if guide, err = cleanGuide(guide); err != nil {
			return ImportResult{}, err
		}
	}
	var out ImportResult
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if changesGuide {
			if _, err := s.setGuide(ctx, q, workspaceID, guide); err != nil {
				return err
			}
		}
		// Titles are matched here, not with lower() in SQL: Go folds the
		// case of any script whatever the database's locale.
		existing, err := q.ArticleTitles(ctx, workspaceID)
		if err != nil {
			return err
		}
		ids := make(map[string]uuid.UUID, len(existing))
		for _, e := range existing {
			if k := titleKey(e.Kind, e.Title); ids[k] == uuid.Nil {
				ids[k] = e.ID
			}
		}
		for _, a := range articles {
			if id, ok := ids[titleKey(a.Kind, a.Title)]; ok {
				if err := s.replaceArticle(ctx, q, workspaceID, id, a); err != nil {
					return err
				}
				out.Updated++
				continue
			}
			if _, err := s.insertArticle(ctx, q, workspaceID, a); err != nil {
				return err
			}
			out.Created++
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, kbError(err, "import articles")
	}
	return out, nil
}

// titleKey is an entry's kind and title with the case folded.
func titleKey(kind, title string) string {
	return kind + "\x00" + strings.ToLower(title)
}

func (s *Service) insertArticle(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, in ArticleInput) (uuid.UUID, error) {
	n, err := q.CountArticles(ctx, workspaceID)
	if err != nil {
		return uuid.Nil, err
	}
	if n >= MaxArticles {
		return uuid.Nil, fmt.Errorf("%w: the knowledge base holds up to %d entries", ErrInvalid, MaxArticles)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	if err := q.CreateArticle(ctx, store.CreateArticleParams{
		ID: id, WorkspaceID: workspaceID, Kind: in.Kind, Title: in.Title, Body: in.Body, Url: nullable(in.URL),
		Published: in.Published, Now: s.now(),
	}); err != nil {
		return uuid.Nil, err
	}
	return id, setArticleTopics(ctx, q, workspaceID, id, in.TopicIDs)
}

func (s *Service) replaceArticle(ctx context.Context, q *store.Queries, workspaceID, id uuid.UUID, in ArticleInput) error {
	n, err := q.UpdateArticle(ctx, store.UpdateArticleParams{
		Kind: in.Kind, Title: in.Title, Body: in.Body, Url: nullable(in.URL), Published: in.Published, Now: s.now(),
		WorkspaceID: workspaceID, ID: id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := q.ClearArticleTopics(ctx, store.ClearArticleTopicsParams{WorkspaceID: workspaceID, ArticleID: id}); err != nil {
		return err
	}
	return setArticleTopics(ctx, q, workspaceID, id, in.TopicIDs)
}

func setArticleTopics(ctx context.Context, q *store.Queries, workspaceID, id uuid.UUID, topics []uuid.UUID) error {
	if len(topics) == 0 {
		return nil
	}
	return q.AddArticleTopics(ctx, store.AddArticleTopicsParams{WorkspaceID: workspaceID, ArticleID: id, TopicIds: topics})
}

func getArticle(ctx context.Context, q *store.Queries, workspaceID, id uuid.UUID) (Article, error) {
	r, err := q.GetArticle(ctx, store.GetArticleParams{WorkspaceID: workspaceID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Article{}, ErrNotFound
	}
	if err != nil {
		return Article{}, err
	}
	return Article{
		ID: r.ID, Kind: r.Kind, Title: r.Title, Body: r.Body, URL: deref(r.Url), Published: r.Published,
		TopicIDs: r.TopicIds, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

func cleanArticle(in ArticleInput) (ArticleInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(in.Body)
	in.URL = strings.TrimSpace(in.URL)
	switch {
	case in.Kind != KindArticle && in.Kind != KindQA:
		return in, fmt.Errorf("%w: kind must be article or qa", ErrInvalid)
	case in.Title == "" || utf8.RuneCountInString(in.Title) > MaxArticleTitle || strings.ContainsFunc(in.Title, unicode.IsControl):
		return in, fmt.Errorf("%w: the title must be 1 to %d characters on one line", ErrInvalid, MaxArticleTitle)
	case in.Body == "" || utf8.RuneCountInString(in.Body) > MaxArticleBody:
		return in, fmt.Errorf("%w: the text must be 1 to %d characters", ErrInvalid, MaxArticleBody)
	case in.URL != "" && !webURL(in.URL):
		return in, fmt.Errorf("%w: the link must be an http(s) address of up to %d characters", ErrInvalid, MaxArticleURL)
	}
	topics := make([]uuid.UUID, 0, len(in.TopicIDs))
	for _, t := range in.TopicIDs {
		if !slices.Contains(topics, t) {
			topics = append(topics, t)
		}
	}
	if len(topics) > MaxArticleTopics {
		return in, fmt.Errorf("%w: an entry has up to %d topics", ErrInvalid, MaxArticleTopics)
	}
	in.TopicIDs = topics
	return in, nil
}

func cleanGuide(in Guide) (Guide, error) {
	in.About = strings.TrimSpace(in.About)
	in.HandoffRules = strings.TrimSpace(in.HandoffRules)
	in.TaskChecklist = strings.TrimSpace(in.TaskChecklist)
	if utf8.RuneCountInString(in.About) > MaxGuideText || utf8.RuneCountInString(in.HandoffRules) > MaxGuideText ||
		utf8.RuneCountInString(in.TaskChecklist) > MaxGuideText {
		return in, fmt.Errorf("%w: each field of the guide is up to %d characters", ErrInvalid, MaxGuideText)
	}
	return in, nil
}

// webURL: a link the customer may open, http or https with a host.
func webURL(raw string) bool {
	if len(raw) > MaxArticleURL || strings.ContainsFunc(raw, unicode.IsSpace) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

func kbError(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return fmt.Errorf("%w: a topic does not exist", ErrInvalid)
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}
