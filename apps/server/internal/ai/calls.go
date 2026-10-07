package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/guard"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/prompts"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// Purposes of calls, as ai_calls.purpose.
const (
	PurposeConnectionCheck = "connection_check"
)

// MaxAnswerTokens bounds Call.MaxTokens.
const MaxAnswerTokens = 8192

var purposeRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)

// Call is one request to the workspace's model. System is the module's
// prompt (internal/ai/prompts); what customers wrote goes into Messages,
// marked as data by the prompt (docs/03 §10.1). Personal data is masked in
// both before they leave.
type Call struct {
	Purpose        string
	ConversationID *uuid.UUID
	System         string
	Messages       []llm.Message
	// Schema asks for Result.JSON.
	Schema    *llm.Schema
	MaxTokens int
	// CacheSystem: System is the same for many calls (llm.Request.CacheSystem).
	CacheSystem bool
}

// Result is the model's answer, still masked: Unmask puts personal data
// back where the caller wants it.
type Result struct {
	Text       string
	JSON       json.RawMessage
	Model      string
	Usage      llm.Usage
	CostMicros int64
	masker     *guard.Masker
	latency    time.Duration
}

// Unmask puts the masked values back into s, except cards and CVV.
func (r Result) Unmask(s string) string {
	if r.masker == nil {
		return s
	}
	return r.masker.Unmask(s)
}

// Complete asks the model. ErrNotConfigured when AI is off or incomplete,
// ErrBudgetExceeded when the month's budget is spent; the provider's
// failures come as *llm.Error. Every attempt that reached the provider is
// in ai_calls, failed or not.
func (s *Service) Complete(ctx context.Context, workspaceID uuid.UUID, call Call) (Result, error) {
	return s.complete(ctx, workspaceID, call, true)
}

// CheckResult is a successful connection check.
type CheckResult struct {
	Model   string
	Latency time.Duration
}

// Check sends a tiny request with the saved settings, on or off, so the
// admin knows the key and the model work. An answer cut off at the token
// limit counts: the model did answer.
func (s *Service) Check(ctx context.Context, workspaceID uuid.UUID) (CheckResult, error) {
	start := time.Now()
	res, err := s.complete(ctx, workspaceID, Call{
		Purpose: PurposeConnectionCheck, System: prompts.ConnectionCheck, MaxTokens: 16,
		Messages: []llm.Message{{Role: llm.User, Text: "OK?"}},
	}, false)
	if err != nil && !errors.Is(err, llm.ErrTruncated) {
		return CheckResult{}, err
	}
	return CheckResult{Model: res.Model, Latency: time.Since(start)}, nil
}

// target is what a call needs from the settings.
type target struct {
	settings store.AiSetting
	key      crypto.Secret
	spent    int64
	// reservation holds the call's share of the budget until it is logged;
	// uuid.Nil without a budget.
	reservation uuid.UUID
}

func (s *Service) complete(ctx context.Context, workspaceID uuid.UUID, call Call, needEnabled bool) (Result, error) {
	if !purposeRE.MatchString(call.Purpose) || call.MaxTokens <= 0 || call.MaxTokens > MaxAnswerTokens || len(call.Messages) == 0 {
		return Result{}, fmt.Errorf("%w: a call needs a purpose, messages and 1 to %d tokens", ErrInvalid, MaxAnswerTokens)
	}
	masker := guard.NewMasker()
	req := llm.Request{
		System: masker.Mask(call.System), MaxTokens: call.MaxTokens, Schema: call.Schema, CacheSystem: call.CacheSystem,
		Messages: make([]llm.Message, 0, len(call.Messages)),
	}
	for _, m := range call.Messages {
		req.Messages = append(req.Messages, llm.Message{Role: m.Role, Text: masker.Mask(m.Text)})
	}
	t, err := s.target(ctx, workspaceID, needEnabled, req)
	if err != nil {
		return Result{}, err
	}
	req.Model = *t.settings.Model
	// The log is written on a context of its own: a timed-out call is
	// still recorded and paid for, and its reservation given back.
	logCtx, logCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second+s.opts.Timeout)
	defer logCancel()
	provider, err := s.provider(t)
	if err != nil {
		return Result{}, errors.Join(err, s.release(logCtx, workspaceID, t.reservation))
	}

	ctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()
	attempt := func() (Result, error) {
		started := time.Now()
		resp, err := provider.Chat(ctx, req)
		res := Result{Text: resp.Text, JSON: resp.JSON, Model: resp.Model, Usage: resp.Usage, masker: masker, latency: time.Since(started)}
		if res.Model == "" {
			res.Model = req.Model
		}
		res.CostMicros = cost(resp.Usage, t.settings)
		return res, err
	}
	res, err := attempt()
	if wait, again := s.retry(ctx, err); again {
		// The first attempt is logged and paid for as it is, whatever it
		// cost; the reservation stays for the second.
		if lerr := s.record(logCtx, workspaceID, call, req, res, err, *t.settings.Provider, uuid.Nil); lerr != nil {
			return Result{}, errors.Join(fmt.Errorf("record ai call: %w", lerr), s.release(logCtx, workspaceID, t.reservation))
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Result{}, errors.Join(err, s.release(logCtx, workspaceID, t.reservation))
		case <-timer.C:
			res, err = attempt()
		}
	}
	if lerr := s.record(logCtx, workspaceID, call, req, res, err, *t.settings.Provider, t.reservation); lerr != nil {
		return Result{}, errors.Join(fmt.Errorf("record ai call: %w", lerr), s.release(logCtx, workspaceID, t.reservation))
	}
	return res, err
}

