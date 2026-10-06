package llm

import (
	"context"
	"encoding/json"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// anthropicVersion pins the Messages API version; Anthropic keeps old
// versions working, so an update is our choice, not a breakage.
const anthropicVersion = "2023-06-01"

// anthropic calls the Messages API (POST /v1/messages). A schema is asked
// for as a forced tool call: the tool's input is the object. That works on
// every Claude model with tool use.
type anthropic struct {
	base string
	key  crypto.Secret
	hc   HTTPClient
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature *float64           `json:"temperature,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	ToolChoice  *anthropicChoice   `json:"tool_choice,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type anthropicResponse struct {
	Model   string `json:"model"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func (a *anthropic) Name() string { return Anthropic }

func (a *anthropic) Chat(ctx context.Context, req Request) (Response, error) {
	body := anthropicRequest{
		Model: req.Model, MaxTokens: req.MaxTokens, System: req.System, Temperature: req.Temperature,
		Messages: make([]anthropicMessage, 0, len(req.Messages)),
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, anthropicMessage{Role: string(m.Role), Content: m.Text})
	}
	if req.Schema != nil {
		body.Tools = []anthropicTool{{Name: req.Schema.Name, Description: req.Schema.Description, InputSchema: req.Schema.JSON}}
		body.ToolChoice = &anthropicChoice{Type: "tool", Name: req.Schema.Name}
	}
	var out anthropicResponse
	headers := map[string]string{"x-api-key": a.key.Reveal(), "anthropic-version": anthropicVersion}
	if err := post(ctx, a.hc, a.base+"/v1/messages", headers, body, &out); err != nil {
		return Response{}, err
	}
	// Anthropic counts cached input apart from input_tokens.
	cached := out.Usage.CacheReadInputTokens
	resp := Response{
		Model: out.Model,
		Usage: Usage{
			InputTokens:  out.Usage.InputTokens + out.Usage.CacheCreationInputTokens + cached,
			OutputTokens: out.Usage.OutputTokens,
			CachedTokens: cached,
		},
	}
	switch out.StopReason {
	case "max_tokens":
		return resp, &Error{Kind: ErrTruncated}
	case "refusal":
		return resp, &Error{Kind: ErrRefused}
	}
	for _, c := range out.Content {
		switch {
		case c.Type == "text":
			resp.Text += c.Text
		case c.Type == "tool_use" && req.Schema != nil && c.Name == req.Schema.Name:
			if obj, ok := object(string(c.Input)); ok {
				resp.JSON = obj
			}
		}
	}
	if req.Schema != nil && resp.JSON == nil {
		return resp, &Error{Kind: ErrBadOutput}
	}
	return resp, nil
}
