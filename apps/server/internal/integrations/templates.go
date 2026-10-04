package integrations

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Templates are the texts an issue and the reply about a fix start from
// (ADR-0012). The issue template is one; the reply has a text per
// contact language. Placeholders are {name}; unknown ones are refused
// when saved.
type Templates struct {
	IssueTitle string
	IssueBody  string
	ReplyRU    string
	ReplyEN    string
}

// IssuePlaceholders are the placeholders of the issue template, filled from
// the conversation and the app report (GH-02). The user's own words are not
// a placeholder: they go into a separate quote block the operator may turn
// off.
var IssuePlaceholders = []string{"summary", "app_name", "app_version", "os", "os_version", "conversation_url"}

// ReplyPlaceholders are the placeholders of the reply about a fix (GH-03).
var ReplyPlaceholders = []string{"name", "issue_number"}

// Template limits; GitHub caps an issue title at 256 characters.
const (
	maxIssueTitle = 256
	maxIssueBody  = 20000
	maxReply      = 4000
)

// DefaultTemplates returns the built-in templates; the issue template is in
// the workspace language (ru or en).
func DefaultTemplates(locale string) Templates {
	t := Templates{
		IssueTitle: "{summary}",
		ReplyRU:    "Исправили, исправление войдёт в ближайшее обновление.",
		ReplyEN:    "We have fixed it. The fix will be in the next update.",
	}
	if locale == "en" {
		t.IssueBody = "## What happens\n{summary}\n\n## Steps\n1. \n\n## Expected\n\n## Environment\n" +
			"- App: {app_name} {app_version}\n- OS: {os} {os_version}\n\n## Conversation\n{conversation_url}\n"
	} else {
		t.IssueBody = "## Что происходит\n{summary}\n\n## Шаги\n1. \n\n## Ожидалось\n\n## Окружение\n" +
			"- Приложение: {app_name} {app_version}\n- ОС: {os} {os_version}\n\n## Диалог\n{conversation_url}\n"
	}
	return t
}

var placeholderRE = regexp.MustCompile(`\{([a-z_]+)\}`)

// Render fills the placeholders; unknown ones stay as written.
func Render(tpl string, vars map[string]string) string {
	return placeholderRE.ReplaceAllStringFunc(tpl, func(m string) string {
		if v, ok := vars[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// validateTemplate checks the length and the placeholders of one template.
func validateTemplate(field, tpl string, maxLen int, allowed []string) error {
	if strings.TrimSpace(tpl) == "" || utf8.RuneCountInString(tpl) > maxLen || !utf8.ValidString(tpl) {
		return fmt.Errorf("%w: %s must be 1 to %d characters", ErrInvalid, field, maxLen)
	}
	for _, m := range placeholderRE.FindAllStringSubmatch(tpl, -1) {
		if !slices.Contains(allowed, m[1]) {
			return fmt.Errorf("%w: %s has an unknown placeholder {%s}; known: {%s}", ErrInvalid, field, m[1], strings.Join(allowed, "}, {"))
		}
	}
	return nil
}

func (t Templates) validate() error {
	for _, f := range []struct {
		field, tpl string
		max        int
		allowed    []string
	}{
		{"issue_title", t.IssueTitle, maxIssueTitle, IssuePlaceholders},
		{"issue_body", t.IssueBody, maxIssueBody, IssuePlaceholders},
		{"reply_ru", t.ReplyRU, maxReply, ReplyPlaceholders},
		{"reply_en", t.ReplyEN, maxReply, ReplyPlaceholders},
	} {
		if err := validateTemplate(f.field, f.tpl, f.max, f.allowed); err != nil {
			return err
		}
	}
	if strings.ContainsAny(t.IssueTitle, "\r\n") {
		return fmt.Errorf("%w: issue_title must be one line", ErrInvalid)
	}
	return nil
}
