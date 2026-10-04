package conversations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// fixture is a fresh database (application role, RLS on), local file
// storage in a temp dir and two workspaces with an owner each.
type fixture struct {
	t        *testing.T
	ctx      context.Context
	svc      *conversations.Service
	app      *db.DB
	files    *storage.FS
	ownerURL string
	tenants  *tenancy.Service
	wsA, wsB uuid.UUID
	ownerA   uuid.UUID
	ownerB   uuid.UUID
}

func setup(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.New(t)
	app := d.OpenApp(t)
	files, err := storage.NewFS(storage.FSOptions{
		Root: t.TempDir(), LinkBaseURL: "http://localhost/api/v1/files",
		SigningKey: bytes.Repeat([]byte("k"), storage.MinSigningKeyLen),
	})
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = files.Close() })
	f := &fixture{
		t: t, ctx: context.Background(), app: app, files: files, ownerURL: d.OwnerURL,
		svc: conversations.New(app, files), tenants: tenancy.New(app),
	}
	f.ownerA = f.user("anna")
	f.ownerB = f.user("boris")
	f.wsA = f.workspace("alpha", f.ownerA)
	f.wsB = f.workspace("bravo", f.ownerB)
	return f
}

// user creates a global user directly: the identity module owns users.
func (f *fixture) user(name string) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	err := f.app.InGlobalTx(f.ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, "INSERT INTO users (id, email, name) VALUES ($1, $2, $3)", id, name+"@example.test", name)
		return err
	})
	if err != nil {
		f.t.Fatalf("create user: %v", err)
	}
	return id
}

func (f *fixture) workspace(slug string, owner uuid.UUID) uuid.UUID {
	f.t.Helper()
	ws, err := f.tenants.CreateWorkspace(f.ctx, tenancy.CreateWorkspaceInput{Name: slug, Slug: slug, OwnerID: owner})
	if err != nil {
		f.t.Fatalf("create workspace: %v", err)
	}
	return ws.ID
}

func (f *fixture) member(ws uuid.UUID, name, role string) uuid.UUID {
	f.t.Helper()
	id := f.user(name)
	if err := f.tenants.AddMember(f.ctx, ws, id, role); err != nil {
		f.t.Fatalf("add member: %v", err)
	}
	return id
}

func (f *fixture) contact(ws uuid.UUID, name string) conversations.Contact {
	f.t.Helper()
	c, err := f.svc.CreateContact(f.ctx, ws, conversations.ContactInput{Name: name})
	if err != nil {
		f.t.Fatalf("create contact: %v", err)
	}
	return c
}

func (f *fixture) conversation(ws uuid.UUID) conversations.Conversation {
	f.t.Helper()
	c, err := f.svc.CreateConversation(f.ctx, ws, conversations.CreateConversationInput{ContactID: f.contact(ws, "Client").ID})
	if err != nil {
		f.t.Fatalf("create conversation: %v", err)
	}
	return c
}

// assigned returns a conversation of ws assigned to user.
func (f *fixture) assigned(ws, user uuid.UUID) conversations.Conversation {
	f.t.Helper()
	c, err := f.svc.Assign(f.ctx, ws, admin(user), f.conversation(ws).ID, &user, nil)
	if err != nil {
		f.t.Fatalf("assign: %v", err)
	}
	return c
}

// upload registers and stores a file the way a client would.
func (f *fixture) upload(ws, conv uuid.UUID, user uuid.UUID, body []byte) conversations.Attachment {
	f.t.Helper()
	sum := sha256.Sum256(body)
	up, err := f.svc.CreateUpload(f.ctx, ws, operator(user), conv, conversations.UploadInput{
		FileName: "log.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: sum[:],
	})
	if err != nil {
		f.t.Fatalf("create upload: %v", err)
	}
	f.store(up, body)
	return up.Attachment
}

// store puts the file where the signed link points.
func (f *fixture) store(up conversations.Upload, body []byte) {
	f.t.Helper()
	u, err := url.Parse(up.Request.URL)
	if err != nil {
		f.t.Fatalf("parse link: %v", err)
	}
	key := strings.TrimPrefix(u.Path, "/api/v1/files/")
	if _, err := f.files.Put(f.ctx, key, bytes.NewReader(body), storage.PutOptions{MaxSize: int64(len(body))}); err != nil {
		f.t.Fatalf("put file: %v", err)
	}
}

// ownerExec changes data behind the service's back (fixtures).
func (f *fixture) ownerExec(sql string, args ...any) {
	f.t.Helper()
	conn, err := pgx.Connect(f.ctx, f.ownerURL)
	if err != nil {
		f.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(f.ctx) }()
	if _, err := conn.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

// events returns the outbox topics of ws in order. The application role
// may only insert events, so this reads as the table owner.
func (f *fixture) events(ws uuid.UUID) []string {
	f.t.Helper()
	conn, err := pgx.Connect(f.ctx, f.ownerURL)
	if err != nil {
		f.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(f.ctx) }()
	rows, err := conn.Query(f.ctx, "SELECT topic FROM outbox_events WHERE workspace_id = $1 ORDER BY id", ws)
	if err != nil {
		f.t.Fatalf("query events: %v", err)
	}
	topics, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		f.t.Fatalf("collect events: %v", err)
	}
	return topics
}

// admin acts like an owner or admin: on any conversation.
func admin(id uuid.UUID) conversations.Actor {
	return conversations.Actor{UserID: id, AnyConversation: true, AssignOthers: true}
}

// operator acts only on their own conversations.
func operator(id uuid.UUID) conversations.Actor { return conversations.Actor{UserID: id} }
