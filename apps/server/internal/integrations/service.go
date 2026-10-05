// Package integrations connects outside trackers (ADR-0012, docs/02 §12.3).
// Phase 1 has GitHub: a workspace creates its own GitHub App from a
// manifest, installs it on its repositories and picks the repository, the
// labels and the templates new issues and replies about fixes start from.
// Operators create issues from conversations or link conversations to
// issues (GH-02, issues.go); the App's webhook closes and reopens them and
// drafts the reply about a fix (GH-03, hooks.go).
//
// The App's private key and webhook secret are sealed with
// internal/platform/crypto and never leave the module. Calls to GitHub
// never run inside a database transaction (docs/00 §6.4).
package integrations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/github"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
)

// ProviderGitHub is the only provider of phase 1.
const ProviderGitHub = "github"

// Statuses of an integration (migration 00016).
const (
	StatusAppPending     = "app_pending"
	StatusInstallPending = "install_pending"
	StatusActive         = "active"
	StatusDisabled       = "disabled"
)

// StateTTL bounds the manifest flow from the panel to GitHub and back; the
// manifest code GitHub returns lives an hour too.
const StateTTL = time.Hour

// Label limits; GitHub caps a label name at 50 characters.
const (
	MaxLabels     = 10
	maxLabelChars = 50
)

// Errors of the module. GitHub's own errors pass through wrapped.
var (
	ErrInvalid  = errors.New("integrations: invalid input")
	ErrNotFound = errors.New("integrations: not found")
	// ErrNoEncryption: TOLKOVO_MASTER_KEY is not set, the App's key cannot be stored.
	ErrNoEncryption = errors.New("integrations: TOLKOVO_MASTER_KEY is not set, secrets cannot be stored")
	// ErrAlreadyConnected: the workspace has a working GitHub integration.
	ErrAlreadyConnected = errors.New("integrations: GitHub is already connected")
	// ErrStep: the integration is not waiting for this step.
	ErrStep = errors.New("integrations: the connection is not waiting for this step")
	// ErrState: the state of the GitHub redirect is not this flow's or has expired.
	ErrState = errors.New("integrations: the GitHub redirect does not belong to this connection or has expired")
	// ErrCodeExpired: GitHub does not know the manifest code (used or older than an hour).
	ErrCodeExpired = errors.New("integrations: the GitHub code has expired, start again")
	// ErrInstallation: the installation is not of this workspace's App.
	ErrInstallation = errors.New("integrations: the installation is not of this GitHub App")
	// ErrRepository: the App is not installed on the repository.
	ErrRepository = errors.New("integrations: the GitHub App cannot reach the repository")
	// ErrGitHubAuth: GitHub refused the App (deleted, or its key revoked).
	ErrGitHubAuth = errors.New("integrations: GitHub refused the App's credentials")
	// ErrGitHub: GitHub could not be reached or answered unexpectedly.
	ErrGitHub = errors.New("integrations: GitHub is unavailable")
)

// GitHub is the part of the GitHub client the module uses (github.Client).
type GitHub interface {
	NewAppURL(org, state string) string
	InstallURL(slug string) string
	RepositoryURL(fullName string) string
	ConvertManifest(ctx context.Context, code string) (github.AppCredentials, error)
	Installation(ctx context.Context, app github.App, id int64) (github.Installation, error)
	InstallationToken(ctx context.Context, app github.App, installationID int64) (crypto.Secret, error)
	Repositories(ctx context.Context, token crypto.Secret) ([]github.Repository, bool, error)
	Repository(ctx context.Context, token crypto.Secret, fullName string) (github.Repository, error)
	CreateIssue(ctx context.Context, token crypto.Secret, fullName string, in github.NewIssue) (github.Issue, error)
	Issue(ctx context.Context, token crypto.Secret, fullName string, number int64) (github.Issue, error)
}

