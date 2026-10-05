-- A name for every contact. Owned by internal/conversations.
--
-- An anonymous site visitor (and anyone else who came without a name) gets
-- «Гость 4821» / «Guest 4821» in the workspace's language, so operators
-- can tell such chats apart. name_placeholder marks the generated name:
-- a name the visitor types later or an operator enters replaces it, and
-- API clients can tell it from a real one. The number comes from the
-- random tail of the contact id, so it never changes.

-- +goose Up

CREATE FUNCTION contact_guest_name(locale text, id uuid) RETURNS text
    LANGUAGE sql IMMUTABLE
    AS $$ SELECT CASE locale WHEN 'en' THEN 'Guest ' ELSE 'Гость ' END
                 || (1000 + ('x' || right(id::text, 7))::bit(28)::int % 9000)::text $$;
GRANT EXECUTE ON FUNCTION contact_guest_name(text, uuid) TO tolkovo_app;

ALTER TABLE contacts ADD COLUMN name_placeholder boolean NOT NULL DEFAULT false;

-- FORCE ROW LEVEL SECURITY hides every row from the owner as well, so it
-- is lifted for this one statement, inside the migration's transaction.
ALTER TABLE contacts NO FORCE ROW LEVEL SECURITY;
ALTER TABLE workspaces NO FORCE ROW LEVEL SECURITY;
UPDATE contacts c
SET name = contact_guest_name(w.locale, c.id), name_placeholder = true
FROM workspaces w
WHERE w.id = c.workspace_id AND c.name = '';
ALTER TABLE workspaces FORCE ROW LEVEL SECURITY;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;

ALTER TABLE contacts ADD CONSTRAINT contacts_name_chk CHECK (name <> '');

-- +goose Down

ALTER TABLE contacts DROP CONSTRAINT contacts_name_chk;
ALTER TABLE contacts NO FORCE ROW LEVEL SECURITY;
UPDATE contacts SET name = '' WHERE name_placeholder;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;
ALTER TABLE contacts DROP COLUMN name_placeholder;
DROP FUNCTION contact_guest_name(text, uuid);
