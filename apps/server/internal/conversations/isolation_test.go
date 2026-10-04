package conversations_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// Tenant isolation (docs/00 §11.3): data of workspace A is invisible and
// untouchable from workspace B through every method of the facade.
func TestIsolation_FacadeAnswersNotFoundAcrossWorkspaces(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)
	att := f.upload(f.wsA, c.ID, f.ownerA, []byte("log"))
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.MessageInput{
		Body: "secret", AttachmentIDs: []uuid.UUID{att.ID},
	}); err != nil {
		t.Fatalf("post: %v", err)
	}
	tag, err := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, conversations.TagInput{Name: "vip"})
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	b := admin(f.ownerB)
	name := "hijack"
	feed, _ := f.svc.Messages(f.ctx, f.wsA, c.ID, "", 1)

	calls := map[string]func() error{
		"Contact": func() error { _, err := f.svc.Contact(f.ctx, f.wsB, c.ContactID); return err },
		"UpdateContact": func() error {
			_, err := f.svc.UpdateContact(f.ctx, f.wsB, c.ContactID, conversations.ContactPatch{Name: &name})
			return err
		},
		"Conversation": func() error { _, err := f.svc.Conversation(f.ctx, f.wsB, f.ownerB, c.ID); return err },
		"Transition": func() error {
			_, err := f.svc.Transition(f.ctx, f.wsB, b, c.ID, conversations.StatusResolved, nil)
			return err
		},
		"Assign":   func() error { _, err := f.svc.Assign(f.ctx, f.wsB, b, c.ID, &f.ownerB, nil); return err },
		"Messages": func() error { _, err := f.svc.Messages(f.ctx, f.wsB, c.ID, "", 10); return err },
		"PostMessage": func() error {
			_, _, err := f.svc.PostMessage(f.ctx, f.wsB, b, c.ID, conversations.MessageInput{Body: "x"})
			return err
		},
		"DownloadLink": func() error { _, err := f.svc.DownloadLink(f.ctx, f.wsB, att.ID); return err },
		"SetTags": func() error {
			_, err := f.svc.SetTags(f.ctx, f.wsB, f.ownerB, c.ID, nil, nil)
			return err
		},
		"MarkRead": func() error { return f.svc.MarkRead(f.ctx, f.wsB, f.ownerB, c.ID, feed.Items[0].ID) },
		"CreateUpload": func() error {
			_, err := f.svc.CreateUpload(f.ctx, f.wsB, b, c.ID, conversations.UploadInput{FileName: "a.txt", ContentType: "text/plain", Size: 1})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, conversations.ErrNotFound) {
			t.Errorf("%s from B: want ErrNotFound, got %v", name, err)
		}
	}

	contacts, err := f.svc.Contacts(f.ctx, f.wsB, "", 10)
	if err != nil || len(contacts.Items) != 0 {
		t.Errorf("B lists A's contacts: %+v %v", contacts.Items, err)
	}
	convs, err := f.svc.Conversations(f.ctx, f.wsB, conversations.ListFilter{})
	if err != nil || len(convs.Items) != 0 {
		t.Errorf("B lists A's conversations: %+v %v", convs.Items, err)
	}
	history, err := f.svc.Conversations(f.ctx, f.wsB, conversations.ListFilter{ContactID: &c.ContactID})
	if err != nil || len(history.Items) != 0 {
		t.Errorf("B reads the history of A's contact: %+v %v", history.Items, err)
	}
	// B's own conversation cannot carry A's tag, and B does not see A's tags.
	if _, err := f.svc.SetTags(f.ctx, f.wsB, f.ownerB, f.conversation(f.wsB).ID, []uuid.UUID{tag.ID}, nil); !errors.Is(err, conversations.ErrInvalid) {
		t.Errorf("A's tag on B's conversation: %v", err)
	}
	if tags, err := f.svc.Tags(f.ctx, f.wsB); err != nil || len(tags) != 0 {
		t.Errorf("B lists A's tags: %+v %v", tags, err)
	}
	// Nothing in A changed.
	if got, _ := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, c.ID); got.Version != c.Version+1 || got.Status != conversations.StatusAssigned {
		t.Errorf("A's conversation changed: %+v", got)
	}
}

