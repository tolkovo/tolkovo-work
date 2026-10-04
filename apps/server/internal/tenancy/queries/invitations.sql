-- Invitations (migration 00007). Open = neither accepted nor revoked; an
-- open invitation past expires_at is expired and cannot be accepted.

-- name: ActiveMemberByEmail :one
SELECT EXISTS (
    SELECT 1
    FROM memberships m
    JOIN users u ON u.id = m.user_id
    WHERE m.workspace_id = @workspace_id AND u.email = @email AND m.is_active
);

-- name: RevokeOpenInvitationByEmail :exec
UPDATE invitations
SET revoked_at = now()
WHERE workspace_id = @workspace_id AND email = @email
  AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: CreateInvitation :one
-- Inserts nothing when the role key does not exist for this workspace.
INSERT INTO invitations (id, workspace_id, email, role_id, invited_by, expires_at)
SELECT @id, @workspace_id, @email, r.id, @invited_by, @expires_at
FROM roles r
WHERE r.key = @role_key
  AND (r.workspace_id IS NULL OR r.workspace_id = @workspace_id)
RETURNING id, email, expires_at, created_at;

-- name: ListOpenInvitations :many
SELECT i.id, i.email, r.key AS role_key, i.expires_at, i.created_at,
       u.name AS invited_by_name
FROM invitations i
JOIN roles r ON r.id = i.role_id
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.workspace_id = @workspace_id
  AND i.accepted_at IS NULL AND i.revoked_at IS NULL
ORDER BY i.created_at DESC, i.id;

-- name: RevokeInvitation :execrows
UPDATE invitations
SET revoked_at = now()
WHERE workspace_id = @workspace_id AND id = @id
  AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: IssueInvitationToken :one
-- A new link for an open invitation: the previous token stops working and
-- the invitation is valid for another full period.
UPDATE invitations i
SET token_hash = @token_hash, expires_at = @expires_at
FROM roles r, workspaces w
WHERE i.workspace_id = @workspace_id AND i.id = @id
  AND i.accepted_at IS NULL AND i.revoked_at IS NULL
  AND r.id = i.role_id AND w.id = i.workspace_id AND w.deleted_at IS NULL
RETURNING i.id, i.email, r.key AS role_key, i.expires_at, i.created_at,
          w.name AS workspace_name, w.locale AS workspace_locale,
          COALESCE((SELECT u.name FROM users u WHERE u.id = i.invited_by), '')::text AS invited_by_name;

-- name: GetInvitationByToken :one
-- FOR UPDATE: accepting serialises on the invitation row.
SELECT i.id, i.email, r.key AS role_key, i.expires_at,
       w.name AS workspace_name
FROM invitations i
JOIN roles r ON r.id = i.role_id
JOIN workspaces w ON w.id = i.workspace_id
WHERE i.workspace_id = @workspace_id AND i.token_hash = @token_hash
  AND i.accepted_at IS NULL AND i.revoked_at IS NULL
  AND i.expires_at > now()
  AND w.deleted_at IS NULL
FOR UPDATE OF i;

-- name: MarkInvitationAccepted :exec
UPDATE invitations
SET accepted_at = now(), accepted_by = @user_id
WHERE workspace_id = @workspace_id AND id = @id;

-- name: UpsertMember :execrows
-- Adds the user, or reactivates a deactivated member with the new role.
-- An active member keeps their role (inviting cannot change it). Inserts
-- nothing when the role key does not exist for this workspace.
INSERT INTO memberships (workspace_id, user_id, role_id)
SELECT @workspace_id, @user_id, r.id
FROM roles r
WHERE r.key = @role_key
  AND (r.workspace_id IS NULL OR r.workspace_id = @workspace_id)
ON CONFLICT (workspace_id, user_id) DO UPDATE
SET role_id = EXCLUDED.role_id, is_active = true, updated_at = now()
WHERE NOT memberships.is_active;
