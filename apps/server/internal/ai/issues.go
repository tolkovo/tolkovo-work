package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/prompts"
)

// The issue draft (docs/03 §8.1, AI-02): the model reads the user's report
// and the app's details and returns the parts of a bug report; the
// integrations module puts them into the workspace's issue template and
// the operator confirms the text. The report goes to the model as data
// inside <report>, and the user's own words still reach the issue only as
// the separate quote block the operator may keep.

// PurposeIssueDraft is the purpose of the issue draft in ai_calls.
const PurposeIssueDraft = "issue_draft"

// Limits of the issue draft.
const (
	// issueDraftTokens bounds the answer: a few short fields.
	issueDraftTokens = 1024
	// maxReportText bounds the report sent to the model.
	maxReportText = 6000
	maxDraftTitle = 120
	maxDraftField = 1500
	maxDraftSteps = 12
	maxDraftStep  = 300
)

// IssueReport is what the draft is made from: no contact data.
type IssueReport struct {
	ConversationID uuid.UUID
	// Locale is the language of the draft, ru or en.
	Locale string
	// Subject is the conversation's subject, if any.
	Subject string
	// Text is the user's report.
	Text string
	// App describes the reporting app; empty for other channels.
	App AppInfo
}

// AppInfo is what an app said about itself in its report.
type AppInfo struct {
	Name, Version, OS, OSVersion, Locale string
}

// IssueText is the model's draft. Masked personal data stays masked: the
// draft goes to the issue tracker, where contact data must not go.
type IssueText struct {
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Steps    []string `json:"steps"`
	Expected string   `json:"expected"`
	Actual   string   `json:"actual"`
}

var issueDraftSchema = &llm.Schema{
	Name: "issue_draft", Description: "A draft bug report for the developers", JSON: json.RawMessage(prompts.IssueDraftSchema),
}

// DraftIssue asks the workspace's model for an issue draft. Errors are
// those of Complete; llm.ErrBadOutput also when the answer has no title.
func (s *Service) DraftIssue(ctx context.Context, workspaceID uuid.UUID, in IssueReport) (IssueText, error) {
	res, err := s.Complete(ctx, workspaceID, IssueDraftCall(in))
	if err != nil {
		return IssueText{}, err
	}
	return ParseIssueText(res.JSON)
}

// IssueDraftCall is the call of the issue draft; the eval runs it too.
func IssueDraftCall(in IssueReport) Call {
	lang := "ru"
	if in.Locale == "en" {
		lang = "en"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<report lang=%q>\n", lang)
	for _, f := range []struct{ name, value string }{
		{"Subject", in.Subject},
		{"App", strings.TrimSpace(in.App.Name + " " + in.App.Version)},
		{"OS", strings.TrimSpace(in.App.OS + " " + in.App.OSVersion)},
		{"App locale", in.App.Locale},
	} {
		if v := oneLine(f.value); v != "" {
			b.WriteString(f.name + ": " + dataText(v) + "\n")
		}
	}
	b.WriteString("Text:\n" + dataText(clip(strings.TrimSpace(in.Text), maxReportText)) + "\n</report>")
	call := Call{
		Purpose: PurposeIssueDraft, System: prompts.IssueDraft, Schema: issueDraftSchema, MaxTokens: issueDraftTokens,
		Messages: []llm.Message{{Role: llm.User, Text: b.String()}},
	}
	if in.ConversationID != uuid.Nil {
		id := in.ConversationID
		call.ConversationID = &id
	}
	return call
}

// ParseIssueText reads and tidies the model's answer: fields trimmed and
// cut to their limits, empty steps dropped, the title on one line.
func ParseIssueText(raw json.RawMessage) (IssueText, error) {
	var t IssueText
	if err := json.Unmarshal(raw, &t); err != nil {
		return IssueText{}, &llm.Error{Kind: llm.ErrBadOutput, Code: "issue_draft"}
	}
	t.Title = clip(strings.TrimRight(oneLine(t.Title), "."), maxDraftTitle)
	if t.Title == "" {
		return IssueText{}, &llm.Error{Kind: llm.ErrBadOutput, Code: "issue_draft"}
	}
	t.Summary = clip(strings.TrimSpace(t.Summary), maxDraftField)
	t.Expected = clip(strings.TrimSpace(t.Expected), maxDraftField)
	t.Actual = clip(strings.TrimSpace(t.Actual), maxDraftField)
	steps := make([]string, 0, len(t.Steps))
	for _, st := range t.Steps {
		st = clip(stepNumber.ReplaceAllString(oneLine(st), ""), maxDraftStep)
		if st != "" && len(steps) < maxDraftSteps {
			steps = append(steps, st)
		}
	}
	t.Steps = steps
	return t, nil
}

// stepNumber is a number the model put before a step itself: the template
// numbers them.
var stepNumber = regexp.MustCompile(`^(\d{1,2}[.)]|[-*•])\s+`)

// reportTag finds anything that would close or open <report> in the data.
var reportTag = regexp.MustCompile(`(?i)</?\s*report`)

// dataText keeps the user's text from closing the <report> block early.
func dataText(s string) string {
	return reportTag.ReplaceAllStringFunc(s, func(m string) string { return strings.Replace(m, "<", "‹", 1) })
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clip cuts s to n characters, adding "…" when it cuts.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
