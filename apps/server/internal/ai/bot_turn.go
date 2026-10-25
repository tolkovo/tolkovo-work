package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/prompts"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// PurposeBotReply is the purpose of the bot's turns in ai_calls.
const PurposeBotReply = "bot_reply"

// Limits of a turn.
const (
	botAnswerTokens = 1500
	// maxBotAnswer bounds what the customer reads in one reply.
	maxBotAnswer = 2000
	// maxBotLine bounds one message of the conversation sent to the model.
	maxBotLine = 1500
	// maxEntryText bounds one entry of the knowledge base sent to the model.
	maxEntryText = 4000
	// maxCollected bounds the details collected for a task.
	maxCollected = 20
	maxItemText  = 500
)

// What the model may answer (prompts/bot_reply.schema.json).
const (
	answerFromKB  = "answer"
	answerClarify = "clarify"
	answerCollect = "collect"
	answerHandoff = "handoff"
)

// BotAnswer is the model's answer in one turn.
type BotAnswer struct {
	Answer        string   `json:"answer"`
	AnswerType    string   `json:"answer_type"`
	Citations     []int    `json:"citations"`
	Confidence    float64  `json:"confidence"`
	HandoffReason string   `json:"handoff_reason"`
	Summary       string   `json:"summary"`
	Collected     []Detail `json:"collected"`
	Missing       []string `json:"missing"`
	TaskTitle     string   `json:"task_title"`
}

// Detail is an item of the task checklist with what the customer told.
type Detail struct {
	Item  string `json:"item"`
	Value string `json:"value"`
}

var botReplySchema = &llm.Schema{Name: "bot_reply", Description: "The reply of the support bot", JSON: json.RawMessage(prompts.BotReplySchema)}

// plan is what a turn decided before it is recorded.
type plan struct {
	// waitTopic: the customer is choosing the topic; the choice wakes the bot.
	waitTopic bool
	turn      conversations.BotTurn
	// task is created in the turn's transaction; its number joins the reply.
	task         *taskPlan
	collected    []Detail
	collectTurns int
	locale       string
	// fallback is what the customer reads on a handoff without the model's words.
	fallback string
}

type taskPlan struct{ title, body string }

