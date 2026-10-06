-- The customer's rating of a resolved conversation (docs/04 §5.4). Owned by
-- internal/conversations.
--
-- One rating per conversation: rating again, also after the conversation
-- reopened and was resolved once more, replaces it. resolved_by is fixed
-- when the customer rates: ai when only the bot replied, human otherwise,
-- so CSAT can be told apart for the two (docs/01 §8).

-- +goose Up

ALTER TABLE conversations
    ADD COLUMN rating smallint CHECK (rating BETWEEN 1 AND 5),
    ADD COLUMN rating_comment text CHECK (char_length(rating_comment) <= 1000),
    ADD COLUMN rating_resolved_by text CHECK (rating_resolved_by IN ('human', 'ai')),
    ADD COLUMN rated_at timestamptz,
    ADD CONSTRAINT conversations_rating_complete
        CHECK ((rating IS NULL) = (rated_at IS NULL) AND (rating IS NULL) = (rating_resolved_by IS NULL));

-- +goose Down

ALTER TABLE conversations
    DROP CONSTRAINT conversations_rating_complete,
    DROP COLUMN rated_at,
    DROP COLUMN rating_resolved_by,
    DROP COLUMN rating_comment,
    DROP COLUMN rating;
