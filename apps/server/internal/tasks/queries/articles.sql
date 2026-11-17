-- Articles of the knowledge base linked to tasks, and the tasks of a
-- contact (ADR-0022, P5). kb_articles belongs to internal/ai and
-- conversations to internal/conversations; tasks only reads them.

-- name: TaskArticles :many
-- The articles of a task, in the order they were linked.
SELECT a.id, a.kind, a.title, a.published, l.created_at
FROM kb_article_tasks l
JOIN kb_articles a ON a.workspace_id = l.workspace_id AND a.id = l.article_id
WHERE l.workspace_id = @workspace_id AND l.task_id = @task_id
ORDER BY l.created_at, a.id;

-- name: ArticleExists :one
SELECT EXISTS (SELECT 1 FROM kb_articles WHERE workspace_id = @workspace_id AND id = @id)::boolean AS found;

-- name: CountTaskArticles :one
SELECT count(*) FROM kb_article_tasks WHERE workspace_id = @workspace_id AND task_id = @task_id;

-- name: InsertTaskArticle :execrows
INSERT INTO kb_article_tasks (workspace_id, task_id, article_id, created_by, created_at)
VALUES (@workspace_id, @task_id, @article_id, sqlc.narg(created_by), @created_at)
ON CONFLICT DO NOTHING;

-- name: DeleteTaskArticle :execrows
DELETE FROM kb_article_tasks WHERE workspace_id = @workspace_id AND task_id = @task_id AND article_id = @article_id;

-- name: ArticleTasks :many
-- The tasks an article is linked to, open first, then the last changed;
-- visible: the viewer sees the task (task_visible).
SELECT t.id, t.project_id, p.key AS project_key, t.number, t.title, t.status, t.assignee_id, t.updated_at,
       task_visible(t.workspace_id, t.project_id, t.created_by, t.assignee_id, @viewer::uuid, @see_all::boolean)::boolean AS visible
FROM kb_article_tasks l
JOIN tasks t ON t.workspace_id = l.workspace_id AND t.id = l.task_id
JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
WHERE l.workspace_id = @workspace_id AND l.article_id = @article_id
ORDER BY t.closed_at IS NOT NULL, t.updated_at DESC, t.id
LIMIT 200;

-- name: ContactTasks :many
-- The tasks linked to the conversations of a contact, open first, then the
-- last changed; visible: the viewer sees the task (task_visible).
SELECT t.id, t.project_id, p.key AS project_key, t.number, t.title, t.status, t.assignee_id, t.updated_at,
       task_visible(t.workspace_id, t.project_id, t.created_by, t.assignee_id, @viewer::uuid, @see_all::boolean)::boolean AS visible
FROM tasks t
JOIN task_projects p ON p.workspace_id = t.workspace_id AND p.id = t.project_id
WHERE t.workspace_id = @workspace_id
  AND EXISTS (SELECT 1 FROM task_conversations l
              JOIN conversations c ON c.workspace_id = l.workspace_id AND c.id = l.conversation_id
              WHERE l.workspace_id = t.workspace_id AND l.task_id = t.id AND c.contact_id = @contact_id)
ORDER BY t.closed_at IS NOT NULL, t.updated_at DESC, t.id
LIMIT 200;