// plan decides the turn: everything up to the model's checked answer, no
// writes. Errors are those of the database; the model's failures hand over.
func (s *Service) plan(ctx context.Context, ws, convID uuid.UUID, view conversations.BotView, st store.AiBotConversation) (plan, error) {
	settings, err := s.BotSettings(ctx, ws)
	if err != nil {
		return plan{}, err
	}
	p := plan{locale: view.Locale, collectTurns: int(st.CollectTurns)}
	if len(st.Collected) > 2 {
		_ = json.Unmarshal(st.Collected, &p.collected) // written only by apply
	}
	p.fallback = settings.HandoffMessage
	if p.fallback == "" {
		p.fallback = phrase(view.Locale, phraseHandoff)
	}
	handOff := func(reason, reply, note string) plan {
		if reply == "" {
			reply = p.fallback
		}
		p.turn = conversations.BotTurn{Reply: reply, HandOff: true, Reason: reason, Note: note}
		return p
	}
	pending := pendingLines(view.Lines, st.AnsweredUpTo)
	switch {
	case int(st.Replies) >= MaxBotReplies:
		return handOff(conversations.HandoffLimit, "", p.note("")), nil
	case asksForPerson(pending):
		return handOff(conversations.HandoffAsked, "", p.note("")), nil
	case view.TopicID == nil && view.TopicAsked && customerLines(view.Lines) == 1:
		p.waitTopic = true
		return p, nil
	}
	if view.TopicID != nil && slices.Contains(settings.TopicsOff, *view.TopicID) {
		return handOff(conversations.HandoffTopic, "", ""), nil
	}
	guide, err := s.Guide(ctx, ws)
	if err != nil {
		return plan{}, err
	}
	hits, err := s.Search(ctx, ws, searchText(view.Lines, pending), view.TopicID, SearchLimit)
	if err != nil {
		return plan{}, err
	}
	call := BotReplyCall(BotInput{
		ConversationID: convID, Guide: guide, Hits: hits, Lines: view.Lines,
		FirstReply: st.Replies == 0 && !botWrote(view.Lines), Collected: p.collected, Persona: settings.Persona(),
	})
	res, err := s.Complete(ctx, ws, call)
	if err != nil {
		var le *llm.Error
		if errors.Is(err, ErrNotConfigured) || errors.Is(err, ErrBudgetExceeded) || errors.Is(err, ErrNoEncryption) || errors.As(err, &le) {
			return handOff(conversations.HandoffUnavailable, "", p.note("")), nil
		}
		return plan{}, err
	}
	a, ok := parseBotAnswer(res.JSON)
	if !ok { // not the object asked for: people answer
		return handOff(conversations.HandoffChecks, "", p.note("")), nil
	}
	a = unmaskAnswer(res, a)
	v := CheckAnswer(a, CheckInput{
		Hits: hits, Guide: guide, Customer: customerText(view.Lines), MinConfidence: settings.MinConfidence,
	})
	if len(v.Collected) > 0 {
		p.collected = v.Collected // the model returns everything known so far
	}
	summary := p.note(oneLine(a.Summary))
	switch v.Outcome {
	case OutcomeReply:
		p.turn = conversations.BotTurn{Reply: v.Reply}
		if a.AnswerType == answerCollect {
			p.collectTurns++
			if p.collectTurns >= MaxCollectTurns {
				return handOff(conversations.HandoffIncomplete, "", summary), nil
			}
		}
		return p, nil
	case OutcomeTask:
		p.collectTurns++
		p.turn = conversations.BotTurn{Reply: v.Reply, Note: summary}
		p.task = &taskPlan{title: v.TaskTitle, body: taskBody(oneLine(a.Summary), p.collected)}
		return p, nil
	case OutcomeHandoff:
		return handOff(v.Reason, v.Reply, summary), nil
	}
	return handOff(conversations.HandoffChecks, "", summary), nil
}

func parseBotAnswer(raw json.RawMessage) (BotAnswer, bool) {
	var a BotAnswer
	return a, json.Unmarshal(raw, &a) == nil
}

// note is what operators read when the bot hands over: what the customer
// wants and the details collected.
func (p plan) note(summary string) string {
	var b strings.Builder
	if summary != "" {
		b.WriteString(summary)
	}
	if len(p.collected) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(phrase(p.locale, phraseCollected))
		for _, d := range p.collected {
			b.WriteString("\n— " + d.Item + ": " + d.Value)
		}
	}
	return clip(b.String(), conversations.MaxBodyLength)
}

func taskBody(summary string, details []Detail) string {
	var b strings.Builder
	b.WriteString(summary)
	for i, d := range details {
		if i == 0 && b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("\n" + d.Item + ": " + d.Value)
	}
	return b.String()
}

func unmaskAnswer(res Result, a BotAnswer) BotAnswer {
	a.Answer, a.Summary, a.TaskTitle = res.Unmask(a.Answer), res.Unmask(a.Summary), res.Unmask(a.TaskTitle)
	for i := range a.Collected {
		a.Collected[i].Value = res.Unmask(a.Collected[i].Value)
	}
	return a
}

// pendingLines are the customer's messages after the last one a turn took.
func pendingLines(lines []conversations.BotLine, answered *uuid.UUID) []conversations.BotLine {
	var out []conversations.BotLine
	for _, l := range lines {
		if l.From == conversations.SenderContact && (answered == nil || after(l.ID, *answered)) {
			out = append(out, l)
		}
	}
	return out
}

func customerLines(lines []conversations.BotLine) int {
	n := 0
	for _, l := range lines {
		if l.From == conversations.SenderContact {
			n++
		}
	}
	return n
}

func botWrote(lines []conversations.BotLine) bool {
	return slices.ContainsFunc(lines, func(l conversations.BotLine) bool { return l.From == conversations.SenderBot })
}

// customerText is everything the customer wrote that the model reads.
func customerText(lines []conversations.BotLine) string {
	var b strings.Builder
	for _, l := range lines {
		if l.From == conversations.SenderContact {
			b.WriteString(l.Text + "\n")
		}
	}
	return b.String()
}

