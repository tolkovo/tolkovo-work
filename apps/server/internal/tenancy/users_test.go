package tenancy_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

func registerInput(email, slug string) tenancy.RegisterInput {
	return tenancy.RegisterInput{
		Email: email, Name: "User", PasswordHash: "$argon2id$stub",
		WorkspaceName: "WS " + slug, WorkspaceSlug: slug,
	}
}

func TestRegister_CreatesUserWorkspaceAndOwner(t *testing.T) {
	f := setup(t)
	m, err := f.svc.Register(f.ctx, registerInput(" marina@example.test ", "zerno"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if m.Role != tenancy.RoleOwner || m.User.Email != "marina@example.test" || m.Workspace.Slug != "zerno" {
		t.Fatalf("unexpected membership: %+v", m)
	}
	got, err := f.svc.Membership(f.ctx, m.Workspace.ID, m.User.ID)
	if err != nil || got.Role != tenancy.RoleOwner || got.Workspace.Name != "WS zerno" {
		t.Fatalf("Membership: %+v %v", got, err)
	}
	creds, err := f.svc.Credentials(f.ctx, "MARINA@example.test")
	if err != nil || creds.UserID != m.User.ID || creds.PasswordHash != "$argon2id$stub" || creds.Disabled {
		t.Fatalf("Credentials: %+v %v", creds, err)
	}
}

// A failure at any step leaves nothing behind: the user row is written in
// the same transaction as the workspace and the membership.
func TestRegister_IsAtomic(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.Register(f.ctx, registerInput("a@example.test", "zerno")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := f.svc.Register(f.ctx, registerInput("b@example.test", "zerno")); !errors.Is(err, tenancy.ErrSlugTaken) {
		t.Fatalf("taken slug: want ErrSlugTaken, got %v", err)
	}
	if _, err := f.svc.Credentials(f.ctx, "b@example.test"); !errors.Is(err, tenancy.ErrNotFound) {
		t.Fatalf("user of the failed registration must not exist, got %v", err)
	}
	if _, err := f.svc.Register(f.ctx, registerInput("A@example.test", "other")); !errors.Is(err, tenancy.ErrEmailTaken) {
		t.Fatalf("taken email: want ErrEmailTaken, got %v", err)
	}
	var users int
	err := f.app.InGlobalTx(f.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctx, "SELECT count(*) FROM users").Scan(&users)
	})
	if err != nil || users != 1 {
		t.Fatalf("want exactly 1 user, got %d (%v)", users, err)
	}
	// The workspace of the failed email attempt was rolled back, so its slug is free.
	if _, err := f.svc.Register(f.ctx, registerInput("c@example.test", "other")); err != nil {
		t.Fatalf("slug of the failed attempt must be free: %v", err)
	}
}

func TestRegister_RejectsInvalidInput(t *testing.T) {
	f := setup(t)
	tests := map[string]tenancy.RegisterInput{
		"bad email":    registerInput("nope", "zerno"),
		"display name": registerInput("Marina <m@example.test>", "zerno"),
		"no hash":      {Email: "m@example.test", Name: "M", WorkspaceName: "W", WorkspaceSlug: "zerno"},
		"empty name":   {Email: "m@example.test", Name: " ", PasswordHash: "x", WorkspaceName: "W", WorkspaceSlug: "zerno"},
		"bad slug":     registerInput("m@example.test", "Z"),
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := f.svc.Register(f.ctx, in); !errors.Is(err, tenancy.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestMembership_NotFoundWhenAccessIsGone(t *testing.T) {
	f := setup(t)
	m, err := f.svc.Register(f.ctx, registerInput("a@example.test", "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.Register(f.ctx, registerInput("b@example.test", "bravo"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		ws, usr uuid.UUID
	}{
		{"foreign workspace", other.Workspace.ID, m.User.ID},
		{"unknown workspace", uuid.Must(uuid.NewV7()), m.User.ID},
		{"zero workspace", uuid.Nil, m.User.ID},
	}
	for _, c := range cases {
		if _, err := f.svc.Membership(f.ctx, c.ws, c.usr); !errors.Is(err, tenancy.ErrNotFound) {
			t.Errorf("%s: want ErrNotFound, got %v", c.name, err)
		}
	}
}

// The own_memberships policy (migration 00003) shows a user only their own
// memberships, and only inside InUserTx.
func TestUserWorkspaces_OnlyOwnMemberships(t *testing.T) {
	f := setup(t)
	a, _ := f.svc.Register(f.ctx, registerInput("a@example.test", "alpha"))
	b, _ := f.svc.Register(f.ctx, registerInput("b@example.test", "bravo"))
	if err := f.svc.AddMember(f.ctx, b.Workspace.ID, a.User.ID, tenancy.RoleOperator); err != nil {
		t.Fatal(err)
	}

	list, err := f.svc.UserWorkspaces(f.ctx, a.User.ID)
	if err != nil || len(list) != 2 || list[0].ID != a.Workspace.ID || list[1].Role != tenancy.RoleOperator {
		t.Fatalf("A's workspaces: %+v %v", list, err)
	}
	list, err = f.svc.UserWorkspaces(f.ctx, b.User.ID)
	if err != nil || len(list) != 1 || list[0].ID != b.Workspace.ID {
		t.Fatalf("B's workspaces: %+v %v", list, err)
	}

	count := func(userID uuid.UUID, table string) int {
		var n int
		err := f.app.InUserTx(f.ctx, userID, func(tx pgx.Tx) error {
			// Deliberately unfiltered: RLS alone must hide other users' rows.
			return tx.QueryRow(f.ctx, "SELECT count(*) FROM "+table).Scan(&n)
		})
		if err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	if n := count(b.User.ID, "memberships"); n != 1 {
		t.Errorf("B sees %d memberships, want only its own", n)
	}
	if n := count(b.User.ID, "workspaces"); n != 1 {
		t.Errorf("B sees %d workspaces, want 1", n)
	}
	if n := count(a.User.ID, "memberships"); n != 2 {
		t.Errorf("A sees %d memberships, want its 2 (not B's owner row)", n)
	}

	// The user scope is read-only: no tenant writes without a workspace.
	err = f.app.InUserTx(f.ctx, a.User.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, "UPDATE workspaces SET name = 'x' WHERE id = $1", b.Workspace.ID)
		if err != nil {
			return err
		}
		var name string
		return tx.QueryRow(f.ctx, "SELECT name FROM workspaces WHERE id = $1", b.Workspace.ID).Scan(&name)
	})
	if err != nil {
		t.Fatalf("read after no-op update: %v", err)
	}
	if ws, _ := f.svc.Workspace(f.ctx, b.Workspace.ID); ws.Name != "WS bravo" {
		t.Fatalf("InUserTx must not write tenant rows, name is %q", ws.Name)
	}
	if err := f.app.InUserTx(f.ctx, uuid.Nil, func(pgx.Tx) error { return nil }); !errors.Is(err, db.ErrNoUser) {
		t.Fatalf("want ErrNoUser, got %v", err)
	}
}

func TestUpdateWorkspace(t *testing.T) {
	f := setup(t)
	m, _ := f.svc.Register(f.ctx, registerInput("a@example.test", "alpha"))
	name, tz := "Alpha Ltd", "Europe/Berlin"
	ws, err := f.svc.UpdateWorkspace(f.ctx, m.Workspace.ID, tenancy.UpdateWorkspaceInput{Name: &name, Timezone: &tz})
	if err != nil || ws.Name != name || ws.Timezone != tz || ws.Locale != "ru" || ws.Slug != "alpha" {
		t.Fatalf("update: %+v %v", ws, err)
	}
	bad := "Mars/Base"
	if _, err := f.svc.UpdateWorkspace(f.ctx, m.Workspace.ID, tenancy.UpdateWorkspaceInput{Timezone: &bad}); !errors.Is(err, tenancy.ErrInvalid) {
		t.Fatalf("bad timezone: want ErrInvalid, got %v", err)
	}
	if _, err := f.svc.UpdateWorkspace(f.ctx, uuid.Must(uuid.NewV7()), tenancy.UpdateWorkspaceInput{Name: &name}); !errors.Is(err, tenancy.ErrNotFound) {
		t.Fatalf("unknown workspace: want ErrNotFound, got %v", err)
	}
}

func TestUpdateUser(t *testing.T) {
	f := setup(t)
	m, _ := f.svc.Register(f.ctx, registerInput("a@example.test", "alpha"))
	other, _ := f.svc.Register(f.ctx, registerInput("b@example.test", "bravo"))

	name, en := "  Anna  ", "en"
	u, err := f.svc.UpdateUser(f.ctx, m.User.ID, tenancy.UpdateUserInput{Name: &name, Locale: &en})
	if err != nil || u.Name != "Anna" || u.Locale != "en" || u.Email != "a@example.test" {
		t.Fatalf("update: %+v %v", u, err)
	}
	// Absent fields keep their value; the change is visible in the membership.
	if u, err = f.svc.UpdateUser(f.ctx, m.User.ID, tenancy.UpdateUserInput{}); err != nil || u.Name != "Anna" || u.Locale != "en" {
		t.Fatalf("empty update: %+v %v", u, err)
	}
	got, _ := f.svc.Membership(f.ctx, m.Workspace.ID, m.User.ID)
	if got.User.Name != "Anna" || got.User.Locale != "en" {
		t.Fatalf("membership user: %+v", got.User)
	}
	// Only the given account changes.
	if ou, _ := f.svc.Membership(f.ctx, other.Workspace.ID, other.User.ID); ou.User.Name != "User" {
		t.Fatalf("another user changed: %+v", ou.User)
	}

	blank, long, de := "   ", strings.Repeat("я", 121), "de"
	for label, in := range map[string]tenancy.UpdateUserInput{
		"blank name": {Name: &blank}, "long name": {Name: &long}, "bad locale": {Locale: &de},
	} {
		if _, err := f.svc.UpdateUser(f.ctx, m.User.ID, in); !errors.Is(err, tenancy.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", label, err)
		}
	}
	if _, err := f.svc.UpdateUser(f.ctx, uuid.Must(uuid.NewV7()), tenancy.UpdateUserInput{Name: &name}); !errors.Is(err, tenancy.ErrNotFound) {
		t.Fatalf("unknown user: want ErrNotFound, got %v", err)
	}
}

func TestSetPasswordHash(t *testing.T) {
	f := setup(t)
	m, _ := f.svc.Register(f.ctx, registerInput("a@example.test", "alpha"))
	if err := f.svc.SetPasswordHash(f.ctx, m.User.ID, "$argon2id$new"); err != nil {
		t.Fatalf("set: %v", err)
	}
	c, err := f.svc.CredentialsByID(f.ctx, m.User.ID)
	if err != nil || c.PasswordHash != "$argon2id$new" || c.Disabled {
		t.Fatalf("credentials: %+v %v", c, err)
	}
	if err := f.svc.SetPasswordHash(f.ctx, uuid.Must(uuid.NewV7()), "$argon2id$x"); !errors.Is(err, tenancy.ErrNotFound) {
		t.Fatalf("unknown user: want ErrNotFound, got %v", err)
	}
	if _, err := f.svc.CredentialsByID(f.ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, tenancy.ErrNotFound) {
		t.Fatalf("unknown user credentials: want ErrNotFound, got %v", err)
	}
	if err := f.svc.SetPasswordHash(f.ctx, m.User.ID, ""); !errors.Is(err, tenancy.ErrInvalid) {
		t.Fatalf("empty hash: want ErrInvalid, got %v", err)
	}
}
