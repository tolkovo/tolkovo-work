// Package authz is the server-side permission check (docs/00 §9.2,
// docs/04 §4). A permission is "resource.action"; a role is a set of
// permissions. Handlers call Require with the permission they need, and the
// workspace always comes from the authenticated Principal, never from input.
//
// Phase 1 has only the system roles owner, admin and operator with a static
// matrix in code. Custom roles stored in role_permissions and scopes (lines,
// teams, channels) arrive with docs/06 phase 3 and v1.0.
package authz

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
)

// Errors returned by Require. The api layer maps them to 401 and 403.
var (
	ErrUnauthenticated = errors.New("authz: not authenticated")
	ErrForbidden       = errors.New("authz: permission denied")
)

// System role keys, equal to roles.key in the database.
const (
	RoleOwner    = "owner"
	RoleAdmin    = "admin"
	RoleOperator = "operator"
)

// Permission is a "resource.action" key.
type Permission string

// Phase 1 permissions. A new permission needs a row in the matrix below, a
// line in docs/04 §4.3 and allowed/denied tests (docs/00 §9.2).
const (
	MembersView     Permission = "members.view"     // see colleagues (transfer targets)
	MembersManage   Permission = "members.manage"   // invite, change role, deactivate
	WorkspaceManage Permission = "workspace.manage" // name, locale, time zone and other settings
	WorkspaceDelete Permission = "workspace.delete" // delete or transfer the workspace

	ConversationsView   Permission = "conversations.view"   // see conversations, their messages and files
	ConversationsCreate Permission = "conversations.create" // start a conversation from the panel or API
	ConversationsReply  Permission = "conversations.reply"  // reply and change the status of own conversations
	ConversationsNote   Permission = "conversations.note"   // leave internal notes in any visible conversation
	ConversationsTake   Permission = "conversations.take"   // take an unassigned conversation, release own
	ConversationsAssign Permission = "conversations.assign" // assign to anyone, take or release anyone's
	// ConversationsManage widens reply and status changes to conversations
	// assigned to someone else or nobody (scope ✅ instead of 👤 in docs/04 §4.3).
	ConversationsManage Permission = "conversations.manage"
	ConversationsTag    Permission = "conversations.tag" // put tags on any visible conversation
	TagsManage          Permission = "tags.manage"       // create tags
	ContactsView        Permission = "contacts.view"     // see contacts with email and phone
	ContactsEdit        Permission = "contacts.edit"     // create and change contacts

	ChannelsView   Permission = "channels.view"   // see connected channels and their status
	ChannelsManage Permission = "channels.manage" // connect, rename and disconnect channels (tokens, sessions)

	IntegrationsManage Permission = "integrations.manage" // connect GitHub, pick the repository, labels and templates
)

// rolePermissions is the phase 1 RBAC matrix (docs/04 §4.3).
var rolePermissions = map[string][]Permission{
	RoleOwner: {
		MembersView, MembersManage, WorkspaceManage, WorkspaceDelete,
		ConversationsView, ConversationsCreate, ConversationsReply, ConversationsNote,
		ConversationsTake, ConversationsAssign, ConversationsManage, ConversationsTag, TagsManage,
		ContactsView, ContactsEdit,
		ChannelsView, ChannelsManage, IntegrationsManage,
	},
	RoleAdmin: {
		MembersView, MembersManage, WorkspaceManage,
		ConversationsView, ConversationsCreate, ConversationsReply, ConversationsNote,
		ConversationsTake, ConversationsAssign, ConversationsManage, ConversationsTag, TagsManage,
		ContactsView, ContactsEdit,
		ChannelsView, ChannelsManage, IntegrationsManage,
	},
	RoleOperator: {
		MembersView,
		ConversationsView, ConversationsReply, ConversationsNote, ConversationsTake, ConversationsTag,
		ContactsView, ContactsEdit,
	},
}

// Allowed reports whether role has permission p. Unknown roles have none.
func Allowed(role string, p Permission) bool {
	return slices.Contains(rolePermissions[role], p)
}

// Permissions returns the sorted permissions of role, for GET /me.
func Permissions(role string) []Permission {
	out := slices.Clone(rolePermissions[role])
	slices.Sort(out)
	return out
}

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID      uuid.UUID
	WorkspaceID uuid.UUID
	Role        string
}

type principalKey struct{}

// WithPrincipal stores the caller in ctx. Only the authentication middleware
// calls it, after it has checked the session and the membership.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the caller stored by WithPrincipal.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && p.UserID != uuid.Nil && p.WorkspaceID != uuid.Nil
}

// Require returns the caller if it holds permission p in its workspace.
func Require(ctx context.Context, p Permission) (Principal, error) {
	pr, ok := PrincipalFrom(ctx)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	if !Allowed(pr.Role, p) {
		return Principal{}, ErrForbidden
	}
	return pr, nil
}