// Options configure the module.
type Options struct {
	// Sealer seals the App's key; nil refuses to connect (ErrNoEncryption).
	Sealer *crypto.Sealer
	GitHub GitHub
	// PublicURL is where GitHub posts webhooks (TOLKOVO_PUBLIC_URL).
	PublicURL string
	// PanelURL is where GitHub sends the browser back; issues link to
	// conversations under it.
	PanelURL string
	// Conversations draft issues and record them in the feed (GH-02).
	Conversations Conversations
	// Jobs queues github.create_issue (role api); nil refuses to create issues.
	Jobs Jobs
}

// Service is the facade of the module.
type Service struct {
	db   *db.DB
	opts Options
	now  func() time.Time
}

// New returns the service.
func New(database *db.DB, opts Options) *Service {
	return &Service{db: database, opts: opts, now: func() time.Time { return time.Now().UTC() }}
}

// EncryptionAvailable reports whether the App's key can be stored.
func (s *Service) EncryptionAvailable() bool { return s.opts.Sealer != nil }

// GitHubApp is the workspace's GitHub App.
type GitHubApp struct {
	ID    int64
	Slug  string
	Owner string
	// URL is the App's page on GitHub, where it is deleted.
	URL string
}

// Repository is the repository new issues go to.
type Repository struct {
	ID       int64
	FullName string
	Private  bool
	URL      string
}

// GitHubIntegration is what the panel may see of the integration.
type GitHubIntegration struct {
	ID     uuid.UUID
	Status string
	// App is nil until GitHub has created it.
	App *GitHubApp
	// InstallURL is set while the App waits to be installed.
	InstallURL string
	// Account the App is installed on.
	Account    string
	Repository *Repository
	Labels     []string
	// Templates in effect: the saved ones or the built-in ones.
	Templates Templates
	// Custom says which templates are saved rather than built in.
	Custom    CustomTemplates
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CustomTemplates flags the templates the admin has changed.
type CustomTemplates struct {
	IssueTitle, IssueBody, ReplyRU, ReplyEN bool
}

// appSecret is the sealed part of a GitHub integration.
type appSecret struct {
	PrivateKey    string `json:"private_key"`
	WebhookSecret string `json:"webhook_secret"`
}

// GitHubIntegration returns the workspace's live GitHub integration, or
// nil when there is none. locale picks the built-in issue template.
func (s *Service) GitHubIntegration(ctx context.Context, workspaceID uuid.UUID, locale string) (*GitHubIntegration, error) {
	var out *GitHubIntegration
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetLiveIntegration(ctx, store.GetLiveIntegrationParams{WorkspaceID: workspaceID, Provider: ProviderGitHub})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		v := s.integrationFrom(row, locale)
		out = &v
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("get github integration: %w", err)
	}
	return out, nil
}

// ManifestStart is the first step: the browser posts Manifest (JSON) to
// PostURL as the form field "manifest".
type ManifestStart struct {
	Integration GitHubIntegration
	PostURL     string
	Manifest    string
}

// githubLoginRE is the shape of a GitHub user or organization login.
var githubLoginRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// StartGitHub begins connecting GitHub: it records the flow and returns the
// manifest of a new GitHub App for the user's account, or for org. An
// unfinished earlier flow is replaced; a working integration is not.
func (s *Service) StartGitHub(ctx context.Context, workspaceID, actorID uuid.UUID, org, locale string) (ManifestStart, error) {
	if s.opts.Sealer == nil {
		return ManifestStart{}, ErrNoEncryption
	}
	org = strings.TrimSpace(org)
	if org != "" && !githubLoginRE.MatchString(org) {
		return ManifestStart{}, fmt.Errorf("%w: organization must be a GitHub login", ErrInvalid)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return ManifestStart{}, err
	}
	state, stateHash, err := newState()
	if err != nil {
		return ManifestStart{}, err
	}
	manifest, err := json.Marshal(s.manifest(workspaceID, id))
	if err != nil {
		return ManifestStart{}, err
	}
	now := s.now()
	expires := now.Add(StateTTL)
	var out ManifestStart
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetLiveIntegrationForUpdate(ctx, store.GetLiveIntegrationForUpdateParams{WorkspaceID: workspaceID, Provider: ProviderGitHub})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case cur.Status == StatusActive:
			return ErrAlreadyConnected
		default:
			// An App created by the abandoned flow stays on GitHub; the
			// panel tells the admin to delete it there.
			if err := s.dropUnfinished(ctx, q, cur); err != nil {
				return err
			}
		}
		row, err := q.InsertIntegration(ctx, store.InsertIntegrationParams{
			ID: id, WorkspaceID: workspaceID, Provider: ProviderGitHub, Status: StatusAppPending,
			StateHash: stateHash, StateExpiresAt: &expires, CreatedBy: &actorID, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		out = ManifestStart{Integration: s.integrationFrom(row, locale), PostURL: s.opts.GitHub.NewAppURL(org, state), Manifest: string(manifest)}
		return nil
	})
	if isUniqueViolation(err) {
		return ManifestStart{}, ErrAlreadyConnected // a concurrent start won
	}
	if err != nil {
		return ManifestStart{}, fmt.Errorf("start github: %w", err)
	}
	return out, nil
}