// searchText is what the knowledge base is searched by: the customer's new
// messages, and the one before them when they are only a few words ("and
// on iOS?").
func searchText(lines []conversations.BotLine, pending []conversations.BotLine) string {
	var parts []string
	for _, l := range pending {
		parts = append(parts, l.Text)
	}
	text := strings.Join(parts, " ")
	if len(strings.Fields(text)) >= 4 || len(pending) == 0 {
		return text
	}
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if l.From == conversations.SenderContact && after(pending[0].ID, l.ID) {
			return l.Text + " " + text
		}
	}
	return text
}

// linkWords are skipped between the words of a request: "соедините со
// специалистом", "talk to a real person".
var linkWords = []string{"с", "со", "к", "ко", "мне", "нам", "пожалуйста", "with", "to", "a", "me", "please"}

// asksForPerson finds a request for a person in the customer's messages,
// in Russian or English, without asking the model.
func asksForPerson(lines []conversations.BotLine) bool {
	for _, l := range lines {
		words := strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(l.Text), "ё", "е"), func(r rune) bool {
			return !unicode.IsLetter(r)
		})
		for i, w := range words {
			next := ""
			for _, n := range words[i+1:] {
				if !slices.Contains(linkWords, n) {
					next = n
					break
				}
			}
			switch {
			case strings.HasPrefix(w, "оператор"), w == "operator", w == "human", w == "humans":
				return true
			case strings.HasPrefix(w, "жив") && (strings.HasPrefix(next, "человек") || strings.HasPrefix(next, "сотрудник") || strings.HasPrefix(next, "оператор")):
				return true
			case (strings.HasPrefix(w, "позов") || strings.HasPrefix(w, "позв") || strings.HasPrefix(w, "соедин")) &&
				(strings.HasPrefix(next, "человек") || strings.HasPrefix(next, "сотрудник") || strings.HasPrefix(next, "специалист") || strings.HasPrefix(next, "менеджер")):
				return true
			case (w == "real" || w == "live") && (next == "person" || next == "agent" || next == "human"):
				return true
			}
		}
	}
	return false
}

// BotInput is what one turn of the bot is asked from.
type BotInput struct {
	ConversationID uuid.UUID
	Guide          Guide
	Hits           []Hit
	Lines          []conversations.BotLine
	FirstReply     bool
	Collected      []Detail
	// Persona is how the bot speaks.
	Persona Persona
}

// Persona is how the bot speaks (AI-05): its name, tone, form of address
// and the company's own rules.
type Persona struct {
	Name, Tone, Address, Rules string
}

// Persona returns how the bot of these settings speaks.
func (b BotSettings) Persona() Persona {
	return Persona{Name: b.Name, Tone: b.Tone, Address: b.Address, Rules: b.Rules}
}

