package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/guard"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// The eval of the bot's replies (docs/00 §10.2, ADR-0018): conversations in
// testdata/bot_reply_eval.json over one knowledge base, each with the
// entries the search would find and what the bot must do after the checks
// of the code. It asks a real model, so it runs only when asked, like
// TestIssueDraftEval:
//
//	TOLKOVO_EVAL_AI_PROVIDER=anthropic TOLKOVO_EVAL_AI_MODEL=claude-haiku-4-5 \
//	TOLKOVO_EVAL_AI_KEY=... go test -run TestBotReplyEval -v ./internal/ai/
//
// A change of the prompt, the schema, the checks or the model comes with
// its result, before and after.

type botEvalSet struct {
	Guide struct {
		About         string `json:"about"`
		HandoffRules  string `json:"handoff_rules"`
		TaskChecklist string `json:"task_checklist"`
	} `json:"guide"`
	Entries []struct {
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Body  string `json:"body"`
		URL   string `json:"url"`
	} `json:"entries"`
	Cases []botEvalCase `json:"cases"`
}

type botEvalCase struct {
	ID      string `json:"id"`
	Locale  string `json:"locale"`
	Persona *struct {
		Name    string `json:"name"`
		Tone    string `json:"tone"`
		Address string `json:"address"`
		Rules   string `json:"rules"`
	} `json:"persona"`
	Lines []struct {
		From string `json:"from"`
		Text string `json:"text"`
	} `json:"lines"`
	Collected []Detail `json:"collected"`
	// Hits are the entries the search finds, numbered from 1 in Entries.
	Hits []int `json:"hits"`
	// Expect or ExpectAny: the outcome of CheckAnswer.
	Expect    string   `json:"expect"`
	ExpectAny []string `json:"expect_any"`
	// AnswerType or AnswerTypeAny: the model's answer_type, when it matters.
	AnswerType    string   `json:"answer_type"`
	AnswerTypeAny []string `json:"answer_type_any"`
	// Reason of a handoff, when it matters.
	Reason string `json:"reason"`
	// Cite: entries (as in Hits) the answer must cite.
	Cite []int `json:"cite"`
	// MustMention: every group needs one of its words in the reply.
	MustMention [][]string `json:"must_mention"`
	// Forbid: none of these may appear in the reply.
	Forbid []string `json:"forbid"`
}

func (c botEvalCase) outcomes() []string {
	if len(c.ExpectAny) > 0 {
		return c.ExpectAny
	}
	return []string{c.Expect}
}

func (c botEvalCase) answerTypes() []string {
	if len(c.AnswerTypeAny) > 0 {
		return c.AnswerTypeAny
	}
	if c.AnswerType != "" {
		return []string{c.AnswerType}
	}
	return nil
}

func loadBotEvalSet(t *testing.T) botEvalSet {
	t.Helper()
	raw, err := os.ReadFile("testdata/bot_reply_eval.json")
	if err != nil {
		t.Fatalf("read eval set: %v", err)
	}
	var set botEvalSet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatalf("parse eval set: %v", err)
	}
	return set
}

// input builds the turn as the bot would ask it, and what it is checked
// against.
func (set botEvalSet) input(c botEvalCase) (BotInput, CheckInput) {
	in := BotInput{
		ConversationID: uuid.Nil, Collected: c.Collected,
		Guide: Guide{About: set.Guide.About, HandoffRules: set.Guide.HandoffRules, TaskChecklist: set.Guide.TaskChecklist},
	}
	if p := c.Persona; p != nil {
		in.Persona = Persona{Name: p.Name, Tone: p.Tone, Address: p.Address, Rules: p.Rules}
	}
	for _, n := range c.Hits {
		e := set.Entries[n-1]
		in.Hits = append(in.Hits, Hit{ID: uuid.NewSHA1(uuid.Nil, []byte(e.Title)), Kind: e.Kind, Title: e.Title, Body: e.Body, URL: e.URL})
	}
	in.FirstReply = true
	for i, l := range c.Lines {
		from := conversations.SenderContact
		if l.From == "assistant" {
			from, in.FirstReply = conversations.SenderBot, false
		}
		in.Lines = append(in.Lines, conversations.BotLine{
			ID: uuid.NewSHA1(uuid.Nil, fmt.Appendf(nil, "%s/%d", c.ID, i)), From: from, Text: l.Text,
		})
	}
	return in, CheckInput{Hits: in.Hits, Guide: in.Guide, Customer: customerText(in.Lines), MinConfidence: DefaultMinConfidence}
}

