package auth

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Personal account operations of a signed-in user: password change and the
// list of their sessions ("Безопасность и вход", docs/07 §7). Each takes the
// token of the current request, which is never revealed, only compared.

// ChangePassword checks the current password, sets the new one, revokes
// every other session of the user and replaces the current one. It returns
// the new token and session; the caller sets the new cookie.
//
// Postgres and Redis share no transaction. The new session is created
// before the password is written and taken back if the write fails; if
// revoking the old sessions fails afterwards, the error is returned and the
// user can retry with the new password ("sign out everywhere").
func (s *Service) ChangePassword(ctx context.Context, token string, cur Session, current, next, userAgent string) (string, Session, error) {
	if err := s.allow(ctx, "pwchange:u:"+cur.UserID.String(), passwordChangePerUser); err != nil {
		return "", Session{}, err
	}
	if err := validatePassword(next); err != nil {
		return "", Session{}, err
	}
	creds, err := s.tenancy.CredentialsByID(ctx, cur.UserID)
	if errors.Is(err, tenancy.ErrNotFound) {
		return "", Session{}, ErrNoSession // the account has gone or has no password
	}
	if err != nil {
		return "", Session{}, err
	}
	ok, err := s.hasher.Verify(ctx, current, creds.PasswordHash)
	if err != nil {
		return "", Session{}, fmt.Errorf("verify password of %s: %w", cur.UserID, err)
	}
	if !ok {
		return "", Session{}, ErrWrongPassword
	}
	if creds.Disabled {
		return "", Session{}, ErrNoSession
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return "", Session{}, err
	}

	newToken, sess, err := s.sessions.create(ctx, cur.UserID, cur.WorkspaceID, userAgent)
	if err != nil {
		return "", Session{}, err
	}
	if err := s.tenancy.SetPasswordHash(ctx, cur.UserID, hash); err != nil {
		_ = s.sessions.delete(ctx, newToken)
		if errors.Is(err, tenancy.ErrNotFound) {
			return "", Session{}, ErrNoSession
		}
		return "", Session{}, err
	}
	if _, err := s.revokeAllExcept(ctx, cur.UserID, tokenHash(newToken)); err != nil {
		return "", Session{}, err
	}
	return newToken, sess, nil
}

// DeviceSession is one live session of the user as the session list shows it.
type DeviceSession struct {
	ID         uuid.UUID
	CreatedAt  time.Time
	LastSeenAt time.Time
	UserAgent  string
	Current    bool
}

// Sessions lists the user's live sessions in every workspace: the current
// one first, then the most recently active.
func (s *Service) Sessions(ctx context.Context, token string, userID uuid.UUID) ([]DeviceSession, error) {
	list, err := s.sessions.list(ctx, userID)
	if err != nil {
		return nil, err
	}
	current := tokenHash(token)
	out := make([]DeviceSession, 0, len(list))
	for _, is := range list {
		out = append(out, DeviceSession{
			ID: is.ID, CreatedAt: is.CreatedAt, LastSeenAt: is.LastSeenAt, UserAgent: is.UserAgent,
			Current: is.hash == current,
		})
	}
	slices.SortStableFunc(out, func(a, b DeviceSession) int {
		if a.Current != b.Current {
			if a.Current {
				return -1
			}
			return 1
		}
		return cmp.Or(b.LastSeenAt.Compare(a.LastSeenAt), b.CreatedAt.Compare(a.CreatedAt))
	})
	return out, nil
}

// RevokeSession ends one session of the user by its public ID. Another
// user's session, an ended one and an unknown ID are ErrSessionNotFound;
// the current session is ErrCurrentSession (logout ends it).
func (s *Service) RevokeSession(ctx context.Context, token string, userID, id uuid.UUID) error {
	list, err := s.sessions.list(ctx, userID)
	if err != nil {
		return err
	}
	for _, is := range list {
		if is.ID != id {
			continue
		}
		if is.hash == tokenHash(token) {
			return ErrCurrentSession
		}
		return s.sessions.revoke(ctx, userID, []string{is.hash})
	}
	return ErrSessionNotFound
}

// RevokeOtherSessions ends every session of the user except the current
// one and returns how many it ended.
func (s *Service) RevokeOtherSessions(ctx context.Context, token string, userID uuid.UUID) (int, error) {
	return s.revokeAllExcept(ctx, userID, tokenHash(token))
}

func (s *Service) revokeAllExcept(ctx context.Context, userID uuid.UUID, keep string) (int, error) {
	list, err := s.sessions.list(ctx, userID)
	if err != nil {
		return 0, err
	}
	var hashes []string
	for _, is := range list {
		if is.hash != keep {
			hashes = append(hashes, is.hash)
		}
	}
	if err := s.sessions.revoke(ctx, userID, hashes); err != nil {
		return 0, err
	}
	return len(hashes), nil
}
