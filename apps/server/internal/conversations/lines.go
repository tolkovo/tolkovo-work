package conversations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Lines of support L1/L2/L3 (ADR-0021). A conversation without a line is
// on the first line: new ones and those from the AI bot land there.
// Escalation moves it to a line of a higher level, and back; the router
// gives a conversation of a line to its members only. line_level of the
// conversation is its line's level, 1 on the first line.

// Limits of lines.
const (
	MaxLines          = 30
	MaxLineNameLength = 60
	MaxLineLevel      = 3
	// MaxEscalationReason bounds the reason, which becomes a note.
	MaxEscalationReason = 1000
)

// ErrLineExists is a line whose name is taken.
var ErrLineExists = errors.New("conversations: line name is taken")

// Line is a line of support.
type Line struct {
	ID    uuid.UUID
	Name  string
	Level int
	// MemberIDs get its conversations from automatic distribution.
	MemberIDs []uuid.UUID
}

// LineInput is a new or changed line.
type LineInput struct {
	Name      string
	Level     int
	MemberIDs []uuid.UUID
}

func (in LineInput) valid() (LineInput, error) {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	ids := slices.Clone(in.MemberIDs)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	switch {
	case in.Name == "":
		return in, fmt.Errorf("%w: name is required", ErrInvalid)
	case utf8.RuneCountInString(in.Name) > MaxLineNameLength:
		return in, fmt.Errorf("%w: name must be at most %d characters", ErrInvalid, MaxLineNameLength)
	case in.Level < 1 || in.Level > MaxLineLevel:
		return in, fmt.Errorf("%w: level must be between 1 and %d", ErrInvalid, MaxLineLevel)
	case len(slices.Compact(ids)) != len(in.MemberIDs):
		return in, fmt.Errorf("%w: member ids must be unique", ErrInvalid)
	}
	if in.MemberIDs == nil {
		in.MemberIDs = []uuid.UUID{}
	}
	return in, nil
}

// Lines lists the lines of the workspace by level, then name.
func (s *Service) Lines(ctx context.Context, workspaceID uuid.UUID) ([]Line, error) {
	var out []Line
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		rows, err := q.ListLines(ctx, store.ListLinesParams{WorkspaceID: workspaceID, Lim: MaxLines})
		out = make([]Line, 0, len(rows))
		for _, r := range rows {
			out = append(out, lineFrom(r.Line, r.MemberIds))
		}
		return err
	})
	if err != nil {
		return nil, wrap(err, "list lines")
	}
	return out, nil
}

// CreateLine adds a line. A name already taken, compared
// case-insensitively, is ErrLineExists.
func (s *Service) CreateLine(ctx context.Context, workspaceID uuid.UUID, in LineInput) (Line, error) {
	in, err := in.valid()
	if err != nil {
		return Line{}, err
	}
	id, err := newID()
	if err != nil {
		return Line{}, err
	}
	var out Line
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		n, err := q.CountLines(ctx, workspaceID)
		if err != nil {
			return err
		}
		if n >= MaxLines {
			return fmt.Errorf("%w: a workspace has at most %d lines", ErrInvalid, MaxLines)
		}
		row, err := q.CreateLine(ctx, store.CreateLineParams{
			ID: id, WorkspaceID: workspaceID, Name: in.Name, Level: int16(in.Level), Now: s.now(), //nolint:gosec // G115: checked to be 1..3
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLineExists
		}
		if err != nil {
			return err
		}
		out, err = setLineMembers(ctx, q, row, in.MemberIDs)
		return err
	})
	if err != nil {
		return Line{}, lineError(err, "create line")
	}
	return out, nil
}

// UpdateLine changes a line; its conversations take the new level.
func (s *Service) UpdateLine(ctx context.Context, workspaceID, id uuid.UUID, in LineInput) (Line, error) {
	in, err := in.valid()
	if err != nil {
		return Line{}, err
	}
	var out Line
	err = s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.UpdateLine(ctx, store.UpdateLineParams{
			Name: in.Name, Level: int16(in.Level), Now: s.now(), WorkspaceID: workspaceID, ID: id, //nolint:gosec // G115: checked to be 1..3
		})
		if err != nil {
			return err
		}
		if err := q.SetLineLevelOf(ctx, store.SetLineLevelOfParams{Level: row.Level, WorkspaceID: workspaceID, LineID: &id}); err != nil {
			return err
		}
		if err := q.ClearLineMembers(ctx, store.ClearLineMembersParams{WorkspaceID: workspaceID, LineID: id}); err != nil {
			return err
		}
		if out, err = setLineMembers(ctx, q, row, in.MemberIDs); err != nil {
			return err
		}
		// Members changed: the queue may now go to someone.
		return s.queued(ctx, tx, workspaceID, nil)
	})
	if err != nil {
		return Line{}, lineError(err, "update line")
	}
	return out, nil
}

// DeleteLine removes a line: its conversations go back to the first line.
func (s *Service) DeleteLine(ctx context.Context, workspaceID, id uuid.UUID) error {
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		if err := q.SetLineLevelOf(ctx, store.SetLineLevelOfParams{Level: 1, WorkspaceID: workspaceID, LineID: &id, Release: true}); err != nil {
			return err
		}
		n, err := q.DeleteLine(ctx, store.DeleteLineParams{WorkspaceID: workspaceID, ID: id})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.queued(ctx, tx, workspaceID, nil)
	})
	return wrap(err, "delete line")
}

