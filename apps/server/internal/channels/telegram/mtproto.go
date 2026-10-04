package telegram

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gotd/td/session"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// Errors of the personal-account login (ADR-0014). Telegram's own error
// names stay inside this package.
var (
	ErrPhoneInvalid    = errors.New("telegram: phone number is invalid")
	ErrNoAccount       = errors.New("telegram: no Telegram account for this phone number")
	ErrBanned          = errors.New("telegram: the account is banned or deactivated")
	ErrCodeInvalid     = errors.New("telegram: wrong login code")
	ErrCodeExpired     = errors.New("telegram: the login code has expired")
	ErrPasswordNeeded  = errors.New("telegram: the account has a cloud password")
	ErrPasswordInvalid = errors.New("telegram: wrong cloud password")
	ErrSessionRevoked  = errors.New("telegram: the session was ended in Telegram")
	ErrAppInvalid      = errors.New("telegram: api_id or api_hash is invalid")
)

// FloodWaitError asks to wait before the next attempt (FLOOD_WAIT_N).
type FloodWaitError struct{ Wait time.Duration }

func (e *FloodWaitError) Error() string { return fmt.Sprintf("telegram: flood wait %s", e.Wait) }

// Code delivery as reported by auth.sendCode.
const (
	CodeViaApp   = "app"
	CodeViaSMS   = "sms"
	CodeViaCall  = "call"
	CodeViaOther = "other"
)

// SentCode is the answer to SendCode.
type SentCode struct {
	Hash string
	Via  string
}

// Account is the signed-in personal account.
type Account struct {
	ID        int64
	FirstName string
	LastName  string
	Username  string
}

// Name is the account's display name.
func (a Account) Name() string {
	return strings.TrimSpace(a.FirstName + " " + a.LastName)
}

// phoneRE is an international number after NormalizePhone.
var phoneRE = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// NormalizePhone strips spaces, dashes, dots and brackets and checks the
// result is an international number: "+7 (900) 123-45-67" → "+79001234567".
func NormalizePhone(s string) (string, bool) {
	p := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')', '.', ' ':
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if !strings.HasPrefix(p, "+") {
		p = "+" + p
	}
	return p, phoneRE.MatchString(p)
}

// MaskPhone keeps the first two and the last two digits: "+79•••••••67".
func MaskPhone(p string) string {
	d := strings.TrimPrefix(p, "+")
	if len(d) < 6 {
		return "+" + strings.Repeat("•", len(d))
	}
	return "+" + d[:2] + strings.Repeat("•", len(d)-4) + d[len(d)-2:]
}

// UserAuth signs personal accounts in over MTProto. Every call opens a
// connection with the session it is given, makes its requests and returns
// the session as it stands afterwards; the caller stores it encrypted.
// Nothing here is logged: phones, codes, passwords and keys only pass
// through.
type UserAuth struct {
	appID   int
	appHash string
	device  gotd.DeviceConfig
	timeout time.Duration
	// options lets tests replace the transport.
	options func(gotd.Options) gotd.Options
}

// NewUserAuth returns a UserAuth for the Telegram application of the
// installation (my.telegram.org). version names the server build in
// Telegram's list of devices.
func NewUserAuth(appID int, appHash, version string) *UserAuth {
	return &UserAuth{
		appID: appID, appHash: appHash, timeout: 20 * time.Second, // inside the 30 s request timeout
		device: gotd.DeviceConfig{
			// Shown to the account owner in Settings → Devices.
			DeviceModel: "Tolkovo", SystemVersion: "server", AppVersion: version,
			SystemLangCode: "en", LangCode: "en",
		},
		options: func(o gotd.Options) gotd.Options { return o },
	}
}

// SendCode asks Telegram to send a login code for phone. sess may be nil
// for a fresh login.
func (u *UserAuth) SendCode(ctx context.Context, sess []byte, phone string) ([]byte, SentCode, error) {
	var sent SentCode
	out, err := u.run(ctx, sess, func(ctx context.Context, c *gotd.Client) error {
		res, err := c.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
		if err != nil {
			return err
		}
		code, ok := res.(*tg.AuthSentCode)
		if !ok {
			// AuthSentCodeSuccess (a stored login token) or a payment demand:
			// neither applies to a server without saved tokens.
			return fmt.Errorf("telegram: unexpected answer %T to auth.sendCode", res)
		}
		sent = SentCode{Hash: code.PhoneCodeHash, Via: codeVia(code.Type)}
		return nil
	})
	return out, sent, err
}

// SignIn completes the login with the code. ErrPasswordNeeded means the
// account has a cloud password: call Password with the returned session.
func (u *UserAuth) SignIn(ctx context.Context, sess []byte, phone, hash, code string) ([]byte, Account, error) {
	var acc Account
	out, err := u.run(ctx, sess, func(ctx context.Context, c *gotd.Client) error {
		res, err := c.Auth().SignIn(ctx, phone, code, hash)
		if errors.Is(err, auth.ErrPasswordAuthNeeded) {
			return ErrPasswordNeeded
		}
		var signUp *auth.SignUpRequired
		if errors.As(err, &signUp) {
			return ErrNoAccount
		}
		if err != nil {
			return err
		}
		acc, err = account(res)
		return err
	})
	return out, acc, err
}

