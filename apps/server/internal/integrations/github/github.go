// Package github is a thin client of the GitHub REST API for a GitHub App
// (ADR-0012): the manifest flow that creates the App, its installation,
// installation tokens, repositories and issues. No SDK: a handful of calls over
// net/http, the App's JWT signed with crypto/rsa.
//
// The client never logs and never puts secrets into errors: an error names
// the call and the HTTP status, not the request or the answer.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tcrypto "github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// Defaults for github.com; GitHub Enterprise Server has its own
// (https://ghe.example.com and https://ghe.example.com/api/v3).
const (
	DefaultAPIURL = "https://api.github.com"
	DefaultWebURL = "https://github.com"
)

// Errors of the client. ErrUnavailable wraps the cause (network, status).
var (
	// ErrNotFound: GitHub answered 404, e.g. an expired manifest code, an
	// installation of another App, a repository the App cannot see.
	ErrNotFound = errors.New("github: not found")
	// ErrUnauthorized: GitHub refused the App's credentials (401 or 403),
	// e.g. the App was deleted or its key revoked.
	ErrUnauthorized = errors.New("github: credentials refused")
	// ErrUnavailable: GitHub could not be reached or answered unexpectedly.
	ErrUnavailable = errors.New("github: unavailable")
	// ErrRejected: GitHub refused the request itself (410 or 422), e.g.
	// issues are turned off in the repository or a field is not accepted.
	ErrRejected = errors.New("github: request rejected")
	// ErrPrivateKey: the App's private key does not parse.
	ErrPrivateKey = errors.New("github: the App private key is not an RSA key in PEM")
)

// MaxRepositories is how many repositories Repositories returns.
const MaxRepositories = 100

// Client calls one GitHub instance.
type Client struct {
	api  string
	web  string
	http *http.Client
	now  func() time.Time
}

// New returns a client of the GitHub at apiURL (REST) and webURL (pages the
// browser opens). hc should be an httpx.SafeClient.
func New(apiURL, webURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{api: strings.TrimRight(apiURL, "/"), web: strings.TrimRight(webURL, "/"), http: hc, now: time.Now}
}

// Manifest describes the GitHub App to create (docs.github.com, "Registering
// a GitHub App from a manifest").
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Description        string            `json:"description,omitempty"`
	HookAttributes     HookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	SetupURL           string            `json:"setup_url"`
	SetupOnUpdate      bool              `json:"setup_on_update"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

// HookAttributes is the webhook of the App.
type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// NewAppURL is where the browser posts the manifest: the user's account,
// or the organization when org is set.
func (c *Client) NewAppURL(org, state string) string {
	path := "/settings/apps/new"
	if org != "" {
		path = "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return c.web + path + "?state=" + url.QueryEscape(state)
}

// InstallURL is where the browser installs the App on repositories.
func (c *Client) InstallURL(slug string) string {
	return c.web + "/apps/" + url.PathEscape(slug) + "/installations/new"
}

// RepositoryURL is the page of a repository.
func (c *Client) RepositoryURL(fullName string) string {
	return c.web + "/" + fullName
}

// AppCredentials are what GitHub hands over once when the App is created.
type AppCredentials struct {
	ID            int64
	Slug          string
	Owner         string
	HTMLURL       string
	PrivateKey    tcrypto.Secret
	WebhookSecret tcrypto.Secret
}

// ConvertManifest exchanges the code from the manifest redirect for the
// App's credentials. The code is single-use and lives an hour.
func (c *Client) ConvertManifest(ctx context.Context, code string) (AppCredentials, error) {
	var out struct {
		ID      int64  `json:"id"`
		Slug    string `json:"slug"`
		HTMLURL string `json:"html_url"`
		Owner   struct {
			Login string `json:"login"`
		} `json:"owner"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
	}
	if err := c.do(ctx, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &out); err != nil {
		return AppCredentials{}, fmt.Errorf("convert manifest: %w", err)
	}
	if out.ID == 0 || out.Slug == "" || out.PEM == "" {
		return AppCredentials{}, fmt.Errorf("convert manifest: %w: incomplete answer", ErrUnavailable)
	}
	if _, err := parseKey(out.PEM); err != nil {
		return AppCredentials{}, fmt.Errorf("convert manifest: %w", err)
	}
	return AppCredentials{
		ID: out.ID, Slug: out.Slug, Owner: out.Owner.Login, HTMLURL: out.HTMLURL,
		PrivateKey: tcrypto.NewSecret(out.PEM), WebhookSecret: tcrypto.NewSecret(out.WebhookSecret),
	}, nil
}

// App authenticates as one GitHub App.
type App struct {
	ID         int64
	PrivateKey tcrypto.Secret
}

// Installation is an installation of the App.
type Installation struct {
	ID int64
	// Account is the user or organization login the App is installed on.
	Account string
}

