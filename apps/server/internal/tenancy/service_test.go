package tenancy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

type fixture struct {
	svc    *tenancy.Service
	app    *db.DB
	ctx    context.Context
	userID func(name string) uuid.UUID
}

func setup(t *testing.T) fixture {
	t.Helper()
	app := dbtest.New(t).OpenApp(t)
	ctx := context.Background()
	return fixture{
		svc: tenancy.New(app), app: app, ctx: ctx,
		userID: func(name string) uuid.UUID { return createUser(t, app, name) },
	}
}

// Users are global, so the app role creates them without a workspace. The
// identity module will own this; the test inserts directly for now.
func createUser(t *testing.T, d *db.DB, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	err := d.InGlobalTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			"INSERT INTO users (id, email, name) VALUES ($1, $2, $3)", id, name+"@example.test", name)
		return err
	})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	return id
}

func (f fixture) workspace(t *testing.T, slug string, owner uuid.UUID) tenancy.Workspace {
	t.Helper()
	ws, err := f.svc.CreateWorkspace(f.ctx, tenancy.CreateWorkspaceInput{Name: "WS " + slug, Slug: slug, OwnerID: owner})
	if err != nil {
		t.Fatalf("create workspace %s: %v", slug, err)
	}
	return ws
}

func TestCreateWorkspace_MakesCreatorOwner(t *testing.T) {
	f := setup(t)
	owner := f.userID("marina")

	ws := f.workspace(t, "zerno", owner)

	if ws.Locale != "ru" || ws.Timezone != "Europe/Moscow" {
		t.Fatalf("defaults not applied: %+v", ws)
	}
	members, err := f.svc.Members(f.ctx, ws.ID)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].UserID != owner || members[0].Role != tenancy.RoleOwner {
		t.Fatalf("want single owner, got %+v", members)
	}
	if members[0].MaxConcurrentChats != 3 || members[0].LineLevel != 1 {
		t.Fatalf("want capacity 3 and line 1 by default (docs/07 §10), got %+v", members[0])
	}
}

func TestCreateWorkspace_RejectsInvalidInput(t *testing.T) {
	f := setup(t)
	owner := f.userID("owner")
	tests := []struct {
		name string
		in   tenancy.CreateWorkspaceInput
	}{
		{"no owner", tenancy.CreateWorkspaceInput{Name: "A", Slug: "abc"}},
		{"short slug", tenancy.CreateWorkspaceInput{Name: "A", Slug: "ab", OwnerID: owner}},
		{"uppercase slug", tenancy.CreateWorkspaceInput{Name: "A", Slug: "Zerno", OwnerID: owner}},
		{"trailing hyphen", tenancy.CreateWorkspaceInput{Name: "A", Slug: "zerno-", OwnerID: owner}},
		{"empty name", tenancy.CreateWorkspaceInput{Slug: "zerno", OwnerID: owner}},
		{"bad locale", tenancy.CreateWorkspaceInput{Name: "A", Slug: "zerno", Locale: "de", OwnerID: owner}},
		{"bad timezone", tenancy.CreateWorkspaceInput{Name: "A", Slug: "zerno", Timezone: "Mars/Base", OwnerID: owner}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.svc.CreateWorkspace(f.ctx, tt.in); !errors.Is(err, tenancy.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestCreateWorkspace_SlugIsUniqueAcrossTenants(t *testing.T) {
	f := setup(t)
	f.workspace(t, "zerno", f.userID("a"))

	// The other workspace's row is invisible under RLS, but the unique
	// constraint still applies.
	_, err := f.svc.CreateWorkspace(f.ctx, tenancy.CreateWorkspaceInput{Name: "B", Slug: "zerno", OwnerID: f.userID("b")})
	if !errors.Is(err, tenancy.ErrSlugTaken) {
		t.Fatalf("want ErrSlugTaken, got %v", err)
	}
}

func TestAddMember(t *testing.T) {
	f := setup(t)
	ws := f.workspace(t, "zerno", f.userID("owner"))
	op := f.userID("operator")

	if err := f.svc.AddMember(f.ctx, ws.ID, op, tenancy.RoleOperator); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if err := f.svc.AddMember(f.ctx, ws.ID, op, tenancy.RoleOperator); !errors.Is(err, tenancy.ErrAlreadyMember) {
		t.Fatalf("want ErrAlreadyMember, got %v", err)
	}
	if err := f.svc.AddMember(f.ctx, ws.ID, f.userID("x"), "superuser"); !errors.Is(err, tenancy.ErrUnknownRole) {
		t.Fatalf("want ErrUnknownRole, got %v", err)
	}
	members, err := f.svc.Members(f.ctx, ws.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("want 2 members, got %d (%v)", len(members), err)
	}
}

// Mandatory isolation test (docs/00 §11.3): data of workspace A is invisible
// from workspace B through the service.
func TestIsolation_ServiceHidesOtherWorkspace(t *testing.T) {
	f := setup(t)
	a := f.workspace(t, "alpha", f.userID("a"))
	b := f.workspace(t, "bravo", f.userID("b"))

	members, err := f.svc.Members(f.ctx, b.ID)
	if err != nil {
		t.Fatalf("Members(b): %v", err)
	}
	for _, m := range members {
		if m.Email == "a@example.test" {
			t.Fatalf("member of A visible in B: %+v", m)
		}
	}
	if _, err := f.svc.Workspace(f.ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, tenancy.ErrNotFound) {
		t.Fatalf("unknown workspace: want ErrNotFound, got %v", err)
	}
	if got, err := f.svc.Workspace(f.ctx, a.ID); err != nil || got.Slug != "alpha" {
		t.Fatalf("own workspace must be visible: %+v %v", got, err)
	}
}

// RLS is the second line of defence: even a query that forgets the
// workspace_id filter sees only the current tenant, and writes into another
// tenant are rejected.
func TestIsolation_RLSBlocksQueriesWithoutFilter(t *testing.T) {
	f := setup(t)
	a := f.workspace(t, "alpha", f.userID("a"))
	b := f.workspace(t, "bravo", f.userID("b"))

	countAll := func(ws uuid.UUID, table string) int {
		var n int
		err := f.app.InTenantTx(f.ctx, ws, func(tx pgx.Tx) error {
			// Deliberately unfiltered: simulates a developer mistake.
			return tx.QueryRow(f.ctx, "SELECT count(*) FROM "+table).Scan(&n)
		})
		if err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	for _, table := range []string{"workspaces", "memberships"} {
		if n := countAll(a.ID, table); n != 1 {
			t.Errorf("%s visible from A: want 1 row, got %d", table, n)
		}
	}

	err := f.app.InTenantTx(f.ctx, a.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx,
			"INSERT INTO memberships (workspace_id, user_id, role_id) VALUES ($1, $2, '00000000-0000-7000-8000-000000000003')",
			b.ID, f.userID("intruder"))
		return err
	})
	if err == nil {
		t.Fatal("insert into another workspace must be rejected by RLS")
	}

	var visible int
	err = f.app.InGlobalTx(f.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctx, "SELECT count(*) FROM memberships").Scan(&visible)
	})
	if err != nil || visible != 0 {
		t.Fatalf("without a workspace no tenant rows may be visible: got %d (%v)", visible, err)
	}
}

func TestInTenantTx_RequiresWorkspace(t *testing.T) {
	f := setup(t)
	err := f.app.InTenantTx(f.ctx, uuid.Nil, func(pgx.Tx) error { return nil })
	if !errors.Is(err, db.ErrNoWorkspace) {
		t.Fatalf("want ErrNoWorkspace, got %v", err)
	}
}
