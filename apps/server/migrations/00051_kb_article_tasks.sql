-- Articles of the knowledge base linked to tasks (ADR-0022, P5). Owned by
-- internal/tasks, as task_conversations: a task points at an article; the
-- article belongs to internal/ai, tasks only reads its title.
--
-- One knowledge base for the workspace: an article may be linked to tasks
-- of several projects ("Оплата и возвраты" to CRM-41 and APP-19), and the
-- author of a closed task sees the article to update.

-- +goose Up

CREATE TABLE kb_article_tasks (
    workspace_id uuid NOT NULL,
    task_id      uuid NOT NULL,
    article_id   uuid NOT NULL,
    created_by   uuid,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, task_id, article_id),
    FOREIGN KEY (workspace_id, task_id) REFERENCES tasks (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, article_id) REFERENCES kb_articles (workspace_id, id) ON DELETE CASCADE
);
-- The tasks of an article.
CREATE INDEX kb_article_tasks_article_idx ON kb_article_tasks (workspace_id, article_id);

ALTER TABLE kb_article_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_article_tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_article_tasks
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, DELETE ON kb_article_tasks TO tolkovo_app;

-- +goose Down

DROP TABLE kb_article_tasks;
