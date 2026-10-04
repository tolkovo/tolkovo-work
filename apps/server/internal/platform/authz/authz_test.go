package authz

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// The full phase 1 matrix (docs/04 §4.3): every role × every permission,
// both "allowed" and "denied". A new permission must be added here.
func TestAllowed_Matrix(t *testing.T) {
	want := map[Permission]map[string]bool{
		MembersView:     {RoleOwner: true, RoleAdmin: true, RoleOperator: true},
		MembersManage:   {RoleOwner: true, RoleAdmin: true, RoleOperator: false},
		WorkspaceManage: {RoleOwner: true, RoleAdmin: true, RoleOperator: false},
		WorkspaceDelete: {RoleOwner: true, RoleAdmin: false, RoleOperator: false},

		ConversationsView:   {RoleOwner: true, RoleAdmin: true, RoleOperator: true},
		ConversationsCreate: {RoleOwner: true, RoleAdmin: true, RoleOperator: false},
		ConversationsReply:  {RoleOwner: true, RoleAdmin: true, RoleOperator: true},
		ConversationsNote:   {RoleOwner: true, RoleAdmin: true, RoleOperator: true},
		ConversationsTake:   {RoleOwner: true, RoleAdmin: true, RoleOperator: true},
		ConversationsAssign: {RoleOwner: true, RoleAdmin: true, RoleOperator: false},
		ConversationsManage: {RoleOwner: true, RoleAdmin: true, RoleOperator: false},
		ConversationsTag:    {RoleOwner: true, RoleAdmin: true, RoleOperator: true},
		TagsManage:          {RoleOwner: true, RoleAdmin: true, RoleOperator: false},
		ContactsView:        {RoleOwner: true, RoleAdmin: true, RoleOperator: true},
		ContactsEdit:        {RoleOwner: true, RoleAdmin: true, RoleOperator: true},
	}
	for role, perms := range rolePermissions {
		for _, p := range perms {
			if _, ok := want[p]; !ok {
				t.Errorf("permission %q of role %q is not covered by this test", p, role)
			}
		}
	}
	for p, roles := range want {
		for _, role := range []string{RoleOwner, RoleAdmin, RoleOperator} {
			if got := Allowed(role, p); got != roles[role] {
				t.Errorf("Allowed(%s, %s) = %v, want %v", role, p, got, roles[role])
			}
		}
	}
}

func TestAllowed_UnknownRoleHasNothing(t *testing.T) {
	for _, role := range []string{"", "superuser", "Owner"} {
		if Allowed(role, MembersView) || len(Permissions(role)) != 0 {
			t.Errorf("role %q must have no permissions", role)
		}
	}
}

func TestPermissions_SortedCopy(t *testing.T) {
	got := Permissions(RoleOwner)
	if !slices.IsSorted(got) || len(got) != len(rolePermissions[RoleOwner]) {
		t.Fatalf("want every owner permission sorted, got %v", got)
	}
	got[0] = "hacked"
	if Allowed(RoleOwner, "hacked") {
		t.Fatal("Permissions must return a copy")
	}
}

func TestRequire(t *testing.T) {
	ctx := context.Background()
	if _, err := Require(ctx, MembersView); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("no principal: want ErrUnauthenticated, got %v", err)
	}
	// A principal without a workspace is not authenticated.
	half := WithPrincipal(ctx, Principal{UserID: uuid.New(), Role: RoleOwner})
	if _, err := Require(half, MembersView); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("no workspace: want ErrUnauthenticated, got %v", err)
	}
	op := Principal{UserID: uuid.New(), WorkspaceID: uuid.New(), Role: RoleOperator}
	opCtx := WithPrincipal(ctx, op)
	if _, err := Require(opCtx, WorkspaceManage); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator: want ErrForbidden, got %v", err)
	}
	got, err := Require(opCtx, MembersView)
	if err != nil || got != op {
		t.Fatalf("operator members.view: got %+v, %v", got, err)
	}
}
