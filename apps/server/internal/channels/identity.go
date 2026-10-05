package channels

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Identity verification of a site visitor or an app user (docs/02 §13.2,
// §11.6, docs/06 W-04). The company's backend knows who is signed in and
// signs that user's id with the channel's identity secret:
//
//	signature = hex(HMAC-SHA256(key = identity secret, message = external_id))
//
// The app channel calls the signature identity_token (AP-02), the widget
// user_hash (the name the site's JS API uses); both are this one format.
// It has no expiry: a leaked signature works until the secret is rotated,
// as for any site that embeds it in the page. Only a verified id may lead
// to an existing contact and its history; anything else is a claim.

// Limits of an identity claim.
const (
	// MaxExternalIDLength in bytes, as contacts.external_id allows.
	MaxExternalIDLength = 255
	// maxSignatureLength is generous over the 64 hex digits of a valid one,
	// so a malformed signature is unverified rather than an error, while
	// a huge one is refused before any work.
	maxSignatureLength = 128
)

// IdentityClaim is who a visitor or an app user says they are. Email and
// Name are not checked here: they are what the visitor typed, verified or
// not, and the contact resolution decides what to keep.
type IdentityClaim struct {
	// ExternalID is the user's id in the company's system.
	ExternalID string
	Email      string
	Name       string
	// UserHash (widget) and IdentityToken (app reports) are two names of
	// the same signature; give one. Both are accepted only when equal.
	UserHash      string
	IdentityToken string
}

// Identity is the result of a check.
type Identity struct {
	// Verified: the signature matched; only then is ExternalID the user's.
	Verified bool
	// ExternalID is the verified id, empty when not verified.
	ExternalID string
}

// signature returns the one signature of the claim.
func (c IdentityClaim) signature() (string, error) {
	switch {
	case c.UserHash != "" && c.IdentityToken != "" && c.UserHash != c.IdentityToken:
		return "", fmt.Errorf("%w: give user_hash or identity_token, not two different ones", ErrInvalid)
	case c.UserHash != "":
		return c.UserHash, nil
	}
	return c.IdentityToken, nil
}

// validate bounds a claim before any work. Messages name the field, never
// the value: ids and signatures are personal data or secrets.
func (c IdentityClaim) validate() error {
	sig, err := c.signature()
	switch {
	case err != nil:
		return err
	case len(c.ExternalID) > MaxExternalIDLength || !utf8.ValidString(c.ExternalID):
		return fmt.Errorf("%w: external_id must be valid text of at most %d bytes", ErrInvalid, MaxExternalIDLength)
	case len(sig) > maxSignatureLength:
		return fmt.Errorf("%w: the identity signature is too long", ErrInvalid)
	case sig != "" && c.ExternalID == "":
		return fmt.Errorf("%w: an identity signature needs external_id", ErrInvalid)
	}
	return nil
}

// signatureMatches reports whether sig is the hex HMAC-SHA256 of externalID
// under secret, comparing in constant time. A signature that is not 64 hex
// digits is just not a match.
func signatureMatches(secret, externalID, sig string) bool {
	got, err := hex.DecodeString(sig)
	if err != nil || len(got) != sha256.Size || secret == "" || externalID == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(externalID))
	return hmac.Equal(mac.Sum(nil), got)
}

// signsIdentity reports whether channels of this type have an identity
// secret: its sealed secret is an appSecret.
func signsIdentity(channelType string) bool {
	return channelType == TypeApp || channelType == TypeWidget
}

// VerifyClaim checks a claim against the identity secret of the channel
// it came through. A wrong or missing signature is not an error: the
// identity is just unverified (nothing tells a forger why). ErrInvalid
// means a malformed claim (too long, a signature without an id);
// ErrNotFound — no such channel in the workspace. Secrets and claimed
// values never appear in errors.
func (s *Service) VerifyClaim(ctx context.Context, workspaceID, channelID uuid.UUID, c IdentityClaim) (Identity, error) {
	if err := c.validate(); err != nil {
		return Identity{}, err
	}
	sig, _ := c.signature() // checked by validate
	if sig == "" {
		return Identity{}, nil
	}
	secret, err := s.identitySecret(ctx, workspaceID, channelID)
	if err != nil || !signatureMatches(secret, c.ExternalID, sig) {
		return Identity{}, err
	}
	return Identity{Verified: true, ExternalID: c.ExternalID}, nil
}

// VerifyIdentity reports whether token is the signature of externalID
// under the channel's identity secret (AP-02 reports). Without encryption
// the secret cannot be read and nothing is verified.
func (s *Service) VerifyIdentity(ctx context.Context, workspaceID, channelID uuid.UUID, externalID, token string) (bool, error) {
	id, err := s.VerifyClaim(ctx, workspaceID, channelID, IdentityClaim{ExternalID: externalID, IdentityToken: token})
	if err != nil {
		return false, err
	}
	return id.Verified, nil
}

// identitySecret returns the channel's identity secret, or "" when the
// channel has none or secrets cannot be opened (no TOLKOVO_MASTER_KEY).
func (s *Service) identitySecret(ctx context.Context, workspaceID, channelID uuid.UUID) (string, error) {
	if s.opts.Sealer == nil {
		return "", nil
	}
	ch, raw, err := s.load(ctx, workspaceID, channelID)
	if err != nil {
		return "", err
	}
	if !signsIdentity(ch.Type) || raw == nil {
		return "", nil
	}
	var sec appSecret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return "", fmt.Errorf("decode identity secret: %w", err)
	}
	return sec.IdentitySecret, nil
}