// Escalate moves a conversation to lineID, nil for the first line, by
// actor: up to a line of a higher level with a reason, or across and down,
// where the reason is optional. It leaves its assignee and waits in the
// queue of the line; the reason becomes a note after the feed line. Only
// the assignee escalates their conversation; AssignOthers any open one.
func (s *Service) Escalate(
	ctx context.Context, workspaceID uuid.UUID, actor Actor, conversationID uuid.UUID, lineID *uuid.UUID, reason string, version *int,
) (Conversation, error) {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > MaxEscalationReason {
		return Conversation{}, fmt.Errorf("%w: reason must be at most %d characters", ErrInvalid, MaxEscalationReason)
	}
	var out Conversation
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		if version != nil && *version != int(row.Version) {
			return ErrConflict
		}
		cur := state{Status: Status(row.Status), AssigneeID: row.AssigneeID, ResolvedAt: row.ResolvedAt, ClosedAt: row.ClosedAt}
		if cur.Status != StatusQueued && !inWork(cur.Status) {
			return fmt.Errorf("%w: a %s conversation cannot change its line", ErrTransition, cur.Status)
		}
		if !actor.AssignOthers && !sameID(cur.AssigneeID, &actor.UserID) {
			return ErrNotAssignee
		}
		if sameID(row.LineID, lineID) {
			return fmt.Errorf("%w: the conversation is on this line already", ErrInvalid)
		}
		level := int16(1)
		if lineID != nil {
			l, err := q.GetLine(ctx, store.GetLineParams{WorkspaceID: workspaceID, ID: *lineID})
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: the line does not exist", ErrInvalid)
			}
			if err != nil {
				return err
			}
			level = l.Level
		}
		if level > row.LineLevel && reason == "" {
			return fmt.Errorf("%w: a reason is required to escalate to a higher line", ErrInvalid)
		}
		if err := q.SetConversationLine(ctx, store.SetConversationLineParams{
			LineID: lineID, LineLevel: level, WorkspaceID: workspaceID, ID: row.ID,
		}); err != nil {
			return err
		}
		row.Version++
		if err := addEvent(ctx, q, tx, row, actor.UserID, Event{
			Type: EventLineChanged, From: cur.Status, To: StatusQueued, LineID: lineID, PreviousLineID: row.LineID,
			AssigneeID: nil, PreviousAssigneeID: cur.AssigneeID,
		}, s.now()); err != nil {
			return err
		}
		if reason != "" {
			if _, err := insertMessage(ctx, q, tx, row, actor, MessageInput{Body: reason, Private: true}, s.now()); err != nil {
				return err
			}
		}
		if err := outbox.Add(ctx, tx, workspaceID, "conversation.line_changed", map[string]any{
			"conversation_id": row.ID, "line_id": lineID,
		}); err != nil {
			return err
		}
		if cur.Status == StatusQueued {
			// Nobody had it: only who may take it changed.
			if err := s.queued(ctx, tx, workspaceID, nil); err != nil {
				return err
			}
		} else {
			next, err := assign(cur, nil, s.now())
			if err != nil {
				return err
			}
			n, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
				WorkspaceID: workspaceID, ID: row.ID, Version: row.Version, Status: string(next.Status),
				AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
			})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrConflict // cannot happen under the row lock
			}
			// The feed line above tells the change; recordChange would add a
			// second one.
			if err := s.addStateEvents(ctx, tx, row, actor.UserID, cur, next); err != nil {
				return err
			}
		}
		if err := s.waitsInQueue(ctx, tx, workspaceID, row.ID, lineID, row.TopicID); err != nil {
			return err
		}
		out, err = getConversation(ctx, q, workspaceID, actor.UserID, row.ID)
		return err
	})
	if err != nil {
		return Conversation{}, wrap(err, "escalate")
	}
	return out, nil
}

func setLineMembers(ctx context.Context, q *store.Queries, row store.Line, ids []uuid.UUID) (Line, error) {
	if len(ids) > 0 {
		n, err := q.AddLineMembers(ctx, store.AddLineMembersParams{LineID: row.ID, WorkspaceID: row.WorkspaceID, UserIds: ids})
		if err != nil {
			return Line{}, err
		}
		if n != int64(len(ids)) {
			return Line{}, fmt.Errorf("%w: a member is not an active member of the workspace", ErrInvalid)
		}
	}
	members, err := q.LineMemberIDs(ctx, store.LineMemberIDsParams{WorkspaceID: row.WorkspaceID, LineID: row.ID})
	if err != nil {
		return Line{}, err
	}
	slices.SortFunc(members, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return lineFrom(row, members), nil
}

// lineMembers reads the members of the lines of rows, by line.
func lineMembers(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, rows []store.Conversation) (map[uuid.UUID][]uuid.UUID, error) {
	var ids []uuid.UUID
	for _, r := range rows {
		if r.LineID != nil && !slices.Contains(ids, *r.LineID) {
			ids = append(ids, *r.LineID)
		}
	}
	out := map[uuid.UUID][]uuid.UUID{}
	if len(ids) == 0 {
		return out, nil
	}
	got, err := q.LineMembersOf(ctx, store.LineMembersOfParams{WorkspaceID: workspaceID, LineIds: ids})
	for _, m := range got {
		out[m.LineID] = append(out[m.LineID], m.UserID)
	}
	return out, err
}

// lineError turns a name another line has (a rename) into ErrLineExists.
func lineError(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrLineExists
	}
	return wrap(err, what)
}

func lineFrom(l store.Line, members []uuid.UUID) Line {
	if members == nil {
		members = []uuid.UUID{}
	}
	return Line{ID: l.ID, Name: l.Name, Level: int(l.Level), MemberIDs: members}
}