// release gives a reservation back without a call to log.
func (s *Service) release(ctx context.Context, workspaceID, id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).Release(ctx, store.ReleaseParams{WorkspaceID: workspaceID, ID: id})
	})
	if err != nil {
		return fmt.Errorf("release ai budget: %w", err)
	}
	return nil
}

// target loads the settings, checks them and the budget, and opens the key.
// With a budget it reserves the most req may cost (reserve), under a lock
// of the workspace: what calls in flight reserved counts as spent, so
// parallel calls cannot all pass a budget that has room for one.
func (s *Service) target(ctx context.Context, workspaceID uuid.UUID, needEnabled bool, req llm.Request) (target, error) {
	var (
		t      target
		sealed []byte
	)
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetSettings(ctx, workspaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotConfigured
		}
		if err != nil {
			return err
		}
		if (needEnabled && !row.Enabled) || row.Provider == nil || row.Model == nil {
			return ErrNotConfigured
		}
		t.settings = row
		if row.CredentialID != nil {
			cred, err := q.GetCredential(ctx, store.GetCredentialParams{WorkspaceID: workspaceID, ID: *row.CredentialID})
			if err != nil {
				return err
			}
			sealed = cred.Sealed
		} else if *row.Provider != llm.OpenAICompatible {
			return ErrNotConfigured
		}
		budget := row.MonthlyBudgetMicros
		if budget == nil {
			t.spent, err = q.SpentSince(ctx, store.SpentSinceParams{WorkspaceID: workspaceID, Since: monthStart(s.now())})
			return err
		}
		now := s.now()
		if err := q.LockBudget(ctx, workspaceID); err != nil {
			return err
		}
		if err := q.DropExpiredReservations(ctx, store.DropExpiredReservationsParams{WorkspaceID: workspaceID, Now: now}); err != nil {
			return err
		}
		if t.spent, err = q.SpentSince(ctx, store.SpentSinceParams{WorkspaceID: workspaceID, Since: monthStart(now)}); err != nil {
			return err
		}
		reserved, err := q.ReservedNow(ctx, workspaceID)
		if err != nil {
			return err
		}
		if t.spent+reserved >= *budget {
			t.spent += reserved
			return nil // refused below
		}
		if t.reservation, err = uuid.NewV7(); err != nil {
			return err
		}
		return q.Reserve(ctx, store.ReserveParams{
			ID: t.reservation, WorkspaceID: workspaceID, ReservedMicros: reserve(req, row),
			// Past the call's timeout and the log's: a stopped server's
			// reservation is gone by then.
			ExpiresAt: now.Add(2*s.opts.Timeout + time.Minute),
		})
	})
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return target{}, err
		}
		return target{}, fmt.Errorf("load ai settings: %w", err)
	}
	if b := t.settings.MonthlyBudgetMicros; b != nil && t.reservation == uuid.Nil {
		return target{}, ErrBudgetExceeded
	}
	if sealed != nil {
		if s.opts.Sealer == nil {
			return target{}, errors.Join(ErrNoEncryption, s.release(ctx, workspaceID, t.reservation))
		}
		key, err := s.opts.Sealer.Open(ctx, sealed, crypto.AAD{WorkspaceID: workspaceID, SecretID: *t.settings.CredentialID})
		if err != nil {
			return target{}, errors.Join(fmt.Errorf("open ai key: %w", err), s.release(ctx, workspaceID, t.reservation))
		}
		t.key = key
	}
	return t, nil
}

// reserve is the most a request may cost, in millionths of a US dollar,
// for two attempts: its text counted as one token a byte, which no
// tokenizer exceeds, and the answer at its token limit.
func reserve(req llm.Request, row store.AiSetting) int64 {
	in := len(req.System)
	for _, m := range req.Messages {
		in += len(m.Text)
	}
	return 2 * cost(llm.Usage{InputTokens: in, OutputTokens: req.MaxTokens}, row)
}

