package tenancy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy/store"
)

// Personal access tokens (ADR-0016). This module keeps them; internal/auth
// makes the token, hashes it and checks it on every request.

// MaxAccessTokens is how many live tokens a member may have.
const MaxAccessTokens = 20

// ErrTooManyTokens is returned when a member already has MaxAccessTokens.
var ErrTooManyTokens = errors.New("tenancy: too many access tokens")

// AccessToken describes a token without its secret.
type AccessToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	UserName   string
	Name       string
	Scopes     []string
	Prefix     string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
}

// NewAccessToken is a token to keep: auth made the secret, only its
// SHA-256 and first characters come here.
type NewAccessToken struct {
	UserID    uuid.UUID
	Name      string
	Scopes    []string
	Hash      []byte
	Prefix    string
	CreatedAt time.Time
	ExpiresAt *time.Time
}

// TokenGrant is what a token lets its holder do.
type TokenGrant struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Scopes []string
}

// AddAccessToken keeps a new token of an active member, within
// MaxAccessTokens. Expired tokens of the member are removed on the way.
func (s *Service) AddAccessToken(ctx context.Context, workspaceID uuid.UUID, in NewAccessToken) (AccessToken, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return AccessToken{}, err
	}
	var out AccessToken
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockMemberTokens(ctx, store.LockMemberTokensParams{WorkspaceID: workspaceID, UserID: in.UserID}); err != nil {
			return err
		}
		if err := q.DropExpiredAccessTokens(ctx, store.DropExpiredAccessTokensParams{
			WorkspaceID: workspaceID, UserID: in.UserID, Now: in.CreatedAt,
		}); err != nil {
			return err
		}
		n, err := q.CountLiveAccessTokens(ctx, store.CountLiveAccessTokensParams{WorkspaceID: workspaceID, UserID: in.UserID, Now: in.CreatedAt})
		if err != nil {
			return err
		}
		if n >= MaxAccessTokens {
			return ErrTooManyTokens
		}
		row, err := q.InsertAccessToken(ctx, store.InsertAccessTokenParams{
			ID: id, WorkspaceID: workspaceID, UserID: in.UserID, Name: in.Name, Scopes: in.Scopes,
			TokenHash: in.Hash, Prefix: in.Prefix, CreatedAt: in.CreatedAt, ExpiresAt: in.ExpiresAt,
		})
		if err != nil {
			return err
		}
		out = AccessToken{
			ID: row.ID, UserID: row.UserID, Name: row.Name, Scopes: row.Scopes, Prefix: row.Prefix,
			CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, LastUsedAt: row.LastUsedAt,
		}
		return nil
	})
	if isForeignKey(err) {
		return AccessToken{}, ErrNotFound // not a member of the workspace
	}
	if err != nil {
		return AccessToken{}, fmt.Errorf("add access token: %w", err)
	}
	return out, nil
}

// AccessTokens returns the live tokens of the workspace, newest first; of
// one member when userID is set.
func (s *Service) AccessTokens(ctx context.Context, workspaceID uuid.UUID, userID *uuid.UUID, now time.Time) ([]AccessToken, error) {
	var out []AccessToken
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListAccessTokens(ctx, store.ListAccessTokensParams{
			WorkspaceID: workspaceID, UserID: optUUID(userID), Now: now,
		})
		if err != nil {
			return err
		}
		out = make([]AccessToken, 0, len(rows))
		for _, r := range rows {
			out = append(out, AccessToken{
				ID: r.ID, UserID: r.UserID, UserName: r.UserName, Name: r.Name, Scopes: r.Scopes, Prefix: r.Prefix,
				CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, LastUsedAt: r.LastUsedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list access tokens: %w", err)
	}
	return out, nil
}

// DeleteAccessToken revokes a token: one of userID's when it is set, any
// token of the workspace otherwise. An unknown token is ErrNotFound.
func (s *Service) DeleteAccessToken(ctx context.Context, workspaceID, id uuid.UUID, userID *uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		n, err := store.New(tx).DeleteAccessToken(ctx, store.DeleteAccessTokenParams{
			WorkspaceID: workspaceID, ID: id, UserID: optUUID(userID),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil {
		return fmt.Errorf("delete access token: %w", err)
	}
	return nil
}

// AccessTokenGrant finds a live token by its hash inside the workspace the
// token names, and notes its use at most once per touchEvery. An unknown or
// expired token is ErrNotFound.
func (s *Service) AccessTokenGrant(ctx context.Context, workspaceID uuid.UUID, hash []byte, now time.Time, touchEvery time.Duration) (TokenGrant, error) {
	var out TokenGrant
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetAccessTokenByHash(ctx, store.GetAccessTokenByHashParams{WorkspaceID: workspaceID, TokenHash: hash})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.ExpiresAt != nil && !now.Before(*row.ExpiresAt)) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = TokenGrant{ID: row.ID, UserID: row.UserID, Scopes: row.Scopes}
		if row.LastUsedAt != nil && now.Sub(*row.LastUsedAt) < touchEvery {
			return nil
		}
		return q.TouchAccessToken(ctx, store.TouchAccessTokenParams{WorkspaceID: workspaceID, ID: row.ID, LastUsedAt: &now})
	})
	if errors.Is(err, ErrNotFound) {
		return TokenGrant{}, err
	}
	if err != nil {
		return TokenGrant{}, fmt.Errorf("access token grant: %w", err)
	}
	return out, nil
}

func isForeignKey(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func optUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}
