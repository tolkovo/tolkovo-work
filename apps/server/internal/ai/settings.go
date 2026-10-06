package ai

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/httpx"
)

// Limits of the settings.
const (
	// MaxPriceMicros: a price per million tokens up to 1000 US dollars.
	MaxPriceMicros = 1_000_000_000
	// MaxBudgetMicros: a monthly budget up to a million US dollars.
	MaxBudgetMicros = 1_000_000_000_000
	maxKeyChars     = 500
	maxBaseURLChars = 500
)

var modelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\-]{0,99}$`)

// Prices of a model per million tokens, in millionths of a US dollar.
type Prices struct {
	InputMicros  int64
	OutputMicros int64
}

// Model is an entry of the built-in catalog: models the panel suggests,
// with their list prices when the catalog was written. The admin can
// change the prices and type any other model.
type Model struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Prices   Prices `json:"-"`
}

//go:embed models.json
var modelsJSON []byte

var catalog = func() []Model {
	var raw []struct {
		Provider    string `json:"provider"`
		Model       string `json:"model"`
		InputPrice  int64  `json:"input_price_micros"`
		OutputPrice int64  `json:"output_price_micros"`
	}
	if err := json.Unmarshal(modelsJSON, &raw); err != nil {
		panic(fmt.Sprintf("ai: models.json: %v", err))
	}
	out := make([]Model, 0, len(raw))
	for _, m := range raw {
		out = append(out, Model{Provider: m.Provider, Model: m.Model, Prices: Prices{InputMicros: m.InputPrice, OutputMicros: m.OutputPrice}})
	}
	return out
}()

// Catalog returns the built-in models.
func Catalog() []Model { return append([]Model(nil), catalog...) }

// Settings are what the panel sees of the workspace's AI.
type Settings struct {
	Enabled bool
	// Provider is "" until chosen.
	Provider string
	Model    string
	// BaseURL is the server of openai_compatible.
	BaseURL string
	// KeyHint is "…" and the key's last four characters; "" without a key.
	KeyHint string
	// KeySetAt is when the key was saved.
	KeySetAt *time.Time
	Prices   Prices
	// MonthlyBudgetMicros is nil without a limit.
	MonthlyBudgetMicros *int64
	// SpentMicros is the spending since PeriodStart, the first day of the
	// month (UTC).
	SpentMicros int64
	PeriodStart time.Time
	UpdatedAt   time.Time
}

// SettingsUpdate replaces the settings.
type SettingsUpdate struct {
	Enabled  bool
	Provider string
	Model    string
	BaseURL  string
	// Key replaces the key; nil keeps it. A key belongs to its provider:
	// choosing another provider without a new key forgets the old one.
	Key                 *crypto.Secret
	Prices              Prices
	MonthlyBudgetMicros *int64
}

// Settings returns the workspace's AI settings and the month's spending.
func (s *Service) Settings(ctx context.Context, workspaceID uuid.UUID) (Settings, error) {
	var out Settings
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetSettings(ctx, workspaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			row = store.AiSetting{WorkspaceID: workspaceID}
		} else if err != nil {
			return err
		}
		out, err = s.settingsFrom(ctx, q, row)
		return err
	})
	if err != nil {
		return Settings{}, fmt.Errorf("get ai settings: %w", err)
	}
	return out, nil
}

// UpdateSettings checks and saves the settings; a new key is sealed first.
func (s *Service) UpdateSettings(ctx context.Context, workspaceID, actorID uuid.UUID, in SettingsUpdate) (Settings, error) {
	in.Model = strings.TrimSpace(in.Model)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if err := s.validate(in); err != nil {
		return Settings{}, err
	}
	var (
		newKeyID uuid.UUID
		sealed   []byte
		hint     string
	)
	if in.Key != nil {
		if s.opts.Sealer == nil {
			return Settings{}, ErrNoEncryption
		}
		var err error
		if newKeyID, err = uuid.NewV7(); err != nil {
			return Settings{}, err
		}
		if sealed, err = s.opts.Sealer.Seal(ctx, []byte(in.Key.Reveal()), crypto.AAD{WorkspaceID: workspaceID, SecretID: newKeyID}); err != nil {
			return Settings{}, fmt.Errorf("seal ai key: %w", err)
		}
		hint = in.Key.Mask()
	}
	now := s.now()
	var out Settings
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.EnsureSettings(ctx, workspaceID); err != nil {
			return err
		}
		row, err := q.GetSettingsForUpdate(ctx, workspaceID)
		if err != nil {
			return err
		}
		credID := row.CredentialID
		switch {
		case in.Key != nil:
			if err := q.InsertCredential(ctx, store.InsertCredentialParams{
				ID: newKeyID, WorkspaceID: workspaceID, Provider: in.Provider, Sealed: sealed, Hint: hint,
				CreatedBy: &actorID, CreatedAt: now,
			}); err != nil {
				return err
			}
			credID = &newKeyID
		case credID != nil:
			cred, err := q.GetCredential(ctx, store.GetCredentialParams{WorkspaceID: workspaceID, ID: *credID})
			if err != nil {
				return err
			}
			if cred.Provider != in.Provider || !sameServer(deref(row.BaseUrl), in.BaseURL) {
				credID = nil // the old key opens another provider's API, or goes to another server
			}
		}
		if in.Enabled && credID == nil && in.Provider != llm.OpenAICompatible {
			return ErrKeyRequired
		}
		row, err = q.UpdateSettings(ctx, store.UpdateSettingsParams{
			Enabled: in.Enabled, Provider: nullable(in.Provider), Model: nullable(in.Model), BaseUrl: nullable(in.BaseURL),
			CredentialID: credID, InputPriceMicros: in.Prices.InputMicros, OutputPriceMicros: in.Prices.OutputMicros,
			MonthlyBudgetMicros: in.MonthlyBudgetMicros, UpdatedAt: now, WorkspaceID: workspaceID,
		})
		if err != nil {
			return err
		}
		if err := q.DeleteCredentialsExcept(ctx, store.DeleteCredentialsExceptParams{WorkspaceID: workspaceID, Keep: credID}); err != nil {
			return err
		}
		out, err = s.settingsFrom(ctx, q, row)
		return err
	})
	if err != nil {
		return Settings{}, fmt.Errorf("update ai settings: %w", err)
	}
	return out, nil
}

// secureEnough: the key goes over https, or over http only to a server
// inside the network: a private address, localhost, a name without a dot
// (a service of docker compose) or a local domain. The dial-time check
// still decides which private addresses may be reached.
func secureEnough(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		return httpx.Blocked(ip.Unmap())
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// sameServer: two base URLs reach the same server (scheme and host); a
// saved key does not follow the settings to another one.
func sameServer(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host)
}

func (s *Service) validate(in SettingsUpdate) error {
	switch in.Provider {
	case "":
		if in.Enabled || in.Model != "" || in.BaseURL != "" || in.Key != nil {
			return fmt.Errorf("%w: choose a provider", ErrInvalid)
		}
	case llm.Anthropic, llm.OpenAI, llm.DeepSeek, llm.OpenAICompatible:
	default:
		return fmt.Errorf("%w: provider must be anthropic, openai, deepseek or openai_compatible", ErrInvalid)
	}
	if in.Model != "" && !modelRE.MatchString(in.Model) {
		return fmt.Errorf("%w: model must be a model id of up to 100 characters", ErrInvalid)
	}
	if in.Enabled && in.Model == "" {
		return fmt.Errorf("%w: choose a model to turn AI on", ErrInvalid)
	}
	switch {
	case in.Provider == llm.OpenAICompatible && in.BaseURL == "":
		return fmt.Errorf("%w: an OpenAI-compatible server needs its base URL", ErrInvalid)
	case in.Provider != llm.OpenAICompatible && in.BaseURL != "":
		return fmt.Errorf("%w: base_url is only for an OpenAI-compatible server", ErrInvalid)
	case in.BaseURL != "" && (utf8.RuneCountInString(in.BaseURL) > maxBaseURLChars || httpx.ValidateURLAllowing(in.BaseURL, s.opts.AllowPrivate) != nil):
		return fmt.Errorf("%w: base_url must be a public http(s) URL without a password", ErrInvalid)
	case in.BaseURL != "" && !secureEnough(in.BaseURL):
		return fmt.Errorf("%w: base_url must be https, http only for a server inside the network", ErrInvalid)
	}
	if in.Key != nil {
		k := in.Key.Reveal()
		if k == "" || utf8.RuneCountInString(k) > maxKeyChars || strings.IndexFunc(k, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return fmt.Errorf("%w: the API key must be 1 to %d characters without spaces", ErrInvalid, maxKeyChars)
		}
	}
	if in.Prices.InputMicros < 0 || in.Prices.InputMicros > MaxPriceMicros || in.Prices.OutputMicros < 0 || in.Prices.OutputMicros > MaxPriceMicros {
		return fmt.Errorf("%w: a price per million tokens must be 0 to 1000 US dollars", ErrInvalid)
	}
	if b := in.MonthlyBudgetMicros; b != nil && (*b < 0 || *b > MaxBudgetMicros) {
		return fmt.Errorf("%w: the monthly budget must be 0 to 1000000 US dollars", ErrInvalid)
	}
	return nil
}

func (s *Service) settingsFrom(ctx context.Context, q *store.Queries, row store.AiSetting) (Settings, error) {
	start := monthStart(s.now())
	spent, err := q.SpentSince(ctx, store.SpentSinceParams{WorkspaceID: row.WorkspaceID, Since: start})
	if err != nil {
		return Settings{}, err
	}
	out := Settings{
		Enabled: row.Enabled, Provider: deref(row.Provider), Model: deref(row.Model), BaseURL: deref(row.BaseUrl),
		Prices:              Prices{InputMicros: row.InputPriceMicros, OutputMicros: row.OutputPriceMicros},
		MonthlyBudgetMicros: row.MonthlyBudgetMicros, SpentMicros: spent, PeriodStart: start, UpdatedAt: row.UpdatedAt,
	}
	if row.CredentialID != nil {
		cred, err := q.GetCredential(ctx, store.GetCredentialParams{WorkspaceID: row.WorkspaceID, ID: *row.CredentialID})
		if err != nil {
			return Settings{}, err
		}
		out.KeyHint, out.KeySetAt = cred.Hint, &cred.CreatedAt
	}
	return out, nil
}

// monthStart is the first instant of t's month in UTC: budgets are monthly.
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