// Installation returns an installation of the App. ErrNotFound means it
// is not this App's (or was removed).
func (c *Client) Installation(ctx context.Context, app App, id int64) (Installation, error) {
	jwt, err := c.appJWT(app)
	if err != nil {
		return Installation{}, err
	}
	var out struct {
		ID      int64 `json:"id"`
		AppID   int64 `json:"app_id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := c.do(ctx, http.MethodGet, "/app/installations/"+strconv.FormatInt(id, 10), jwt, nil, &out); err != nil {
		return Installation{}, fmt.Errorf("get installation: %w", err)
	}
	if out.ID != id || (out.AppID != 0 && out.AppID != app.ID) {
		return Installation{}, fmt.Errorf("get installation: %w", ErrNotFound)
	}
	return Installation{ID: out.ID, Account: out.Account.Login}, nil
}

// InstallationToken returns a token of the installation; it lives an hour.
func (c *Client) InstallationToken(ctx context.Context, app App, installationID int64) (tcrypto.Secret, error) {
	jwt, err := c.appJWT(app)
	if err != nil {
		return tcrypto.Secret{}, err
	}
	var out struct {
		Token string `json:"token"`
	}
	path := "/app/installations/" + strconv.FormatInt(installationID, 10) + "/access_tokens"
	if err := c.do(ctx, http.MethodPost, path, jwt, nil, &out); err != nil {
		return tcrypto.Secret{}, fmt.Errorf("installation token: %w", err)
	}
	if out.Token == "" {
		return tcrypto.Secret{}, fmt.Errorf("installation token: %w: empty token", ErrUnavailable)
	}
	return tcrypto.NewSecret(out.Token), nil
}

// Repository is a repository the installation can reach.
type Repository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	HTMLURL  string `json:"html_url"`
}

// Repositories returns up to MaxRepositories repositories of the
// installation and whether there are more.
func (c *Client) Repositories(ctx context.Context, token tcrypto.Secret) ([]Repository, bool, error) {
	var out struct {
		TotalCount   int          `json:"total_count"`
		Repositories []Repository `json:"repositories"`
	}
	path := "/installation/repositories?per_page=" + strconv.Itoa(MaxRepositories)
	if err := c.do(ctx, http.MethodGet, path, "token "+token.Reveal(), nil, &out); err != nil {
		return nil, false, fmt.Errorf("list repositories: %w", err)
	}
	return out.Repositories, out.TotalCount > len(out.Repositories), nil
}

// Repository returns one repository by "owner/name" if the installation
// can reach it.
func (c *Client) Repository(ctx context.Context, token tcrypto.Secret, fullName string) (Repository, error) {
	path, err := repoPath(fullName)
	if err != nil {
		return Repository{}, fmt.Errorf("get repository: %w", err)
	}
	var out Repository
	if err := c.do(ctx, http.MethodGet, path, "token "+token.Reveal(), nil, &out); err != nil {
		return Repository{}, fmt.Errorf("get repository: %w", err)
	}
	return out, nil
}

// Issue is an issue on GitHub.
type Issue struct {
	ID      int64  `json:"id"`
	NodeID  string `json:"node_id"`
	Number  int64  `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"` // open or closed
	HTMLURL string `json:"html_url"`
	// PullRequest is set when the number is a pull request: GitHub serves
	// them from the issues API too.
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

// NewIssue is an issue to create.
type NewIssue struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
}

// CreateIssue creates an issue in the repository "owner/name".
func (c *Client) CreateIssue(ctx context.Context, token tcrypto.Secret, fullName string, in NewIssue) (Issue, error) {
	path, err := repoPath(fullName)
	if err != nil {
		return Issue{}, fmt.Errorf("create issue: %w", err)
	}
	var out Issue
	if err := c.do(ctx, http.MethodPost, path+"/issues", "token "+token.Reveal(), in, &out); err != nil {
		return Issue{}, fmt.Errorf("create issue: %w", err)
	}
	return out, nil
}

// Issue returns an issue of the repository "owner/name" by number.
func (c *Client) Issue(ctx context.Context, token tcrypto.Secret, fullName string, number int64) (Issue, error) {
	path, err := repoPath(fullName)
	if err != nil {
		return Issue{}, fmt.Errorf("get issue: %w", err)
	}
	var out Issue
	if err := c.do(ctx, http.MethodGet, path+"/issues/"+strconv.FormatInt(number, 10), "token "+token.Reveal(), nil, &out); err != nil {
		return Issue{}, fmt.Errorf("get issue: %w", err)
	}
	return out, nil
}

// repoPath is /repos/{owner}/{name} of "owner/name".
func repoPath(fullName string) (string, error) {
	owner, name, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", ErrNotFound
	}
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

// rateLimited: GitHub answers 429, or 403 with no requests left or a
// Retry-After, when the App sends too much; that passes, unlike a refusal.
func rateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "")
}

// appJWT signs the short-lived JWT a GitHub App authenticates with
// (RS256, issued a minute in the past against clock drift, 9 minutes).
func (c *Client) appJWT(app App) (string, error) {
	key, err := parseKey(app.PrivateKey.Reveal())
	if err != nil {
		return "", err
	}
	now := c.now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(app.ID, 10),
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return "Bearer " + signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// parseKey reads the PEM GitHub issues (PKCS#1) or a PKCS#8 one.
func parseKey(s string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, ErrPrivateKey
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrPrivateKey
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, ErrPrivateKey
	}
	return rk, nil
}

// maxAnswer bounds an answer read into memory.
const maxAnswer = 2 << 20

// do sends one request and decodes a 2xx JSON answer into out.
func (c *Client) do(ctx context.Context, method, path, authorization string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, rd)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Tolkovo")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The *url.Error names the URL, which holds no secret (the manifest
		// code is single-use and already spent or expired by then).
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case rateLimited(resp):
		return fmt.Errorf("%w: rate limited", ErrUnavailable)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusUnprocessableEntity:
		return ErrRejected
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAnswer)).Decode(out); err != nil {
		return fmt.Errorf("%w: decode answer: %w", ErrUnavailable, err)
	}
	return nil
}
