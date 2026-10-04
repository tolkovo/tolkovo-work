package integrations

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultTemplatesAreValid(t *testing.T) {
	for _, locale := range []string{"ru", "en"} {
		if err := DefaultTemplates(locale).validate(); err != nil {
			t.Errorf("%s: %v", locale, err)
		}
	}
	if !strings.Contains(DefaultTemplates("en").IssueBody, "## Steps") || !strings.Contains(DefaultTemplates("ru").IssueBody, "## Шаги") {
		t.Fatal("the issue template is not in the workspace language")
	}
}

func TestTemplatesValidate(t *testing.T) {
	ok := DefaultTemplates("ru")
	for name, c := range map[string]struct {
		mut  func(*Templates)
		want string
	}{
		"unknown placeholder": {func(t *Templates) { t.IssueBody = "{summary} {email}" }, "unknown placeholder {email}"},
		"reply placeholder":   {func(t *Templates) { t.ReplyRU = "Привет, {name}! Issue #{issue_number}, {summary}" }, "reply_ru has an unknown placeholder {summary}"},
		"empty title":         {func(t *Templates) { t.IssueTitle = "  " }, "issue_title must be 1 to 256"},
		"long title":          {func(t *Templates) { t.IssueTitle = strings.Repeat("я", 257) }, "issue_title must be 1 to 256"},
		"two-line title":      {func(t *Templates) { t.IssueTitle = "a\nb" }, "issue_title must be one line"},
		"long reply":          {func(t *Templates) { t.ReplyEN = strings.Repeat("a", 4001) }, "reply_en must be 1 to 4000"},
	} {
		tpl := ok
		c.mut(&tpl)
		err := tpl.validate()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Braces that are not placeholders are plain text.
	tpl := ok
	tpl.IssueBody = "JSON: {\"a\": 1} and {Summary} {summary}"
	if err := tpl.validate(); err != nil {
		t.Fatalf("plain braces: %v", err)
	}
}

func TestRender(t *testing.T) {
	got := Render("{summary} — {app_name} {app_version} {unknown}", map[string]string{"summary": "Падает", "app_name": "Зерно", "app_version": ""})
	if got != "Падает — Зерно  {unknown}" {
		t.Fatalf("Render: %q", got)
	}
}

func TestValidLabels(t *testing.T) {
	got, err := validLabels([]string{" bug ", "from-support", "Bug"})
	if err != nil || len(got) != 2 || got[0] != "bug" || got[1] != "from-support" {
		t.Fatalf("labels: %v %v", got, err)
	}
	for name, in := range map[string][]string{
		"empty":     {""},
		"long":      {strings.Repeat("x", 51)},
		"two lines": {"a\nb"},
		"too many":  {"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"},
	} {
		if _, err := validLabels(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
