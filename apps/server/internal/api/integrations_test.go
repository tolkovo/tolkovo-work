package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/github"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/githubtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Connecting GitHub (GH-01): the workspace's own App from a manifest, its
// installation, the repository, labels and templates.
func TestGitHubIntegration(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("owner@example.test", "zerno")
	const base = "/api/v1/integrations/github"

	st := decode[oapi.GitHubIntegrationState](t, c.do(http.MethodGet, base, nil))
	if st.Integration != nil || !st.EncryptionAvailable || !strings.Contains(st.Defaults.IssueBody, "## Шаги") ||
		len(st.Placeholders.Issue) == 0 || len(st.Placeholders.Reply) == 0 {
		t.Fatalf("before connecting: %+v", st)
	}
	wantProblem(t, c.do(http.MethodPost, base+"/app", map[string]any{"code": "x", "state": "y"}), http.StatusNotFound, "not_found")
	wantProblem(t, c.do(http.MethodPost, base+"/manifest", map[string]any{"organization": "-bad"}), http.StatusBadRequest, "invalid_request")

	// Step 1: the manifest of an App that may touch issues only.
	rec := c.do(http.MethodPost, base+"/manifest", map[string]any{"organization": "zerno-team"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("manifest: %d %s", rec.Code, rec.Body)
	}
	started := decode[oapi.GitHubManifestStart](t, rec)
	post, err := url.Parse(started.PostUrl)
	if err != nil || !strings.HasPrefix(started.PostUrl, githubtest.Web+"/organizations/zerno-team/settings/apps/new?") {
		t.Fatalf("post_url: %s", started.PostUrl)
	}
	state := post.Query().Get("state")
	var m github.Manifest
	if err := json.Unmarshal([]byte(started.Manifest), &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	id := started.Integration.Id
	if started.Integration.Status != oapi.GitHubIntegrationStatusAppPending || state == "" || m.Public ||
		m.HookAttributes.URL != "https://support.example.com/hooks/github/"+s.Workspace.Id.String()+"/"+id.String() || !m.HookAttributes.Active ||
		m.RedirectURL != panelURL+"/settings/integrations?github=created" || m.SetupURL != panelURL+"/settings/integrations?github=installed" ||
		len(m.DefaultPermissions) != 2 || m.DefaultPermissions["issues"] != "write" || m.DefaultPermissions["metadata"] != "read" ||
		len(m.DefaultEvents) != 1 || m.DefaultEvents[0] != "issues" || len(m.Name) > 34 {
		t.Fatalf("manifest: %s", started.Manifest)
	}

	// Step 2: only this flow's state and a live code are accepted.
	creds := e.github.AddApp("code-1", 42, "tolkovo-zerno", "zerno-team")
	complete := func(code, state string) int {
		return c.do(http.MethodPost, base+"/app", map[string]any{"code": code, "state": state}).Code
	}
	wantProblem(t, c.do(http.MethodPost, base+"/app", map[string]any{"code": "code-1", "state": "forged"}), http.StatusConflict, "github_state")
	wantProblem(t, c.do(http.MethodPost, base+"/app", map[string]any{"code": "spent", "state": state}), http.StatusUnprocessableEntity, "github_code_expired")
	rec = c.do(http.MethodPost, base+"/app", map[string]any{"code": "code-1", "state": state})
	if rec.Code != http.StatusOK {
		t.Fatalf("app: %d %s", rec.Code, rec.Body)
	}
	g := decode[oapi.GitHubIntegration](t, rec)
	if g.Status != oapi.GitHubIntegrationStatusInstallPending || g.App == nil || g.App.Id != 42 || g.App.Slug != "tolkovo-zerno" ||
		g.InstallUrl == nil || *g.InstallUrl != githubtest.Web+"/apps/tolkovo-zerno/installations/new" {
		t.Fatalf("after the app: %s", rec.Body)
	}
	if code := complete("code-1", state); code != http.StatusConflict {
		t.Fatalf("the step again: %d", code)
	}
	// The key and the webhook secret are sealed, not stored as they came.
	var sealed []byte
	e.ownerScan(&sealed, "SELECT sealed FROM integration_secrets WHERE integration_id = $1", id)
	if strings.Contains(string(sealed), "key-of-42") || strings.Contains(string(sealed), creds.WebhookSecret.Reveal()) {
		t.Fatal("the App's secrets are stored in the clear")
	}
	if strings.Contains(rec.Body.String(), "key-of-42") || strings.Contains(rec.Body.String(), creds.WebhookSecret.Reveal()) {
		t.Fatal("the App's secrets are in the answer")
	}

	// Step 3: an installation of somebody else's App is refused.
	e.github.AddInstallation(7, 99, "stranger")
	e.github.AddInstallation(8, 42, "zerno-team")
	install := func(id int) int {
		return c.do(http.MethodPost, base+"/installation", map[string]any{"installation_id": id}).Code
	}
	wantProblem(t, c.do(http.MethodPost, base+"/installation", map[string]any{"installation_id": 7}), http.StatusUnprocessableEntity, "github_installation_invalid")
	rec = c.do(http.MethodPost, base+"/installation", map[string]any{"installation_id": 8})
	if g = decode[oapi.GitHubIntegration](t, rec); rec.Code != http.StatusOK || g.Status != oapi.GitHubIntegrationStatusActive || g.Account == nil ||
		*g.Account != "zerno-team" || g.InstallUrl != nil || g.Repository != nil {
		t.Fatalf("installed: %d %s", rec.Code, rec.Body)
	}
	// GitHub sends the browser back when the repositories change.
	if code := install(8); code != http.StatusOK {
		t.Fatalf("installed again: %d", code)
	}
	if code := install(7); code != http.StatusConflict {
		t.Fatalf("another installation of an active integration: %d", code)
	}

	// The repository, the labels and the templates.
	e.github.SetRepositories(
		github.Repository{ID: 1, FullName: "zerno-team/desktop", Private: true},
		github.Repository{ID: 2, FullName: "zerno-team/site"},
	)
	repos := decode[oapi.GitHubRepositoryList](t, c.do(http.MethodGet, base+"/repositories", nil))
	if len(repos.Items) != 2 || repos.More || repos.Items[0].Url != githubtest.Web+"/zerno-team/desktop" {
		t.Fatalf("repositories: %+v", repos)
	}
	wantProblem(t, c.do(http.MethodPatch, base, map[string]any{"repository": "zerno-team/secret"}), http.StatusUnprocessableEntity, "github_repository_unreachable")
	defaults := decode[oapi.GitHubIntegrationState](t, c.do(http.MethodGet, base, nil)).Defaults
	templates := map[string]any{
		"issue_title": defaults.IssueTitle, "issue_body": "", "reply_en": defaults.ReplyEn,
		"reply_ru": "{name}, исправили в issue #{issue_number}. Обновление выйдет на этой неделе.",
	}
	rec = c.do(http.MethodPatch, base, map[string]any{
		"repository": "zerno-team/desktop", "labels": []string{"bug", " from-support ", "Bug"}, "templates": templates,
	})
	g = decode[oapi.GitHubIntegration](t, rec)
	if rec.Code != http.StatusOK || g.Repository == nil || g.Repository.FullName != "zerno-team/desktop" || !g.Repository.Private ||
		len(g.Labels) != 2 || g.Labels[1] != "from-support" || !g.CustomTemplates.ReplyRu || g.CustomTemplates.IssueBody ||
		g.CustomTemplates.IssueTitle || g.CustomTemplates.ReplyEn || g.Templates.IssueBody != defaults.IssueBody {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	templates["issue_body"] = "{summary}\n{email}"
	wantProblem(t, c.do(http.MethodPatch, base, map[string]any{"templates": templates}), http.StatusBadRequest, "invalid_request")
	// Fields left out stay.
	g = decode[oapi.GitHubIntegration](t, c.do(http.MethodPatch, base, map[string]any{"labels": []string{"support"}}))
	if len(g.Labels) != 1 || g.Repository == nil || !g.CustomTemplates.ReplyRu {
		t.Fatalf("labels only: %+v", g)
	}
	if got := decode[oapi.GitHubIntegrationState](t, c.do(http.MethodGet, base, nil)).Integration; got == nil ||
		got.Templates.ReplyRu != templates["reply_ru"] || got.Repository.Id != 1 {
		t.Fatalf("saved: %+v", got)
	}

	// GitHub's troubles are told apart.
	e.github.Fail(errors.New("connection reset"))
	wantProblem(t, c.do(http.MethodGet, base+"/repositories", nil), http.StatusBadGateway, "github_unavailable")
	e.github.Fail(github.ErrUnauthorized)
	wantProblem(t, c.do(http.MethodGet, base+"/repositories", nil), http.StatusUnprocessableEntity, "github_app_refused")
	e.github.Fail(nil)

	// One integration at a time; nobody else sees or changes it.
	wantProblem(t, c.do(http.MethodPost, base+"/manifest", map[string]any{}), http.StatusConflict, "github_connected")
	_, sop := e.register("operator@example.test", "operator-home")
	e.addMember(s.Workspace.Id, sop.User.Id, tenancy.RoleOperator)
	op, _ := e.login("operator@example.test")
	if rec := op.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": s.Workspace.Id}); rec.Code != http.StatusOK {
		t.Fatalf("operator switch: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, op.do(http.MethodGet, base, nil), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodPatch, base, map[string]any{"labels": []string{}}), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodDelete, base, nil), http.StatusForbidden, "forbidden")
	other, _ := e.register("other@example.test", "other")
	if got := decode[oapi.GitHubIntegrationState](t, other.do(http.MethodGet, base, nil)); got.Integration != nil {
		t.Fatal("another workspace sees the integration")
	}
	wantProblem(t, other.do(http.MethodDelete, base, nil), http.StatusNotFound, "not_found")

	// Disconnecting forgets the key and keeps the row for linked issues.
	if rec := c.do(http.MethodDelete, base, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.GitHubIntegrationState](t, c.do(http.MethodGet, base, nil)); got.Integration != nil {
		t.Fatal("still connected")
	}
	if n := e.ownerCount("SELECT count(*) FROM integrations WHERE id = $1 AND status = 'disabled' AND disabled_at IS NOT NULL", id); n != 1 {
		t.Fatal("the integration is not kept disabled")
	}
	if n := e.ownerCount("SELECT count(*) FROM integration_secrets WHERE integration_id = $1", id); n != 0 {
		t.Fatal("the App's key is kept")
	}
	wantProblem(t, c.do(http.MethodDelete, base, nil), http.StatusNotFound, "not_found")

	// An unfinished connection is replaced by a new start and expires.
	first := decode[oapi.GitHubManifestStart](t, c.do(http.MethodPost, base+"/manifest", map[string]any{}))
	if !strings.HasPrefix(first.PostUrl, githubtest.Web+"/settings/apps/new?") {
		t.Fatalf("personal account: %s", first.PostUrl)
	}
	second := decode[oapi.GitHubManifestStart](t, c.do(http.MethodPost, base+"/manifest", map[string]any{}))
	if n := e.ownerCount("SELECT count(*) FROM integrations WHERE workspace_id = $1 AND status <> 'disabled'", s.Workspace.Id); n != 1 ||
		second.Integration.Id == first.Integration.Id {
		t.Fatalf("live integrations: %d", n)
	}
	e.github.AddApp("code-2", 43, "tolkovo-zerno-2", "zerno")
	firstURL, _ := url.Parse(first.PostUrl)
	wantProblem(t, c.do(http.MethodPost, base+"/app", map[string]any{"code": "code-2", "state": firstURL.Query().Get("state")}),
		http.StatusConflict, "github_state")
	e.ownerExec("UPDATE integrations SET state_expires_at = now() - interval '1 second' WHERE id = $1", second.Integration.Id)
	secondURL, _ := url.Parse(second.PostUrl)
	wantProblem(t, c.do(http.MethodPost, base+"/app", map[string]any{"code": "code-2", "state": secondURL.Query().Get("state")}),
		http.StatusConflict, "github_state")
	if rec := c.do(http.MethodDelete, base, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d", rec.Code)
	}
	if n := e.ownerCount("SELECT count(*) FROM integrations WHERE workspace_id = $1", s.Workspace.Id); n != 1 {
		t.Fatalf("an unfinished connection is kept: %d rows", n)
	}
}
