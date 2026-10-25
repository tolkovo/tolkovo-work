-- How the AI bot speaks (ADR-0018, AI-05). Owned by internal/ai.
--
-- The name it introduces itself by (empty: "the AI assistant"), its tone,
-- the form of address in languages that have one (Russian «вы» or «ты»)
-- and the company's own rules, which never override the honesty rules of
-- the prompt.

-- +goose Up

ALTER TABLE ai_bot_settings
    ADD COLUMN bot_name text NOT NULL DEFAULT '' CHECK (char_length(bot_name) <= 40),
    ADD COLUMN tone     text NOT NULL DEFAULT 'friendly' CHECK (tone IN ('friendly', 'neutral', 'formal')),
    ADD COLUMN address  text NOT NULL DEFAULT 'formal' CHECK (address IN ('formal', 'informal')),
    ADD COLUMN rules    text NOT NULL DEFAULT '' CHECK (char_length(rules) <= 2000);

-- +goose Down

ALTER TABLE ai_bot_settings
    DROP COLUMN bot_name,
    DROP COLUMN tone,
    DROP COLUMN address,
    DROP COLUMN rules;
