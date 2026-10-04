package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Password reset and email confirmation (docs/06 A-06). The links are
// emailed by the jobs in mailjobs.go; these methods queue the jobs and use
// the tokens up.

// ForgotPassword queues a reset email when the address belongs to an
// enabled account with a password. The answer is the same either way, so it
// does not reveal registered addresses.
func (s *Service) ForgotPassword(ctx context.Context, email string, c Client) error {
	if !s.mailEnabled {
		return ErrEmailDisabled
	}
	email = strings.TrimSpace(email)
	if err := s.allow(ctx, "forgot:ip:"+digest(c.IP), forgotPerIP); err != nil {
		return err
	}
	if err := s.allow(ctx, "forgot:e:"+digest(strings.ToLower(email)), forgotPerEmail); err != nil {
		return err
	}
	creds, err := s.tenancy.Credentials(ctx, email)
	if errors.Is(err, tenancy.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if creds.Disabled {
		return nil
	}
	if _, err := s.jobs.Insert(ctx, PasswordResetEmailArgs{UserID: creds.UserID}, nil); err != nil {
		return err
	}
	return nil
}

// ResetPassword sets a new password by a reset token. The token is used up,
// the email counts as confirmed and every session of the user is revoked;
// the user signs in again with the new password.
func (s *Service) ResetPassword(ctx context.Context, token, password string, c Client) error {
	if err := s.allow(ctx, "linktoken:ip:"+digest(c.IP), linkTokenPerIP); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	if _, _, err := parseLinkToken(token); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	userID, err := s.resetTokens.consume(ctx, token)
	if err != nil {
		return err
	}
	if err := s.tenancy.SetPasswordHash(ctx, userID, hash); err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return ErrInvalidToken // disabled meanwhile
		}
		return err
	}
	if err := s.tenancy.MarkEmailVerified(ctx, userID); err != nil && !errors.Is(err, tenancy.ErrNotFound) {
		return err
	}
	_, err = s.revokeAllExcept(ctx, userID, "")
	return err
}

// VerifyEmail confirms the address by a confirmation token.
func (s *Service) VerifyEmail(ctx context.Context, token string, c Client) error {
	if err := s.allow(ctx, "linktoken:ip:"+digest(c.IP), linkTokenPerIP); err != nil {
		return err
	}
	userID, err := s.verifyTokens.consume(ctx, token)
	if err != nil {
		return err
	}
	if err := s.tenancy.MarkEmailVerified(ctx, userID); err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return ErrInvalidToken
		}
		return err
	}
	return nil
}

// ResendEmailVerification queues a new confirmation email for the user.
func (s *Service) ResendEmailVerification(ctx context.Context, userID uuid.UUID) error {
	if !s.mailEnabled {
		return ErrEmailDisabled
	}
	u, err := s.tenancy.User(ctx, userID)
	if errors.Is(err, tenancy.ErrNotFound) {
		return ErrNoSession
	}
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt != nil {
		return ErrEmailVerified
	}
	if err := s.allow(ctx, "verifyresend:u:"+userID.String(), verifyResendPerUser); err != nil {
		return err
	}
	if _, err := s.jobs.Insert(ctx, EmailVerificationArgs{UserID: userID}, nil); err != nil {
		return err
	}
	return nil
}
