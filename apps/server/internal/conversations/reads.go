package conversations

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// MarkRead moves userID's read mark of the conversation up to messageID
// (docs/02 §10.5). A mark is never moved back, so a late request from
// another tab changes nothing; only a move records conversation.read, which
// the reader's other tabs use to clear their counters.
func (s *Service) MarkRead(ctx context.Context, workspaceID, userID, conversationID, messageID uuid.UUID) error {
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		ok, err := q.MessageExists(ctx, store.MessageExistsParams{WorkspaceID: workspaceID, ConversationID: conversationID, ID: messageID})
		if err != nil {
			return err
		}
		if !ok {
			// One answer for a foreign conversation and a foreign message: 404.
			if _, err := q.GetConversation(ctx, store.GetConversationParams{WorkspaceID: workspaceID, ID: conversationID}); err != nil {
				return err
			}
			return fmt.Errorf("%w: the message is not in this conversation", ErrInvalid)
		}
		moved, err := q.MarkRead(ctx, store.MarkReadParams{
			WorkspaceID: workspaceID, ConversationID: conversationID, UserID: userID, MessageID: messageID, ReadAt: s.now(),
		})
		if err != nil || moved == 0 {
			return err
		}
		return outbox.Add(ctx, tx, workspaceID, "conversation.read", map[string]any{
			"conversation_id": conversationID, "reader_type": "operator", "reader_id": userID, "up_to_message_id": messageID,
		})
	})
	return wrap(err, "mark read")
}
