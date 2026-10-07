-- name: ListArticles :many
-- The entries of the knowledge base, the last changed first, with the start
-- of the text; the number is bounded by MaxArticles.
SELECT a.id, a.kind, a.title, left(a.body, 300)::text AS excerpt, a.url, a.published, a.created_at, a.updated_at,
       coalesce((SELECT array_agg(t.topic_id ORDER BY t.topic_id) FROM kb_article_topics t
                 WHERE t.workspace_id = a.workspace_id AND t.article_id = a.id), '{}')::uuid[] AS topic_ids
FROM kb_articles a
WHERE a.workspace_id = @workspace_id
ORDER BY a.updated_at DESC, a.id DESC
LIMIT @lim;

-- name: GetArticle :one
SELECT a.id, a.kind, a.title, a.body, a.url, a.published, a.created_at, a.updated_at,
       coalesce((SELECT array_agg(t.topic_id ORDER BY t.topic_id) FROM kb_article_topics t
                 WHERE t.workspace_id = a.workspace_id AND t.article_id = a.id), '{}')::uuid[] AS topic_ids
FROM kb_articles a
WHERE a.workspace_id = @workspace_id AND a.id = @id;

-- name: CountArticles :one
SELECT count(*) FROM kb_articles WHERE workspace_id = @workspace_id;

-- name: CreateArticle :exec
INSERT INTO kb_articles (id, workspace_id, kind, title, body, url, published, created_at, updated_at)
VALUES (@id, @workspace_id, @kind, @title, @body, sqlc.narg(url), @published, @now, @now);

-- name: UpdateArticle :execrows
UPDATE kb_articles
SET kind = @kind, title = @title, body = @body, url = sqlc.narg(url), published = @published, updated_at = @now
WHERE workspace_id = @workspace_id AND id = @id;

-- name: DeleteArticle :execrows
DELETE FROM kb_articles WHERE workspace_id = @workspace_id AND id = @id;

-- name: ClearArticleTopics :exec
DELETE FROM kb_article_topics WHERE workspace_id = @workspace_id AND article_id = @article_id;

-- name: AddArticleTopics :exec
INSERT INTO kb_article_topics (workspace_id, article_id, topic_id)
SELECT @workspace_id, @article_id, unnest(@topic_ids::uuid[])
ON CONFLICT DO NOTHING;

-- name: SearchArticles :many
-- Published entries matching any word of the query, the best first. With a
-- topic: the entries of that topic and the ones without topics. A question
-- with its answer weighs a little more than an article.
SELECT a.id, a.kind, a.title, a.body, a.url, a.updated_at,
       (ts_rank_cd(a.tsv, q.query) * CASE WHEN a.kind = 'qa' THEN 1.2 ELSE 1 END)::real AS rank
FROM kb_articles a, to_tsquery('russian', @query::text) AS q(query)
WHERE a.workspace_id = @workspace_id AND a.published AND a.tsv @@ q.query
  AND (sqlc.narg(topic_id)::uuid IS NULL
       OR NOT EXISTS (SELECT 1 FROM kb_article_topics t WHERE t.workspace_id = a.workspace_id AND t.article_id = a.id)
       OR EXISTS (SELECT 1 FROM kb_article_topics t
                  WHERE t.workspace_id = a.workspace_id AND t.article_id = a.id AND t.topic_id = sqlc.narg(topic_id)))
ORDER BY rank DESC, a.updated_at DESC, a.id
LIMIT @lim;

-- name: GetBotGuide :one
SELECT about, handoff_rules, task_checklist, updated_at FROM ai_bot_settings WHERE workspace_id = @workspace_id;

-- name: SetBotGuide :one
INSERT INTO ai_bot_settings (workspace_id, about, handoff_rules, task_checklist, updated_at)
VALUES (@workspace_id, @about, @handoff_rules, @task_checklist, @now)
ON CONFLICT (workspace_id) DO UPDATE
SET about = excluded.about, handoff_rules = excluded.handoff_rules, task_checklist = excluded.task_checklist,
    updated_at = excluded.updated_at
RETURNING about, handoff_rules, task_checklist, updated_at;

-- name: ArticleTitles :many
-- What an import matches its entries against.
SELECT id, kind, title FROM kb_articles
WHERE workspace_id = @workspace_id
ORDER BY created_at, id;