// HookPath is where GitHub posts the App's webhooks, under PublicURL (GH-03).
func HookPath(workspaceID, integrationID uuid.UUID) string {
	return "/hooks/github/" + workspaceID.String() + "/" + integrationID.String()
}

// manifest describes the App: issues read and write, metadata read, no
// access to code (ADR-0012), private to the account that creates it.
func (s *Service) manifest(workspaceID, id uuid.UUID) github.Manifest {
	panel := strings.TrimRight(s.opts.PanelURL, "/")
	public := strings.TrimRight(s.opts.PublicURL, "/")
	// GitHub delivers webhooks only to a URL it can reach; plain http is a
	// development setup, so the hook is created inactive there.
	u, err := url.Parse(public)
	active := err == nil && u.Scheme == "https"
	return github.Manifest{
		// App names are unique on GitHub; the admin may change it there.
		Name:               "Tolkovo " + strings.ReplaceAll(id.String(), "-", "")[24:],
		URL:                panel,
		Description:        "Issues from customer conversations in Tolkovo.",
		HookAttributes:     github.HookAttributes{URL: public + HookPath(workspaceID, id), Active: active},
		RedirectURL:        panel + "/settings/integrations?github=created",
		SetupURL:           panel + "/settings/integrations?github=installed",
		SetupOnUpdate:      true,
		Public:             false,
		DefaultPermissions: map[string]string{"issues": "write", "metadata": "read"},
		DefaultEvents:      []string{"issues"},
	}
}

// CompleteGitHubApp is the second step: GitHub created the App and sent the
// browser back with code and state. The code is exchanged for the App's
// key and webhook secret, which are sealed; then the App waits to be installed.
func (s *Service) CompleteGitHubApp(ctx context.Context, workspaceID uuid.UUID, code, state, locale string) (GitHubIntegration, error) {
	if s.opts.Sealer == nil {
		return GitHubIntegration{}, ErrNoEncryption
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 200 || state == "" || len(state) > 200 {
		return GitHubIntegration{}, fmt.Errorf("%w: code and state are required", ErrInvalid)
	}
	cur, err := s.live(ctx, workspaceID)
	if err != nil {
		return GitHubIntegration{}, err
	}
	if cur.Status != StatusAppPending {
		return GitHubIntegration{}, ErrStep
	}
	sum := sha256.Sum256([]byte(state))
	if subtle.ConstantTimeCompare(cur.StateHash, sum[:]) != 1 || cur.StateExpiresAt == nil || !s.now().Before(*cur.StateExpiresAt) {
		return GitHubIntegration{}, ErrState
	}
	creds, err := s.opts.GitHub.ConvertManifest(ctx, code)
	if errors.Is(err, github.ErrNotFound) {
		return GitHubIntegration{}, ErrCodeExpired
	}
	if err != nil {
		return GitHubIntegration{}, githubError(err)
	}
	secret, err := json.Marshal(appSecret{PrivateKey: creds.PrivateKey.Reveal(), WebhookSecret: creds.WebhookSecret.Reveal()})
	if err != nil {
		return GitHubIntegration{}, err
	}
	sealed, err := s.opts.Sealer.Seal(ctx, secret, crypto.AAD{WorkspaceID: workspaceID, SecretID: cur.ID})
	if err != nil {
		return GitHubIntegration{}, fmt.Errorf("seal github app: %w", err)
	}
	now := s.now()
	var out GitHubIntegration
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetLiveIntegrationForUpdate(ctx, store.GetLiveIntegrationForUpdateParams{WorkspaceID: workspaceID, Provider: ProviderGitHub})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (row.ID != cur.ID || row.Status != StatusAppPending)) {
			return ErrStep // replaced or completed meanwhile
		}
		if err != nil {
			return err
		}
		row, err = q.SetIntegrationApp(ctx, store.SetIntegrationAppParams{
			AppID: &creds.ID, AppSlug: &creds.Slug, AppOwner: &creds.Owner, AppUrl: &creds.HTMLURL,
			UpdatedAt: now, WorkspaceID: workspaceID, ID: cur.ID,
		})
		if err != nil {
			return err
		}
		if err := q.PutIntegrationSecret(ctx, store.PutIntegrationSecretParams{
			WorkspaceID: workspaceID, IntegrationID: cur.ID, Sealed: sealed, UpdatedAt: now,
		}); err != nil {
			return err
		}
		out = s.integrationFrom(row, locale)
		return nil
	})
	if err != nil {
		return GitHubIntegration{}, fmt.Errorf("complete github app: %w", err)
	}
	return out, nil
}