func (s *Service) provider(t target) (llm.Provider, error) {
	cfg := llm.Config{Provider: *t.settings.Provider, BaseURL: deref(t.settings.BaseUrl), Key: t.key}
	switch cfg.Provider {
	case llm.Anthropic:
		cfg.BaseURL = s.opts.AnthropicURL
	case llm.OpenAI:
		cfg.BaseURL = s.opts.OpenAIURL
	case llm.DeepSeek:
		cfg.BaseURL = s.opts.DeepSeekURL
	}
	return llm.New(cfg, s.opts.HTTP)
}

// retry says whether to try once more and after how long: only a rate
// limit or an outage, and only if the wait fits the call's deadline.
func (s *Service) retry(ctx context.Context, err error) (time.Duration, bool) {
	var e *llm.Error
	transient := errors.Is(err, llm.ErrRateLimited) || errors.Is(err, llm.ErrUnavailable)
	if !errors.As(err, &e) || !transient || ctx.Err() != nil {
		return 0, false
	}
	wait := e.RetryAfter
	if wait <= 0 {
		wait = s.retryWait
	}
	if wait > maxRetryWait {
		return 0, false // the provider asks for longer than a request can wait
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait+time.Second {
		return 0, false
	}
	return wait, true
}

// cost is the call's price in millionths of a US dollar, rounded up.
// Cached input is counted at the full input price: never less than the bill.
// Token counts come from the provider, an OpenAI-compatible server may send
// anything: they are taken as ai_calls stores them, 0 to 2^31-1.
func cost(u llm.Usage, row store.AiSetting) int64 {
	micro := float64(clamp32(u.InputTokens))*float64(row.InputPriceMicros) + float64(clamp32(u.OutputTokens))*float64(row.OutputPriceMicros)
	return int64(math.Ceil(micro / 1e6)) // at most 2·(2^31-1)·10^9/10^6, far below MaxInt64
}

// record writes the call to ai_calls with its masked texts and gives back
// the reservation (uuid.Nil for none) in the same transaction.
func (s *Service) record(
	ctx context.Context, workspaceID uuid.UUID, call Call, req llm.Request, res Result, callErr error, provider string, reservation uuid.UUID,
) error {
	latency := res.latency
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	status, code := outcome(callErr)
	type sentRequest struct {
		System   string        `json:"system"`
		Messages []llm.Message `json:"messages"`
		Schema   string        `json:"schema,omitempty"`
	}
	sent := sentRequest{System: req.System, Messages: req.Messages}
	if req.Schema != nil {
		sent.Schema = req.Schema.Name
	}
	reqJSON, err := json.Marshal(sent)
	if err != nil {
		return err
	}
	var respJSON []byte
	switch {
	case res.JSON != nil:
		respJSON, err = json.Marshal(map[string]json.RawMessage{"json": res.JSON})
	case res.Text != "":
		respJSON, err = json.Marshal(map[string]string{"text": res.Text})
	}
	if err != nil {
		return err
	}
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if reservation != uuid.Nil {
			if err := q.Release(ctx, store.ReleaseParams{WorkspaceID: workspaceID, ID: reservation}); err != nil {
				return err
			}
		}
		return q.InsertCall(ctx, store.InsertCallParams{
			ID: id, WorkspaceID: workspaceID, ConversationID: call.ConversationID, Purpose: call.Purpose,
			Provider: provider, Model: res.Model,
			InputTokens: clamp32(res.Usage.InputTokens), OutputTokens: clamp32(res.Usage.OutputTokens), CachedTokens: clamp32(res.Usage.CachedTokens),
			CostMicros: res.CostMicros, LatencyMs: clamp32(int(latency.Milliseconds())),
			Status: status, ErrorCode: code, RequestRedacted: reqJSON, ResponseRedacted: respJSON, CreatedAt: s.now(),
		})
	})
}

// outcome names a call's result for ai_calls.status and error_code.
func outcome(err error) (string, *string) {
	if err == nil {
		return "ok", nil
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", nil
	case errors.Is(err, llm.ErrTruncated):
		return "truncated", nil
	case errors.Is(err, llm.ErrRefused):
		return "refused", nil
	case errors.Is(err, llm.ErrBadOutput):
		return "bad_output", nil
	}
	kind := "error"
	for _, k := range []struct {
		err  error
		name string
	}{{llm.ErrAuth, "auth"}, {llm.ErrRateLimited, "rate_limited"}, {llm.ErrUnavailable, "unavailable"}, {llm.ErrRejected, "rejected"}} {
		if errors.Is(err, k.err) {
			kind = k.name
			break
		}
	}
	var e *llm.Error
	if errors.As(err, &e) && e.Code != "" {
		kind += ":" + e.Code
	}
	return "error", &kind
}

func clamp32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n)
}
