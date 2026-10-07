-- The knowledge base of the AI bot (ADR-0018, docs/03 §4). Owned by internal/ai.
--
-- An entry is an article or a question with its answer, in Markdown. The bot
-- answers only from published entries: the search below picks a few of them
-- and only those reach the model. Entries of a topic serve its conversations;
-- an entry without topics serves all of them. Vectors come later with
-- pgvector; until then the search is the full-text one of Postgres.

-- +goose Up

CREATE TABLE kb_articles (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('article', 'qa')),
    -- The title of an article, the question of a qa entry.
    title         text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    -- The text of an article, the answer of a qa entry.
    body          text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 20000),
    -- A page the bot may show the customer as the source.
    url           text CHECK (char_length(url) <= 500),
    published     boolean NOT NULL DEFAULT true,
    -- The russian configuration stems Cyrillic words with the russian and
    -- Latin ones with the english stemmer, dropping the stop words of both.
    -- ё is written as е: customers rarely type it (the query does the same).
    tsv           tsvector GENERATED ALWAYS AS (
                      setweight(to_tsvector('russian'::regconfig, replace(lower(title), 'ё', 'е')), 'A') ||
                      setweight(to_tsvector('russian'::regconfig, replace(lower(body), 'ё', 'е')), 'B')) STORED,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id)
);
CREATE INDEX kb_articles_tsv_idx ON kb_articles USING gin (tsv);
CREATE INDEX kb_articles_updated_idx ON kb_articles (workspace_id, updated_at DESC, id DESC);

-- The topics an entry serves; none: every conversation.
CREATE TABLE kb_article_topics (
    workspace_id  uuid NOT NULL,
    article_id    uuid NOT NULL,
    topic_id      uuid NOT NULL,
    PRIMARY KEY (workspace_id, article_id, topic_id),
    FOREIGN KEY (workspace_id, article_id) REFERENCES kb_articles (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, topic_id) REFERENCES topics (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX kb_article_topics_topic_idx ON kb_article_topics (workspace_id, topic_id);

-- What the bot always knows, whatever the search finds: who the company is
-- and what its product does, when to call a person and what to collect for
-- a task of the IT team. All three go into every
-- prompt of the bot; the rest of its settings join in AI-05.
CREATE TABLE ai_bot_settings (
    workspace_id   uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    about          text NOT NULL DEFAULT '' CHECK (char_length(about) <= 3000),
    handoff_rules  text NOT NULL DEFAULT '' CHECK (char_length(handoff_rules) <= 3000),
    -- What the bot collects from the customer before it creates a task for
    -- the IT team, one item per line (AI-04 acts on it).
    task_checklist text NOT NULL DEFAULT '' CHECK (char_length(task_checklist) <= 3000),
    updated_at     timestamptz NOT NULL
);

ALTER TABLE ai_bot_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_bot_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_bot_settings
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE kb_articles ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_articles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_articles
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE kb_article_topics ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_article_topics FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_article_topics
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON kb_articles TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON kb_article_topics TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE ON ai_bot_settings TO tolkovo_app;

-- +goose Down
DROP TABLE ai_bot_settings;
DROP TABLE kb_article_topics;
DROP TABLE kb_articles;