// CompleteGitHubInstallation is the third step: the App was installed and
// GitHub sent the browser back with the installation id. Only an
// installation of this workspace's App is accepted, which GitHub confirms
// for the App's own credentials. GitHub sends the browser back again when
// the admin changes the repositories later; that is answered as is.
func (s *Service) CompleteGitHubInstallation(ctx context.Context, workspaceID uuid.UUID, installationID int64, locale string) (GitHubIntegration, error) {
	if installationID <= 0 {
		return GitHubIntegration{}, fmt.Errorf("%w: installation_id must be positive", ErrInvalid)
	}
	cur, err := s.live(ctx, workspaceID)
	if err != nil {
		return GitHubIntegration{}, err
	}
	switch {
	case cur.Status == StatusActive && cur.InstallationID != nil && *cur.InstallationID == installationID:
		return s.integrationFrom(cur, locale), nil
	case cur.Status != StatusInstallPending:
		return GitHubIntegration{}, ErrStep
	}
	app, err := s.app(ctx, cur)
	if err != nil {
		return GitHubIntegration{}, err
	}
	inst, err := s.opts.GitHub.Installation(ctx, app, installationID)
	if errors.Is(err, github.ErrNotFound) {
		return GitHubIntegration{}, ErrInstallation
	}
	if err != nil {
		return GitHubIntegration{}, githubError(err)
	}
	now := s.now()
	var out GitHubIntegration
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetLiveIntegrationForUpdate(ctx, store.GetLiveIntegrationForUpdateParams{WorkspaceID: workspaceID, Provider: ProviderGitHub})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (row.ID != cur.ID || row.Status != StatusInstallPending)) {
			return ErrStep
		}
		if err != nil {
			return err
		}
		row, err = q.SetIntegrationInstallation(ctx, store.SetIntegrationInstallationParams{
			InstallationID: &inst.ID, Account: &inst.Account, UpdatedAt: now, WorkspaceID: workspaceID, ID: cur.ID,
		})
		if err != nil {
			return err
		}
		out = s.integrationFrom(row, locale)
		return nil
	})
	if err != nil {
		return GitHubIntegration{}, fmt.Errorf("complete github installation: %w", err)
	}
	return out, nil
}

// Repositories lists up to github.MaxRepositories repositories the App is
// installed on and whether there are more.
func (s *Service) Repositories(ctx context.Context, workspaceID uuid.UUID) ([]Repository, bool, error) {
	cur, err := s.live(ctx, workspaceID)
	if err != nil {
		return nil, false, err
	}
	token, err := s.token(ctx, cur)
	if err != nil {
		return nil, false, err
	}
	list, more, err := s.opts.GitHub.Repositories(ctx, token)
	if err != nil {
		return nil, false, githubError(err)
	}
	out := make([]Repository, 0, len(list))
	for _, r := range list {
		out = append(out, s.repository(r))
	}
	return out, more, nil
}

