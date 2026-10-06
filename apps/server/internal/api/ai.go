package api

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// GetAISettings implements GET /ai/settings.
func (s *server) GetAISettings(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.AIManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if s.ai == nil {
		notFound(w, r)
		return
	}
	got, err := s.ai.Settings(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.aiSettingsBody(got))
}

// SetAISettings implements PUT /ai/settings.
func (s *server) SetAISettings(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.AIManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if s.ai == nil {
		notFound(w, r)
		return
	}
	var req oapi.AISettingsUpdate
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	in := ai.SettingsUpdate{
		Enabled: req.Enabled, Model: deref(req.Model), BaseURL: deref(req.BaseUrl),
		Prices:              ai.Prices{InputMicros: req.Prices.InputMicros, OutputMicros: req.Prices.OutputMicros},
		MonthlyBudgetMicros: req.MonthlyBudgetMicros,
	}
	if req.Provider != nil {
		in.Provider = string(*req.Provider)
	}
	if req.ApiKey != nil {
		k := crypto.NewSecret(*req.ApiKey)
		in.Key = &k
	}
	got, err := s.ai.UpdateSettings(r.Context(), p.WorkspaceID, p.UserID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.aiSettingsBody(got))
}

// CheckAISettings implements POST /ai/settings/check.
func (s *server) CheckAISettings(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.AIManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if s.ai == nil {
		notFound(w, r)
		return
	}
	got, err := s.ai.Check(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.AICheck{Model: got.Model, LatencyMs: int(got.Latency.Milliseconds())})
}

func (s *server) aiSettingsBody(in ai.Settings) oapi.AISettings {
	out := oapi.AISettings{
		Enabled: in.Enabled, Model: nonEmpty(in.Model), BaseUrl: nonEmpty(in.BaseURL),
		Prices:              oapi.AIPrices{InputMicros: in.Prices.InputMicros, OutputMicros: in.Prices.OutputMicros},
		MonthlyBudgetMicros: in.MonthlyBudgetMicros, SpentMicros: in.SpentMicros, PeriodStart: in.PeriodStart,
		EncryptionAvailable: s.ai.EncryptionAvailable(),
		Models:              []oapi.AIModel{},
	}
	if in.Provider != "" {
		p := oapi.AIProvider(in.Provider)
		out.Provider = &p
	}
	if in.KeyHint != "" && in.KeySetAt != nil {
		out.Key = &struct {
			Hint  string    `json:"hint"`
			SetAt time.Time `json:"set_at"`
		}{Hint: in.KeyHint, SetAt: *in.KeySetAt}
	}
	for _, m := range ai.Catalog() {
		out.Models = append(out.Models, oapi.AIModel{
			Provider: oapi.AIProvider(m.Provider), Model: m.Model,
			Prices: oapi.AIPrices{InputMicros: m.Prices.InputMicros, OutputMicros: m.Prices.OutputMicros},
		})
	}
	return out
}

// aiProblems maps the ai module's errors; the provider's come after.
var aiProblems = []struct {
	err  error
	prob Problem
}{
	{ai.ErrNotConfigured, Problem{
		Status: http.StatusConflict, Code: "ai_not_configured", Title: "Conflict",
		Detail: "choose a provider and a model and save the API key first",
	}},
	{ai.ErrBudgetExceeded, Problem{
		Status: http.StatusConflict, Code: "ai_budget_exceeded", Title: "Conflict", Detail: "the monthly AI budget is spent",
	}},
	{ai.ErrKeyRequired, Problem{
		Status: http.StatusUnprocessableEntity, Code: "ai_key_required", Title: "Unprocessable Content",
		Detail: "the provider needs an API key to turn AI on",
	}},
	{ai.ErrNoEncryption, Problem{
		Status: http.StatusServiceUnavailable, Code: "encryption_unavailable", Title: "Service Unavailable",
		Detail: "TOLKOVO_MASTER_KEY is not set on this server",
	}},
	{llm.ErrAuth, Problem{
		Status: http.StatusUnprocessableEntity, Code: "ai_key_refused", Title: "Unprocessable Content",
		Detail: "the provider refused the API key",
	}},
	{llm.ErrRefused, Problem{
		Status: http.StatusBadGateway, Code: "ai_bad_answer", Title: "Bad Gateway", Detail: "the model declined to answer",
	}},
	{llm.ErrBadOutput, Problem{
		Status: http.StatusBadGateway, Code: "ai_bad_answer", Title: "Bad Gateway", Detail: "the model did not answer as asked",
	}},
}

// writeAIError answers the errors of the ai module and of model providers
// and reports whether err was one. Outages are logged by kind and code,
// which never hold the key or the texts.
func (s *server) writeAIError(w http.ResponseWriter, r *http.Request, err error) bool {
	for _, c := range aiProblems {
		if errors.Is(err, c.err) {
			writeProblem(w, r, c.prob)
			return true
		}
	}
	var e *llm.Error
	if !errors.As(err, &e) {
		return false
	}
	switch {
	case errors.Is(err, llm.ErrRejected):
		writeProblem(w, r, Problem{Status: http.StatusUnprocessableEntity, Code: "ai_request_rejected", Title: "Unprocessable Content", Detail: e.Code})
	case errors.Is(err, llm.ErrRateLimited):
		if e.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
		}
		writeProblem(w, r, Problem{
			Status: http.StatusServiceUnavailable, Code: "ai_rate_limited", Title: "Service Unavailable",
			Detail: "the provider asks to wait before the next request",
		})
	default:
		s.log.WarnContext(r.Context(), "ai provider failed",
			slog.String("request_id", middleware.GetReqID(r.Context())), slog.String("error", err.Error()))
		writeProblem(w, r, Problem{Status: http.StatusBadGateway, Code: "ai_provider_unavailable", Title: "Bad Gateway"})
	}
	return true
}
