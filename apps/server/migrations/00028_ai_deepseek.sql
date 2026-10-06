-- DeepSeek as a provider of its own (docs/03 §8.1, ADR-0008): its API is
-- OpenAI's, but it answers JSON only as json_object and counts cached
-- tokens its own way, so it is not an openai_compatible base_url.

-- +goose Up

ALTER TABLE ai_credentials
    DROP CONSTRAINT ai_credentials_provider_check,
    ADD CONSTRAINT ai_credentials_provider_check
        CHECK (provider IN ('anthropic', 'openai', 'deepseek', 'openai_compatible'));
ALTER TABLE ai_settings
    DROP CONSTRAINT ai_settings_provider_check,
    ADD CONSTRAINT ai_settings_provider_check
        CHECK (provider IN ('anthropic', 'openai', 'deepseek', 'openai_compatible'));

-- +goose Down
UPDATE ai_settings SET enabled = false, provider = NULL, model = NULL, credential_id = NULL
    WHERE provider = 'deepseek';
DELETE FROM ai_credentials WHERE provider = 'deepseek';
ALTER TABLE ai_settings
    DROP CONSTRAINT ai_settings_provider_check,
    ADD CONSTRAINT ai_settings_provider_check
        CHECK (provider IN ('anthropic', 'openai', 'openai_compatible'));
ALTER TABLE ai_credentials
    DROP CONSTRAINT ai_credentials_provider_check,
    ADD CONSTRAINT ai_credentials_provider_check
        CHECK (provider IN ('anthropic', 'openai', 'openai_compatible'));
