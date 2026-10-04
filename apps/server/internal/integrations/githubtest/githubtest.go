// Package githubtest is an in-memory GitHub for tests of the integrations
// module and the API: manifest codes, installations, tokens and
// repositories, with the same errors as the real client.
package githubtest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/github"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// Web is the fake's github.com.
const Web = "https://github.test"

type installation struct {
	appID   int64
	account string
}

// GitHub implements integrations.GitHub.
type GitHub struct {
	mu       sync.Mutex
	codes    map[string]github.AppCredentials
	keys     map[int64]string // App id → private key, to check the sealed one comes back
	installs map[int64]installation
	repos    []github.Repository
	fail     error
}

// AddApp makes code exchangeable once for a new App.
func (g *GitHub) AddApp(code string, id int64, slug, owner string) github.AppCredentials {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.codes == nil {
		g.codes, g.keys = map[string]github.AppCredentials{}, map[int64]string{}
	}
	key := fmt.Sprintf("-----BEGIN RSA PRIVATE KEY-----\nkey-of-%d\n-----END RSA PRIVATE KEY-----\n", id)
	c := github.AppCredentials{
		ID: id, Slug: slug, Owner: owner, HTMLURL: Web + "/apps/" + slug,
		PrivateKey: crypto.NewSecret(key), WebhookSecret: crypto.NewSecret(fmt.Sprintf("whsec-%d", id)),
	}
	g.codes[code] = c
	g.keys[id] = key
	return c
}

// AddInstallation installs App appID on account.
func (g *GitHub) AddInstallation(id, appID int64, account string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.installs == nil {
		g.installs = map[int64]installation{}
	}
	g.installs[id] = installation{appID: appID, account: account}
}

// SetRepositories sets what every installation reaches.
func (g *GitHub) SetRepositories(r ...github.Repository) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repos = r
}

// Fail makes every call fail with err until Fail(nil).
func (g *GitHub) Fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fail = err
}

// NewAppURL implements integrations.GitHub.
func (g *GitHub) NewAppURL(org, state string) string {
	if org != "" {
		return Web + "/organizations/" + org + "/settings/apps/new?state=" + state
	}
	return Web + "/settings/apps/new?state=" + state
}

// InstallURL implements integrations.GitHub.
func (g *GitHub) InstallURL(slug string) string { return Web + "/apps/" + slug + "/installations/new" }

// RepositoryURL implements integrations.GitHub.
func (g *GitHub) RepositoryURL(fullName string) string { return Web + "/" + fullName }

// ConvertManifest implements integrations.GitHub; a code works once.
func (g *GitHub) ConvertManifest(_ context.Context, code string) (github.AppCredentials, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail != nil {
		return github.AppCredentials{}, g.fail
	}
	c, ok := g.codes[code]
	if !ok {
		return github.AppCredentials{}, github.ErrNotFound
	}
	delete(g.codes, code)
	return c, nil
}

func (g *GitHub) checkApp(app github.App) error {
	if g.fail != nil {
		return g.fail
	}
	if key, ok := g.keys[app.ID]; !ok || key != app.PrivateKey.Reveal() {
		return github.ErrUnauthorized
	}
	return nil
}

// Installation implements integrations.GitHub.
func (g *GitHub) Installation(_ context.Context, app github.App, id int64) (github.Installation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkApp(app); err != nil {
		return github.Installation{}, err
	}
	in, ok := g.installs[id]
	if !ok || in.appID != app.ID {
		return github.Installation{}, github.ErrNotFound
	}
	return github.Installation{ID: id, Account: in.account}, nil
}

// InstallationToken implements integrations.GitHub.
func (g *GitHub) InstallationToken(_ context.Context, app github.App, installationID int64) (crypto.Secret, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkApp(app); err != nil {
		return crypto.Secret{}, err
	}
	if in, ok := g.installs[installationID]; !ok || in.appID != app.ID {
		return crypto.Secret{}, github.ErrNotFound
	}
	return crypto.NewSecret(fmt.Sprintf("ghs_%d", installationID)), nil
}

// Repositories implements integrations.GitHub.
func (g *GitHub) Repositories(_ context.Context, token crypto.Secret) ([]github.Repository, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail != nil {
		return nil, false, g.fail
	}
	if !strings.HasPrefix(token.Reveal(), "ghs_") {
		return nil, false, github.ErrUnauthorized
	}
	return append([]github.Repository(nil), g.repos...), false, nil
}

// Repository implements integrations.GitHub.
func (g *GitHub) Repository(_ context.Context, token crypto.Secret, fullName string) (github.Repository, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail != nil {
		return github.Repository{}, g.fail
	}
	if !strings.HasPrefix(token.Reveal(), "ghs_") {
		return github.Repository{}, github.ErrUnauthorized
	}
	for _, r := range g.repos {
		if strings.EqualFold(r.FullName, fullName) {
			return r, nil
		}
	}
	return github.Repository{}, github.ErrNotFound
}