// gradeBotReply returns what the turn fails; none means it passes.
func gradeBotReply(c botEvalCase, hits []int, a BotAnswer, v Verdict) []string {
	var fails []string
	if !slices.Contains(c.outcomes(), v.Outcome) {
		fails = append(fails, fmt.Sprintf("outcome %s (%s), want %s", v.Outcome, v.Reason, strings.Join(c.outcomes(), "/")))
	}
	if types := c.answerTypes(); len(types) > 0 && !slices.Contains(types, a.AnswerType) {
		fails = append(fails, fmt.Sprintf("answer_type %s, want %s", a.AnswerType, strings.Join(types, "/")))
	}
	if c.Reason != "" && v.Outcome == OutcomeHandoff && v.Reason != c.Reason {
		fails = append(fails, fmt.Sprintf("reason %s, want %s", v.Reason, c.Reason))
	}
	for _, n := range c.Cite {
		if k := slices.Index(hits, n); k < 0 || !slices.Contains(a.Citations, k+1) {
			fails = append(fails, fmt.Sprintf("does not cite entry %d", n))
		}
	}
	reply := strings.ToLower(v.Reply)
	for _, group := range c.MustMention {
		if !slices.ContainsFunc(group, func(w string) bool { return strings.Contains(reply, strings.ToLower(w)) }) {
			fails = append(fails, "does not mention "+strings.Join(group, "/"))
		}
	}
	for _, w := range c.Forbid {
		if strings.Contains(reply, strings.ToLower(w)) {
			fails = append(fails, "says "+w)
		}
	}
	if v.Reply != "" && !inLanguage(v.Reply, c.Locale) {
		fails = append(fails, "not in "+c.Locale)
	}
	return fails
}

// The set itself is checked on every run, and so are the checks.
func TestBotReplyEval_Set(t *testing.T) {
	set := loadBotEvalSet(t)
	if len(set.Cases) < 20 {
		t.Fatalf("%d cases, want at least 20", len(set.Cases))
	}
	outcomes := []string{OutcomeReply, OutcomeTask, OutcomeHandoff}
	seen := map[string]bool{}
	for _, c := range set.Cases {
		ok := c.ID != "" && !seen[c.ID] && (c.Locale == "ru" || c.Locale == "en") && len(c.Lines) > 0 &&
			c.Lines[len(c.Lines)-1].From == "customer"
		for _, o := range c.outcomes() {
			ok = ok && slices.Contains(outcomes, o)
		}
		for _, n := range c.Hits {
			ok = ok && n >= 1 && n <= len(set.Entries)
		}
		for _, n := range c.Cite {
			ok = ok && slices.Contains(c.Hits, n)
		}
		if !ok {
			t.Errorf("bad case %q", c.ID)
		}
		seen[c.ID] = true
		// Every request builds.
		in, _ := set.input(c)
		if call := BotReplyCall(in); call.System == "" || len(call.Messages) == 0 {
			t.Errorf("%s: empty call", c.ID)
		}
	}

	c := botEvalCase{Locale: "ru", Expect: OutcomeReply, AnswerType: answerFromKB, Cite: []int{3}, MustMention: [][]string{{"настройки"}}, Forbid: []string{"скоро"}}
	good := BotAnswer{AnswerType: answerFromKB, Citations: []int{2}}
	if fails := gradeBotReply(c, []int{1, 3}, good, Verdict{Outcome: OutcomeReply, Reply: "Откройте «Настройки → Аккаунты»."}); len(fails) != 0 {
		t.Fatalf("good reply fails: %v", fails)
	}
	bad := BotAnswer{AnswerType: answerClarify, Citations: []int{1}}
	if fails := gradeBotReply(c, []int{1, 3}, bad, Verdict{Outcome: OutcomeHandoff, Reply: "Coming soon"}); len(fails) != 5 {
		t.Fatalf("bad reply: %v", fails)
	}
}

func TestBotReplyEval(t *testing.T) {
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
	set := loadBotEvalSet(t)
	passed, in, out := 0, 0, 0
	for _, c := range set.Cases {
		bi, check := set.input(c)
		call := BotReplyCall(bi)
		// The request as Complete sends it: personal data masked.
		m := guard.NewMasker()
		req := llm.Request{Model: model, System: m.Mask(call.System), MaxTokens: call.MaxTokens, Schema: call.Schema, CacheSystem: call.CacheSystem}
		for _, msg := range call.Messages {
			req.Messages = append(req.Messages, llm.Message{Role: msg.Role, Text: m.Mask(msg.Text)})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		resp, err := p.Chat(ctx, req)
		cancel()
		in, out = in+resp.Usage.InputTokens, out+resp.Usage.OutputTokens
		var fails []string
		var v Verdict
		if err != nil {
			fails = []string{"error: " + err.Error()}
		} else if a, ok := parseBotAnswer(resp.JSON); !ok {
			fails = []string{"not the reply asked for"}
		} else {
			a.Answer = m.Unmask(a.Answer)
			v = CheckAnswer(a, check)
			fails = gradeBotReply(c, c.Hits, a, v)
		}
		if len(fails) == 0 {
			passed++
			t.Logf("PASS %-20s %s %q", c.ID, v.Outcome, clip(oneLine(v.Reply), 120))
		} else {
			t.Logf("FAIL %-20s %q — %s", c.ID, clip(oneLine(v.Reply), 120), strings.Join(fails, "; "))
		}
	}
	t.Logf("%s %s: %d of %d passed, %d input and %d output tokens", provider, model, passed, len(set.Cases), in, out)
	if passed*100 < len(set.Cases)*80 {
		t.Errorf("%d of %d passed, want at least 80%%", passed, len(set.Cases))
	}
}