// BotReplyCall is the call of a turn; the eval runs it too. The rules and
// what the bot always knows go first, the same for every turn of the
// workspace, so providers can cache them; the turn's data follows.
func BotReplyCall(in BotInput) Call {
	var sys strings.Builder
	sys.WriteString(prompts.BotReply)
	pe := in.Persona
	name := oneLine(pe.Name)
	if name == "" {
		name = "none"
	}
	tone, address := pe.Tone, pe.Address
	if tone == "" {
		tone = ToneFriendly
	}
	if address == "" {
		address = AddressFormal
	}
	fmt.Fprintf(&sys, "\n<assistant>\nname: %s\ntone: %s\naddress: %s\n</assistant>\n", botData(name), tone, address)
	if rules := strings.TrimSpace(pe.Rules); rules != "" {
		fmt.Fprintf(&sys, "\n<company_rules>\n%s\n</company_rules>\n", botData(rules))
	}
	for _, part := range []struct{ tag, text string }{
		{"company", in.Guide.About}, {"handoff_rules", in.Guide.HandoffRules}, {"task_checklist", in.Guide.TaskChecklist},
	} {
		fmt.Fprintf(&sys, "\n<%s>\n%s\n</%s>\n", part.tag, botData(part.text), part.tag)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<turn first_reply=%q collecting=%q></turn>\n<knowledge>\n", strconv.FormatBool(in.FirstReply), strconv.FormatBool(len(in.Collected) > 0))
	for i, h := range in.Hits {
		fmt.Fprintf(&b, "<entry n=\"%d\" kind=%q title=%q>\n%s\n</entry>\n", i+1, h.Kind, botData(oneLine(h.Title)), botData(clip(h.Body, maxEntryText)))
	}
	b.WriteString("</knowledge>\n")
	if len(in.Collected) > 0 {
		b.WriteString("<collected>\n")
		for _, d := range in.Collected {
			b.WriteString("- " + botData(oneLine(d.Item)) + ": " + botData(oneLine(d.Value)) + "\n")
		}
		b.WriteString("</collected>\n")
	}
	b.WriteString("<conversation>\n")
	for _, l := range in.Lines {
		who := "customer"
		switch l.From {
		case conversations.SenderBot:
			who = "assistant"
		case conversations.SenderOperator:
			who = "operator"
		}
		text := botData(clip(strings.TrimSpace(l.Text), maxBotLine))
		if l.Files > 0 {
			text = strings.TrimSpace(text + fmt.Sprintf(" (sent %d file(s))", l.Files))
		}
		b.WriteString("[" + who + "] " + text + "\n")
	}
	b.WriteString("</conversation>")
	call := Call{
		Purpose: PurposeBotReply, System: sys.String(), Schema: botReplySchema, MaxTokens: botAnswerTokens, CacheSystem: true,
		Messages: []llm.Message{{Role: llm.User, Text: b.String()}},
	}
	if in.ConversationID != uuid.Nil {
		id := in.ConversationID
		call.ConversationID = &id
	}
	return call
}

// botTag finds anything that would open or close a block of the prompt.
var botTag = regexp.MustCompile(`(?i)</?\s*(company|assistant|handoff_rules|task_checklist|turn|knowledge|entry|collected|conversation)`)

// botData keeps data from closing a block of the prompt early.
func botData(s string) string {
	return botTag.ReplaceAllStringFunc(s, func(m string) string { return strings.Replace(m, "<", "‹", 1) })
}

// Outcomes of CheckAnswer.
const (
	OutcomeReply   = "reply"
	OutcomeTask    = "task"
	OutcomeHandoff = "handoff"
)

// CheckInput is what an answer is checked against.
type CheckInput struct {
	Hits          []Hit
	Guide         Guide
	Customer      string
	MinConfidence int
}

// Verdict is the checked answer.
type Verdict struct {
	Outcome string
	// Reply is what the customer reads; empty on a handoff without the
	// model's words.
	Reply  string
	Reason string
	// Collected are the details for a task, tidied.
	Collected []Detail
	TaskTitle string
}

// CheckAnswer checks the model's answer before the customer sees it
// (ADR-0018): an answer from the knowledge base cites existing entries and
// the model is sure enough of it; numbers and links in any reply come from
// the cited entries, the company description or the customer's own words.
// What fails goes to people instead, without the model's words.
func CheckAnswer(a BotAnswer, in CheckInput) Verdict {
	v := Verdict{Collected: tidyDetails(a.Collected)}
	answer := strings.TrimSpace(a.Answer)
	fail := Verdict{Outcome: OutcomeHandoff, Reason: conversations.HandoffChecks, Collected: v.Collected}
	if utf8.RuneCountInString(answer) > maxBotAnswer || !validText(answer) {
		return fail
	}
	cited := make([]Hit, 0, len(a.Citations))
	for _, n := range a.Citations {
		if n < 1 || n > len(in.Hits) {
			return fail
		}
		if h := in.Hits[n-1]; !slices.ContainsFunc(cited, func(c Hit) bool { return c.ID == h.ID }) {
			cited = append(cited, h)
		}
	}
	known := knownText(cited, in.Guide, in.Customer)
	grounded := groundedIn(answer, known)
	switch a.AnswerType {
	case answerHandoff:
		v.Outcome, v.Reason = OutcomeHandoff, conversations.HandoffUnknown
		switch a.HandoffReason {
		case "customer_asked":
			v.Reason = conversations.HandoffAsked
		case "rules":
			v.Reason = conversations.HandoffRules
		}
		if grounded {
			v.Reply = answer
		}
		return v
	case answerFromKB:
		if answer == "" || len(cited) == 0 || a.Confidence*100 < float64(in.MinConfidence) || !grounded {
			return fail
		}
		v.Outcome, v.Reply = OutcomeReply, withSource(answer, cited)
		return v
	case answerClarify:
		if answer == "" || !grounded {
			return fail
		}
		v.Outcome, v.Reply = OutcomeReply, answer
		return v
	case answerCollect:
		if strings.TrimSpace(in.Guide.TaskChecklist) == "" || !grounded {
			return fail
		}
		if len(a.Missing) == 0 && len(v.Collected) > 0 {
			v.Outcome, v.Reply = OutcomeTask, answer
			v.TaskTitle = clip(strings.TrimRight(oneLine(a.TaskTitle), "."), maxDraftTitle)
			if v.TaskTitle == "" {
				v.TaskTitle = clip(oneLine(v.Collected[0].Value), maxDraftTitle)
			}
			return v
		}
		if answer == "" {
			return fail
		}
		v.Outcome, v.Reply = OutcomeReply, answer
		return v
	}
	return fail
}

func tidyDetails(in []Detail) []Detail {
	out := make([]Detail, 0, len(in))
	for _, d := range in {
		d.Item, d.Value = clip(oneLine(d.Item), maxItemText), clip(strings.TrimSpace(d.Value), maxItemText)
		if d.Item != "" && d.Value != "" && validText(d.Item+d.Value) && len(out) < maxCollected {
			out = append(out, d)
		}
	}
	return out
}

// knownText is what a reply may take numbers and links from.
func knownText(cited []Hit, g Guide, customer string) string {
	var b strings.Builder
	for _, h := range cited {
		b.WriteString(h.Title + "\n" + h.Body + "\n" + h.URL + "\n")
	}
	b.WriteString(g.About + "\n" + customer)
	return strings.ToLower(b.String())
}

var (
	numberRE = regexp.MustCompile(`\d+`)
	linkRE   = regexp.MustCompile(`(?i)\bhttps?://[^\s<>()"'«»]+`)
)

// groundedIn reports whether every number and link of the answer is in known.
func groundedIn(answer, known string) bool {
	for _, link := range linkRE.FindAllString(answer, -1) {
		if !strings.Contains(known, strings.ToLower(strings.TrimRight(link, ".,;:!?"))) {
			return false
		}
	}
	for _, n := range numberRE.FindAllString(answer, -1) {
		if !strings.Contains(known, n) {
			return false
		}
	}
	return true
}

// withSource adds the link of the first cited entry that has one, unless
// the answer gives it already.
func withSource(answer string, cited []Hit) string {
	for _, h := range cited {
		if h.URL != "" {
			if strings.Contains(answer, h.URL) {
				return answer
			}
			return answer + "\n\n" + h.URL
		}
	}
	return answer
}

func validText(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }

// Phrases of the bot that do not come from the model.
const (
	phraseHandoff = iota
	phraseTask
	phraseCollected
)

var phrases = map[string][]string{
	"ru": {
		phraseHandoff:   "Передаю ваш вопрос сотруднику поддержки. Ответ придёт сюда, в этот чат.",
		phraseTask:      "Задача №%d передана технической команде. Ответ придёт сюда, в этот чат.",
		phraseCollected: "Собрано для задачи:",
	},
	"en": {
		phraseHandoff:   "I'm passing your question to our support team. The reply will come here, in this chat.",
		phraseTask:      "Task #%d is with our technical team. The reply will come here, in this chat.",
		phraseCollected: "Collected for a task:",
	},
}

// phrase is a phrase in the workspace's language, Russian by default.
func phrase(locale string, p int) string {
	if l, ok := phrases[locale]; ok {
		return l[p]
	}
	return phrases["ru"][p]
}