// GitHubSettings change an active integration; nil fields stay as they are.
type GitHubSettings struct {
	// Repository is "owner/name"; "" clears it.
	Repository *string
	Labels     *[]string
	// Templates: a field equal to the built-in text, or empty, goes back
	// to the built-in one.
	Templates *Templates
}

// UpdateGitHub saves the repository, the labels and the templates. A new
// repository is checked on GitHub first: the App must be installed on it.
func (s *Service) UpdateGitHub(ctx context.Context, workspaceID uuid.UUID, in GitHubSettings, locale string) (GitHubIntegration, error) {
	cur, err := s.live(ctx, workspaceID)
	if err != nil {
		return GitHubIntegration{}, err
	}
	if cur.Status != StatusActive {
		return GitHubIntegration{}, ErrStep
	}
	// Fields left out are taken from the locked row below.
	p := store.UpdateIntegrationSettingsParams{WorkspaceID: workspaceID, ID: cur.ID}
	if in.Labels != nil {
		labels, err := validLabels(*in.Labels)
		if err != nil {
			return GitHubIntegration{}, err
		}
		p.Labels = labels
	}
	if in.Templates != nil {
		def := DefaultTemplates(locale)
		effective := Templates{
			IssueTitle: orDefault(in.Templates.IssueTitle, def.IssueTitle),
			IssueBody:  orDefault(in.Templates.IssueBody, def.IssueBody),
			ReplyRU:    orDefault(in.Templates.ReplyRU, def.ReplyRU),
			ReplyEN:    orDefault(in.Templates.ReplyEN, def.ReplyEN),
		}
		if err := effective.validate(); err != nil {
			return GitHubIntegration{}, err
		}
		p.IssueTitleTemplate = custom(effective.IssueTitle, def.IssueTitle)
		p.IssueBodyTemplate = custom(effective.IssueBody, def.IssueBody)
		p.ReplyTemplateRu = custom(effective.ReplyRU, def.ReplyRU)
		p.ReplyTemplateEn = custom(effective.ReplyEN, def.ReplyEN)
	}
	if in.Repository != nil {
		name := strings.TrimSpace(*in.Repository)
		switch {
		case name == "":
			p.RepoID, p.RepoFullName, p.RepoPrivate = nil, nil, nil
		case cur.RepoFullName != nil && strings.EqualFold(*cur.RepoFullName, name):
		default:
			if utf8.RuneCountInString(name) > 200 {
				return GitHubIntegration{}, fmt.Errorf("%w: repository must be owner/name", ErrInvalid)
			}
			token, err := s.token(ctx, cur)
			if err != nil {
				return GitHubIntegration{}, err
			}
			repo, err := s.opts.GitHub.Repository(ctx, token, name)
			if errors.Is(err, github.ErrNotFound) {
				return GitHubIntegration{}, ErrRepository
			}
			if err != nil {
				return GitHubIntegration{}, githubError(err)
			}
			p.RepoID, p.RepoFullName, p.RepoPrivate = &repo.ID, &repo.FullName, &repo.Private
		}
	}
	p.UpdatedAt = s.now()
	var out GitHubIntegration
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetLiveIntegrationForUpdate(ctx, store.GetLiveIntegrationForUpdateParams{WorkspaceID: workspaceID, Provider: ProviderGitHub})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.ID != cur.ID) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if in.Repository == nil {
			p.RepoID, p.RepoFullName, p.RepoPrivate = row.RepoID, row.RepoFullName, row.RepoPrivate
		}
		if in.Labels == nil {
			p.Labels = row.Labels
		}
		if in.Templates == nil {
			p.IssueTitleTemplate, p.IssueBodyTemplate = row.IssueTitleTemplate, row.IssueBodyTemplate
			p.ReplyTemplateRu, p.ReplyTemplateEn = row.ReplyTemplateRu, row.ReplyTemplateEn
		}
		row, err = q.UpdateIntegrationSettings(ctx, p)
		if err != nil {
			return err
		}
		out = s.integrationFrom(row, locale)
		return nil
	})
	if err != nil {
		return GitHubIntegration{}, fmt.Errorf("update github: %w", err)
	}
	return out, nil
}