// Password completes a login that needs the cloud password. The password
// is used for the SRP proof only and is not kept.
func (u *UserAuth) Password(ctx context.Context, sess []byte, password string) ([]byte, Account, error) {
	var acc Account
	out, err := u.run(ctx, sess, func(ctx context.Context, c *gotd.Client) error {
		res, err := c.Auth().Password(ctx, password)
		if errors.Is(err, auth.ErrPasswordInvalid) {
			return ErrPasswordInvalid
		}
		if err != nil {
			return err
		}
		acc, err = account(res)
		return err
	})
	return out, acc, err
}

// LogOut ends the session in Telegram; it disappears from the account's
// devices. An already ended session is not an error.
func (u *UserAuth) LogOut(ctx context.Context, sess []byte) error {
	_, err := u.run(ctx, sess, func(ctx context.Context, c *gotd.Client) error {
		_, err := c.API().AuthLogOut(ctx)
		return err
	})
	if errors.Is(err, ErrSessionRevoked) {
		return nil
	}
	return err
}

func (u *UserAuth) run(ctx context.Context, sess []byte, f func(context.Context, *gotd.Client) error) ([]byte, error) {
	return u.runFor(ctx, u.timeout, sess, f)
}

// runFor is run with its own time limit: sending a file takes longer than
// a login step.
func (u *UserAuth) runFor(ctx context.Context, timeout time.Duration, sess []byte, f func(context.Context, *gotd.Client) error) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	storage := &session.StorageMemory{}
	if len(sess) > 0 {
		if err := storage.StoreSession(ctx, sess); err != nil {
			return nil, fmt.Errorf("telegram: load session: %w", err)
		}
	}
	client := gotd.NewClient(u.appID, u.appHash, u.options(gotd.Options{
		SessionStorage: storage,
		Device:         u.device,
		NoUpdates:      true,
	}))
	err := client.Run(ctx, func(ctx context.Context) error { return f(ctx, client) })
	out, _ := storage.Bytes(nil) // empty before the first key exchange
	return out, classify(err)
}

// classify turns Telegram's RPC errors into this package's errors.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return &FloodWaitError{Wait: d}
	}
	for _, known := range []error{
		ErrPasswordNeeded, ErrPasswordInvalid, ErrNoAccount,
	} {
		if errors.Is(err, known) {
			return known
		}
	}
	switch {
	case tgerr.Is(err, "PHONE_NUMBER_INVALID", "PHONE_NUMBER_FLOOD"):
		return ErrPhoneInvalid
	case tgerr.Is(err, "PHONE_NUMBER_UNOCCUPIED"):
		return ErrNoAccount
	case tgerr.Is(err, "PHONE_NUMBER_BANNED", "USER_DEACTIVATED", "USER_DEACTIVATED_BAN"):
		return ErrBanned
	case tgerr.Is(err, "PHONE_CODE_INVALID", "PHONE_CODE_EMPTY"):
		return ErrCodeInvalid
	case tgerr.Is(err, "PHONE_CODE_EXPIRED"):
		return ErrCodeExpired
	case tgerr.Is(err, "PASSWORD_HASH_INVALID"):
		return ErrPasswordInvalid
	case tgerr.Is(err, "API_ID_INVALID", "API_ID_PUBLISHED_FLOOD"):
		return ErrAppInvalid
	// Named rather than auth.IsUnauthorized: SESSION_PASSWORD_NEEDED is a 401 too.
	case tgerr.Is(err, "AUTH_KEY_UNREGISTERED", "SESSION_REVOKED", "SESSION_EXPIRED", "AUTH_KEY_INVALID"):
		return ErrSessionRevoked
	}
	return err
}

func codeVia(t tg.AuthSentCodeTypeClass) string {
	switch t.(type) {
	case *tg.AuthSentCodeTypeApp:
		return CodeViaApp
	case *tg.AuthSentCodeTypeSMS, *tg.AuthSentCodeTypeFragmentSMS, *tg.AuthSentCodeTypeFirebaseSMS,
		*tg.AuthSentCodeTypeSMSWord, *tg.AuthSentCodeTypeSMSPhrase:
		return CodeViaSMS
	case *tg.AuthSentCodeTypeCall, *tg.AuthSentCodeTypeFlashCall, *tg.AuthSentCodeTypeMissedCall:
		return CodeViaCall
	default:
		return CodeViaOther
	}
}

func account(a *tg.AuthAuthorization) (Account, error) {
	u, ok := a.User.(*tg.User)
	if !ok {
		return Account{}, fmt.Errorf("telegram: unexpected user %T after login", a.User)
	}
	return Account{ID: u.ID, FirstName: u.FirstName, LastName: u.LastName, Username: u.Username}, nil
}
