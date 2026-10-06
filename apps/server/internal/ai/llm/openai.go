package llm

import (
	"context"
	"encoding/json"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// openai calls Chat Completions (POST {base}/chat/completions), which
// OpenAI keeps and every OpenAI-compatible server implements. A schema is
// asked for with response_format json_schema.
//
// compat marks an OpenAI-compatible server: it gets max_tokens, which they
// all know, instead of max_completion_tokens, which OpenAI's reasoning
// models require; and since some of them ignore response_format, the
// schema is also put into the system prompt.
//
// jsonObject asks for response_format json_object instead of json_schema,
// for DeepSeek, which knows only that; the schema is then in the system
// prompt alone, which also carries the word "JSON" DeepSeek requires.
type openai struct {
	name       string
	base       string
	key        crypto.Secret
	hc         HTTPClient
	compat     bool
	jsonObject bool
}

type openaiRequest struct {
	Model               string          `json:"model"`
	Messages            []openaiMessage `json:"messages"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	ResponseFormat      *openaiFormat   `json:"response_format,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiFormat struct {
	Type       string            `json:"type"`
	JSONSchema *openaiJSONSchema `json:"json_schema,omitempty"`
}

type openaiJSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
	Strict      bool            `json:"strict"`
}

type openaiResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
			Refusal *string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		// PromptCacheHitTokens is DeepSeek's count of cached tokens.
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	} `json:"usage"`
}

func (o *openai) Name() string { return o.name }

func (o *openai) Chat(ctx context.Context, req Request) (Response, error) {
	system := req.System
	if req.Schema != nil && o.compat {
		system += "\n\nAnswer with one JSON object, and nothing else, that matches this JSON Schema:\n" + string(req.Schema.JSON)
	}
	body := openaiRequest{Model: req.Model, Temperature: req.Temperature, Messages: make([]openaiMessage, 0, len(req.Messages)+1)}
	if o.compat {
		body.MaxTokens = req.MaxTokens
	} else {
		body.MaxCompletionTokens = req.MaxTokens
	}
	if system != "" {
		body.Messages = append(body.Messages, openaiMessage{Role: "system", Content: system})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, openaiMessage{Role: string(m.Role), Content: m.Text})
	}
	switch {
	case req.Schema != nil && o.jsonObject:
		body.ResponseFormat = &openaiFormat{Type: "json_object"}
	case req.Schema != nil:
		body.ResponseFormat = &openaiFormat{Type: "json_schema", JSONSchema: &openaiJSONSchema{
			Name: req.Schema.Name, Description: req.Schema.Description, Schema: req.Schema.JSON, Strict: true,
		}}
	}
	headers := map[string]string{}
	if !o.key.Empty() {
		headers["Authorization"] = "Bearer " + o.key.Reveal()
	}
	var out openaiResponse
	if err := post(ctx, o.hc, o.base+"/chat/completions", headers, body, &out); err != nil {
		return Response{}, err
	}
	resp := Response{
		Model: out.Model,
		Usage: Usage{
			InputTokens:  out.Usage.PromptTokens,
			OutputTokens: out.Usage.CompletionTokens,
			CachedTokens: max(out.Usage.PromptTokensDetails.CachedTokens, out.Usage.PromptCacheHitTokens),
		},
	}
	if len(out.Choices) == 0 {
		return resp, &Error{Kind: ErrBadOutput}
	}
	choice := out.Choices[0]
	switch {
	case choice.FinishReason == "length":
		return resp, &Error{Kind: ErrTruncated}
	case choice.FinishReason == "insufficient_system_resource":
		// DeepSeek stops a started answer when it runs out of capacity.
		return resp, &Error{Kind: ErrUnavailable, Code: choice.FinishReason}
	case choice.FinishReason == "content_filter", choice.Message.Refusal != nil && *choice.Message.Refusal != "":
		return resp, &Error{Kind: ErrRefused}
	}
	if choice.Message.Content != nil {
		resp.Text = *choice.Message.Content
	}
	if req.Schema != nil {
		obj, ok := object(resp.Text)
		if !ok {
			return resp, &Error{Kind: ErrBadOutput}
		}
		resp.JSON, resp.Text = obj, ""
	}
	return resp, nil
}