// DisconnectGitHub turns the integration off and forgets the App's key.
// An unfinished connection is removed; a working one is kept disabled for
// the issues linked to it. The App itself stays on GitHub until the admin
// deletes it there.
func (s *Service) DisconnectGitHub(ctx context.Context, workspaceID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetLiveIntegrationForUpdate(ctx, store.GetLiveIntegrationForUpdateParams{WorkspaceID: workspaceID, Provider: ProviderGitHub})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if row.Status != StatusActive {
			return s.dropUnfinished(ctx, q, row)
		}
		return s.disable(ctx, q, row)
	})
	if err != nil {
		return fmt.Errorf("disconnect github: %w", err)
	}
	return nil
}

// dropUnfinished removes an integration that is not active. One that was
// active once and lost its installation (GH-03) has issues: it is disabled
// like a working one, so they stay.
func (s *Service) dropUnfinished(ctx context.Context, q *store.Queries, row store.Integration) error {
	used, err := q.IntegrationHasIssues(ctx, store.IntegrationHasIssuesParams{WorkspaceID: row.WorkspaceID, IntegrationID: row.ID})
	if err != nil {
		return err
	}
	if used {
		return s.disable(ctx, q, row)
	}
	return q.DeleteIntegration(ctx, store.DeleteIntegrationParams{WorkspaceID: row.WorkspaceID, ID: row.ID})
}

// disable forgets the App's key and keeps the integration for its issues.
func (s *Service) disable(ctx context.Context, q *store.Queries, row store.Integration) error {
	if err := q.DeleteIntegrationSecret(ctx, store.DeleteIntegrationSecretParams{WorkspaceID: row.WorkspaceID, IntegrationID: row.ID}); err != nil {
		return err
	}
	now := s.now()
	return q.DisableIntegration(ctx, store.DisableIntegrationParams{WorkspaceID: row.WorkspaceID, ID: row.ID, DisabledAt: &now})
}

// live returns the live GitHub integration or ErrNotFound.
func (s *Service) live(ctx context.Context, workspaceID uuid.UUID) (store.Integration, error) {
	var row store.Integration
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		row, err = store.New(tx).GetLiveIntegration(ctx, store.GetLiveIntegrationParams{WorkspaceID: workspaceID, Provider: ProviderGitHub})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Integration{}, ErrNotFound
	}
	if err != nil {
		return store.Integration{}, fmt.Errorf("load github integration: %w", err)
	}
	return row, nil
}

// app opens the App's key.
func (s *Service) app(ctx context.Context, row store.Integration) (github.App, error) {
	if s.opts.Sealer == nil {
		return github.App{}, ErrNoEncryption
	}
	if row.AppID == nil {
		return github.App{}, ErrStep
	}
	sec, err := s.secret(ctx, row)
	if err != nil {
		return github.App{}, err
	}
	return github.App{ID: *row.AppID, PrivateKey: crypto.NewSecret(sec.PrivateKey)}, nil
}