// RLS is the second line of defence: even raw SQL in B's transaction sees
// no rows of A, cannot write rows for A, and the composite foreign keys
// keep B's rows from pointing at A's.
func TestIsolation_RLSAndCompositeKeys(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)
	att := f.upload(f.wsA, c.ID, f.ownerA, []byte("log"))
	m, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.MessageInput{Body: "hi", AttachmentIDs: []uuid.UUID{att.ID}})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	tag, err := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, conversations.TagInput{Name: "vip"})
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if _, err := f.svc.SetTags(f.ctx, f.wsA, f.ownerA, c.ID, []uuid.UUID{tag.ID}, nil); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	if err := f.svc.MarkRead(f.ctx, f.wsA, f.ownerA, c.ID, m.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	for _, table := range []string{
		"contacts", "conversations", "conversation_counters", "messages", "attachments",
		"tags", "conversation_tags", "conversation_reads",
	} {
		var n int
		err := f.app.InTenantTx(f.ctx, f.wsB, func(tx pgx.Tx) error {
			// Table names come from the fixed list above.
			return tx.QueryRow(f.ctx, "SELECT count(*) FROM "+table).Scan(&n)
		})
		if err != nil || n != 0 {
			t.Errorf("B sees %d rows of %s (%v)", n, table, err)
		}
	}

	// The SQLSTATE proves the right guard fired: 42501 RLS policy,
	// 23503 composite foreign key, 23514 check constraint.
	rejected := map[string]struct {
		code string
		sql  string
		args []any
	}{
		"contact for A": {"42501", "INSERT INTO contacts (id, workspace_id) VALUES ($1, $2)",
			[]any{uuid.Must(uuid.NewV7()), f.wsA}},
		"B's conversation with A's contact": {"23503",
			"INSERT INTO conversations (id, workspace_id, number, contact_id, status) VALUES ($1, $2, 99, $3, 'queued')",
			[]any{uuid.Must(uuid.NewV7()), f.wsB, c.ContactID}},
		"B's conversation assigned to A's owner": {"23503",
			"INSERT INTO conversations (id, workspace_id, number, contact_id, status, assignee_id) VALUES ($1, $2, 99, $3, 'assigned', $4)",
			[]any{uuid.Must(uuid.NewV7()), f.wsB, f.contact(f.wsB, "own").ID, f.ownerA}},
		"B's message in A's conversation": {"23503",
			"INSERT INTO messages (id, workspace_id, conversation_id, direction, sender_type, kind) VALUES ($1, $2, $3, 'out', 'system', 'event')",
			[]any{uuid.Must(uuid.NewV7()), f.wsB, c.ID}},
		"tag for A": {"42501", "INSERT INTO tags (id, workspace_id, name) VALUES ($1, $2, 'x')",
			[]any{uuid.Must(uuid.NewV7()), f.wsA}},
		"B's tag link to A's conversation and tag": {"23503",
			"INSERT INTO conversation_tags (workspace_id, conversation_id, tag_id) VALUES ($1, $2, $3)",
			[]any{f.wsB, c.ID, tag.ID}},
		"B's read mark on A's message": {"23503",
			"INSERT INTO conversation_reads (workspace_id, conversation_id, user_id, last_read_message_id) VALUES ($1, $2, $3, $4)",
			[]any{f.wsB, f.conversation(f.wsB).ID, f.ownerB, m.ID}},
		"B's attachment with a key of A": {"23514",
			"INSERT INTO attachments (id, workspace_id, conversation_id, storage_key, file_name, mime, size) VALUES ($1, $2, $3, $4, 'a', 'text/plain', 1)",
			[]any{uuid.Must(uuid.NewV7()), f.wsB, f.conversation(f.wsB).ID, "ws/" + f.wsA.String() + "/conv/" + uuid.NewString()}},
	}
	for name, q := range rejected {
		err := f.app.InTenantTx(f.ctx, f.wsB, func(tx pgx.Tx) error {
			_, err := tx.Exec(f.ctx, q.sql, q.args...)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != q.code {
			t.Errorf("%s: want SQLSTATE %s, got %v", name, q.code, err)
		}
	}

	// Updates from B touch nothing of A.
	err = f.app.InTenantTx(f.ctx, f.wsB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(f.ctx, "UPDATE conversations SET subject = 'x' WHERE id = $1", c.ID)
		if err == nil && tag.RowsAffected() != 0 {
			t.Errorf("B updated %d of A's conversations", tag.RowsAffected())
		}
		return err
	})
	if err != nil {
		t.Fatalf("update from B: %v", err)
	}
}
