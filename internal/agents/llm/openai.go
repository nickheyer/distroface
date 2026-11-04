package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Chat completions client, also serves azure, gemini, bedrock and every compatible server
type openAIClient struct {
	base  string
	cfg   Config
	http  *http.Client
	azure bool
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

type oaToolCall struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Function oaFunction `json:"function"`
}

type oaFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type oaTool struct {
	Type     string    `json:"type"`
	Function oaToolDef `json:"function"`
}

type oaToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaRequest struct {
	Model               string      `json:"model"`
	Messages            []oaMessage `json:"messages"`
	Tools               []oaTool    `json:"tools,omitempty"`
	ToolChoice          string      `json:"tool_choice,omitempty"`
	MaxTokens           int         `json:"max_tokens,omitempty"`
	MaxCompletionTokens int         `json:"max_completion_tokens,omitempty"`
	Temperature         *float64    `json:"temperature,omitempty"`
	Stream              bool        `json:"stream"`
	PromptCacheKey      string      `json:"prompt_cache_key,omitempty"`
}

type oaResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      oaMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// Only the first party api takes the key
func (c *openAIClient) routesCacheByKey() bool {
	u, err := url.Parse(c.base)
	return err == nil && !c.azure && strings.EqualFold(u.Hostname(), "api.openai.com")
}

func (c *openAIClient) auth(req *http.Request) {
	if c.cfg.APIKey == "" {
		return
	}
	if c.azure {
		req.Header.Set("api-key", c.cfg.APIKey)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
}

func (c *openAIClient) apiVersion() string {
	if v := strings.TrimSpace(c.cfg.APIVersion); v != "" {
		return v
	}
	return "2024-10-21"
}

func (c *openAIClient) chatURL(model string) string {
	if c.azure {
		return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s", c.base, url.PathEscape(model), url.QueryEscape(c.apiVersion()))
	}
	return c.base + "/chat/completions"
}

func (c *openAIClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%w: a model is required", ErrBadRequest)
	}
	body := oaRequest{Model: req.Model, Temperature: req.Temperature, MaxTokens: req.MaxTokens}
	if req.System != "" {
		s := req.System
		body.Messages = append(body.Messages, oaMessage{Role: RoleSystem, Content: &s})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, toOAMessage(m))
	}
	for _, t := range req.Tools {
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		body.Tools = append(body.Tools, oaTool{Type: "function", Function: oaToolDef{Name: t.Name, Description: t.Description, Parameters: params}})
	}
	if len(body.Tools) > 0 {
		body.ToolChoice = "auto"
	}
	if req.CacheKey != "" && c.routesCacheByKey() {
		body.PromptCacheKey = req.CacheKey
	}
	var out oaResponse
	err := postJSON(ctx, c.http, c.cfg, c.chatURL(req.Model), c.auth, body, &out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest && strings.Contains(apiErr.Message, "max_completion_tokens") && body.MaxTokens > 0 {
		// Newer openai models retired max_tokens
		body.MaxCompletionTokens, body.MaxTokens = body.MaxTokens, 0
		err = postJSON(ctx, c.http, c.cfg, c.chatURL(req.Model), c.auth, body, &out)
	}
	if err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, &APIError{Message: "provider returned no choices", class: ErrUnavailable}
	}
	ch := out.Choices[0]
	resp := &Response{Model: out.Model, Usage: Usage{
		InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens, CacheReadTokens: out.Usage.PromptTokensDetails.CachedTokens,
	}}
	if ch.Message.Content != nil {
		resp.Content = *ch.Message.Content
	}
	for i, tc := range ch.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: id, Name: tc.Function.Name, Arguments: rawArguments(tc.Function.Arguments)})
	}
	switch {
	case len(resp.ToolCalls) > 0:
		resp.StopReason = StopToolCalls
	case ch.FinishReason == "length":
		resp.StopReason = StopLength
	case ch.FinishReason == "stop" || ch.FinishReason == "":
		resp.StopReason = StopEnd
	default:
		resp.StopReason = StopOther
	}
	return resp, nil
}

func toOAMessage(m Message) oaMessage {
	out := oaMessage{Role: m.Role}
	switch m.Role {
	case RoleTool:
		content := m.Content
		out.Content = &content
		out.ToolCallID = m.ToolCallID
		out.Name = m.ToolName
	case RoleAssistant:
		if m.Content != "" || len(m.ToolCalls) == 0 {
			content := m.Content
			out.Content = &content
		}
		for _, tc := range m.ToolCalls {
			args, _ := json.Marshal(string(rawArguments(tc.Arguments)))
			out.ToolCalls = append(out.ToolCalls, oaToolCall{ID: tc.ID, Type: "function", Function: oaFunction{Name: tc.Name, Arguments: args}})
		}
	default:
		content := m.Content
		out.Content = &content
	}
	return out
}

func (c *openAIClient) ListModels(ctx context.Context) ([]Model, error) {
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			Model         string `json:"model"`
			ContextWindow int64  `json:"context_window"`
			ContextLength int64  `json:"context_length"`
			MaxModelLen   int64  `json:"max_model_len"`
		} `json:"data"`
	}
	target := c.base + "/models"
	if c.azure {
		target = fmt.Sprintf("%s/openai/deployments?api-version=%s", c.base, url.QueryEscape(c.apiVersion()))
	}
	if err := getJSON(ctx, c.http, c.cfg, target, c.auth, &out); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(out.Data))
	for _, d := range out.Data {
		id := d.ID
		if id == "" {
			continue
		}
		name := id
		if c.azure && d.Model != "" {
			name = fmt.Sprintf("%s (%s)", id, d.Model)
		}
		ctxLen := d.ContextWindow
		if ctxLen == 0 {
			ctxLen = d.ContextLength
		}
		if ctxLen == 0 {
			ctxLen = d.MaxModelLen
		}
		models = append(models, Model{ID: id, Name: name, ContextWindow: ctxLen})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