// secret opens the App's key and webhook secret; ErrStep when there are
// none (not created yet, or disconnected).
func (s *Service) secret(ctx context.Context, row store.Integration) (appSecret, error) {
	if s.opts.Sealer == nil {
		return appSecret{}, ErrNoEncryption
	}
	var sealed []byte
	err := s.db.InTenantTx(ctx, row.WorkspaceID, func(tx pgx.Tx) error {
		var err error
		sealed, err = store.New(tx).GetIntegrationSecret(ctx, store.GetIntegrationSecretParams{WorkspaceID: row.WorkspaceID, IntegrationID: row.ID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return appSecret{}, ErrStep
	}
	if err != nil {
		return appSecret{}, fmt.Errorf("load github secret: %w", err)
	}
	plain, err := s.opts.Sealer.Open(ctx, sealed, crypto.AAD{WorkspaceID: row.WorkspaceID, SecretID: row.ID})
	if err != nil {
		return appSecret{}, fmt.Errorf("open github secret: %w", err)
	}
	var sec appSecret
	if err := json.Unmarshal([]byte(plain.Reveal()), &sec); err != nil {
		return appSecret{}, fmt.Errorf("decode github secret: %w", err)
	}
	return sec, nil
}

// token returns an installation token of an active integration.
func (s *Service) token(ctx context.Context, row store.Integration) (crypto.Secret, error) {
	if row.Status != StatusActive || row.InstallationID == nil {
		return crypto.Secret{}, ErrStep
	}
	app, err := s.app(ctx, row)
	if err != nil {
		return crypto.Secret{}, err
	}
	token, err := s.opts.GitHub.InstallationToken(ctx, app, *row.InstallationID)
	if err != nil {
		return crypto.Secret{}, githubError(err)
	}
	return token, nil
}

func (s *Service) repository(r github.Repository) Repository {
	return Repository{ID: r.ID, FullName: r.FullName, Private: r.Private, URL: s.opts.GitHub.RepositoryURL(r.FullName)}
}

func (s *Service) integrationFrom(row store.Integration, locale string) GitHubIntegration {
	def := DefaultTemplates(locale)
	out := GitHubIntegration{
		ID: row.ID, Status: row.Status, Account: deref(row.Account), Labels: row.Labels,
		Templates: Templates{
			IssueTitle: orDefault(deref(row.IssueTitleTemplate), def.IssueTitle),
			IssueBody:  orDefault(deref(row.IssueBodyTemplate), def.IssueBody),
			ReplyRU:    orDefault(deref(row.ReplyTemplateRu), def.ReplyRU),
			ReplyEN:    orDefault(deref(row.ReplyTemplateEn), def.ReplyEN),
		},
		Custom: CustomTemplates{
			IssueTitle: row.IssueTitleTemplate != nil, IssueBody: row.IssueBodyTemplate != nil,
			ReplyRU: row.ReplyTemplateRu != nil, ReplyEN: row.ReplyTemplateEn != nil,
		},
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if out.Labels == nil {
		out.Labels = []string{}
	}
	if row.AppID != nil {
		out.App = &GitHubApp{ID: *row.AppID, Slug: deref(row.AppSlug), Owner: deref(row.AppOwner), URL: deref(row.AppUrl)}
		if row.Status == StatusInstallPending {
			out.InstallURL = s.opts.GitHub.InstallURL(deref(row.AppSlug))
		}
	}
	if row.RepoID != nil && row.RepoFullName != nil {
		r := s.repository(github.Repository{ID: *row.RepoID, FullName: *row.RepoFullName, Private: row.RepoPrivate != nil && *row.RepoPrivate})
		out.Repository = &r
	}
	return out
}

// validLabels trims, drops repeats and checks the labels.
func validLabels(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l == "" || utf8.RuneCountInString(l) > maxLabelChars || !utf8.ValidString(l) || strings.ContainsAny(l, "\r\n") {
			return nil, fmt.Errorf("%w: a label must be 1 to %d characters on one line", ErrInvalid, maxLabelChars)
		}
		if k := strings.ToLower(l); !seen[k] {
			seen[k] = true
			out = append(out, l)
		}
	}
	if len(out) > MaxLabels {
		return nil, fmt.Errorf("%w: at most %d labels", ErrInvalid, MaxLabels)
	}
	return out, nil
}

// githubError maps the client's errors to the module's.
func githubError(err error) error {
	if errors.Is(err, github.ErrUnauthorized) || errors.Is(err, github.ErrPrivateKey) {
		return fmt.Errorf("%w: %w", ErrGitHubAuth, err)
	}
	return fmt.Errorf("%w: %w", ErrGitHub, err)
}

// newState returns a random state token and its SHA-256.
func newState() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(state))
	return state, sum[:], nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// custom is nil for the built-in text, so a later change of the built-in
// text reaches workspaces that never edited it.
func custom(v, def string) *string {
	if v == def {
		return nil
	}
	return &v
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isConstraint reports a unique violation of the named index.
func isConstraint(err error, name string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == name
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
