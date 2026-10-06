package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/guard"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// The eval of the issue draft (docs/00 §10.2): reports in
// testdata/issue_draft_eval.json, each with checks the draft must pass. It
// asks a real model, so it runs only when asked, with the provider, model
// and key in the environment:
//
//	TOLKOVO_EVAL_AI_PROVIDER=anthropic TOLKOVO_EVAL_AI_MODEL=claude-haiku-4-5 \
//	TOLKOVO_EVAL_AI_KEY=... go test -run TestIssueDraftEval -v ./internal/ai/
//
// TOLKOVO_EVAL_AI_BASE_URL points openai_compatible at its server. A change
// of the prompt, the schema or the model comes with its result, before and
// after.

// evalCase is one report of the eval set.
type evalCase struct {
	ID      string `json:"id"`
	Locale  string `json:"locale"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	App     struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		OS        string `json:"os"`
		OSVersion string `json:"os_version"`
	} `json:"app"`
	// MustMention: every group needs one of its words (case-insensitive)
	// in the title, the summary or the actual result.
	MustMention [][]string `json:"must_mention"`
	// Forbid: none of these may appear anywhere in the draft.
	Forbid   []string `json:"forbid"`
	MinSteps int      `json:"min_steps"`
	// MaxSteps bounds made-up steps for reports without any; 0 is no bound.
	MaxSteps int `json:"max_steps"`
}

func (c evalCase) report() IssueReport {
	return IssueReport{
		Locale: c.Locale, Subject: c.Subject, Text: c.Text,
		App: AppInfo{Name: c.App.Name, Version: c.App.Version, OS: c.App.OS, OSVersion: c.App.OSVersion},
	}
}

func loadEvalSet(t *testing.T) []evalCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/issue_draft_eval.json")
	if err != nil {
		t.Fatalf("read eval set: %v", err)
	}
	var cases []evalCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse eval set: %v", err)
	}
	return cases
}

// gradeDraft returns what the draft fails; none means it passes.
func gradeDraft(c evalCase, d IssueText) []string {
	var fails []string
	all := strings.ToLower(strings.Join(append([]string{d.Title, d.Summary, d.Expected, d.Actual}, d.Steps...), "\n"))
	about := strings.ToLower(d.Title + "\n" + d.Summary + "\n" + d.Actual)
	if n := len([]rune(d.Title)); n > 100 {
		fails = append(fails, fmt.Sprintf("title of %d characters", n))
	}
	if d.Summary == "" {
		fails = append(fails, "no summary")
	}
	if !inLanguage(d.Title+" "+d.Summary, c.Locale) {
		fails = append(fails, "not in "+c.Locale)
	}
	for _, group := range c.MustMention {
		found := false
		for _, w := range group {
			found = found || strings.Contains(about, strings.ToLower(w))
		}
		if !found {
			fails = append(fails, "does not mention "+strings.Join(group, "/"))
		}
	}
	for _, w := range c.Forbid {
		if strings.Contains(all, strings.ToLower(w)) {
			fails = append(fails, "contains "+w)
		}
	}
	if len(d.Steps) < c.MinSteps {
		fails = append(fails, fmt.Sprintf("%d steps, want at least %d", len(d.Steps), c.MinSteps))
	}
	if c.MaxSteps > 0 && len(d.Steps) > c.MaxSteps {
		fails = append(fails, fmt.Sprintf("%d steps, want at most %d", len(d.Steps), c.MaxSteps))
	}
	return fails
}

// inLanguage: most letters are Cyrillic for ru, Latin for en.
func inLanguage(s, locale string) bool {
	var cyr, lat int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case unicode.Is(unicode.Latin, r):
			lat++
		}
	}
	if locale == "en" {
		return lat > 0 && cyr*10 < lat
	}
	return cyr > 0 && cyr > lat*2
}

// The set itself is checked on every run, and so are the checks.
func TestIssueDraftEval_Set(t *testing.T) {
	cases := loadEvalSet(t)
	if len(cases) < 10 || len(cases) > 20 {
		t.Fatalf("%d cases, want 10 to 20", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] || (c.Locale != "ru" && c.Locale != "en") || strings.TrimSpace(c.Text) == "" || len(c.MustMention) == 0 {
			t.Errorf("bad case %+v", c)
		}
		seen[c.ID] = true
	}

	c := evalCase{Locale: "ru", MustMention: [][]string{{"корзин"}}, Forbid: []string{"git push"}, MinSteps: 1}
	good := IssueText{Title: "Корзина пустеет после обновления", Summary: "После обновления корзина пустая.", Steps: []string{"Обновить приложение"}}
	if fails := gradeDraft(c, good); len(fails) != 0 {
		t.Fatalf("good draft fails: %v", fails)
	}
	bad := IssueText{Title: "Cart is empty", Summary: "After the update. Run git push --force."}
	if fails := gradeDraft(c, bad); len(fails) != 4 {
		t.Fatalf("bad draft: %v", fails)
	}
}

func TestIssueDraftEval(t *testing.T) {
	provider, model := os.Getenv("TOLKOVO_EVAL_AI_PROVIDER"), os.Getenv("TOLKOVO_EVAL_AI_MODEL")
	if provider == "" || model == "" {
		t.Skip("set TOLKOVO_EVAL_AI_PROVIDER, TOLKOVO_EVAL_AI_MODEL and TOLKOVO_EVAL_AI_KEY to run the eval")
	}
	p, err := llm.New(llm.Config{
		Provider: provider, BaseURL: os.Getenv("TOLKOVO_EVAL_AI_BASE_URL"), Key: crypto.NewSecret(os.Getenv("TOLKOVO_EVAL_AI_KEY")),
	}, &http.Client{Timeout: 90 * time.Second})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	cases := loadEvalSet(t)
	passed, in, out := 0, 0, 0
	for _, c := range cases {
		// The request as Complete sends it: personal data masked.
		call := IssueDraftCall(c.report())
		m := guard.NewMasker()
		req := llm.Request{Model: model, System: m.Mask(call.System), MaxTokens: call.MaxTokens, Schema: call.Schema}
		for _, msg := range call.Messages {
			req.Messages = append(req.Messages, llm.Message{Role: msg.Role, Text: m.Mask(msg.Text)})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		resp, err := p.Chat(ctx, req)
		cancel()
		in, out = in+resp.Usage.InputTokens, out+resp.Usage.OutputTokens
		var fails []string
		var d IssueText
		if err == nil {
			d, err = ParseIssueText(resp.JSON)
		}
		if err != nil {
			fails = []string{"error: " + err.Error()}
		} else {
			fails = gradeDraft(c, d)
		}
		if len(fails) == 0 {
			passed++
			t.Logf("PASS %-28s %s", c.ID, d.Title)
		} else {
			t.Logf("FAIL %-28s %s — %s", c.ID, d.Title, strings.Join(fails, "; "))
		}
	}
	t.Logf("%s %s: %d of %d passed, %d input and %d output tokens", provider, model, passed, len(cases), in, out)
	if passed*100 < len(cases)*80 {
		t.Errorf("%d of %d passed, want at least 80%%", passed, len(cases))
	}
}
