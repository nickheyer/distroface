package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

// Native ollama chat client, tool calls carry no ids so they are synthesized
type ollamaClient struct {
	base string
	cfg  Config
	http *http.Client
}

type olToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type olMessage struct {
	Role      string       `json:"role"`
	Content   string       `json:"content"`
	ToolCalls []olToolCall `json:"tool_calls,omitempty"`
	ToolName  string       `json:"tool_name,omitempty"`
}

type olRequest struct {
	Model    string         `json:"model"`
	Messages []olMessage    `json:"messages"`
	Tools    []oaTool       `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type olResponse struct {
	Model           string    `json:"model"`
	Message         olMessage `json:"message"`
	DoneReason      string    `json:"done_reason"`
	PromptEvalCount int64     `json:"prompt_eval_count"`
	EvalCount       int64     `json:"eval_count"`
}

func (c *ollamaClient) auth(req *http.Request) {
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
}

func (c *ollamaClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%w: a model is required", ErrBadRequest)
	}
	body := olRequest{Model: req.Model, Options: map[string]any{}}
	if req.System != "" {
		body.Messages = append(body.Messages, olMessage{Role: RoleSystem, Content: req.System})
	}
	for _, m := range req.Messages {
		om := olMessage{Role: m.Role, Content: m.Content}
		if m.Role == RoleTool {
			om.ToolName = m.ToolName
		}
		for _, tc := range m.ToolCalls {
			var call olToolCall
			call.Function.Name = tc.Name
			call.Function.Arguments = rawArguments(tc.Arguments)
			om.ToolCalls = append(om.ToolCalls, call)
		}
		body.Messages = append(body.Messages, om)
	}
	for _, t := range req.Tools {
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		body.Tools = append(body.Tools, oaTool{Type: "function", Function: oaToolDef{Name: t.Name, Description: t.Description, Parameters: params}})
	}
	if req.Temperature != nil {
		body.Options["temperature"] = *req.Temperature
	}
	// Minus one is ollama's own word for no cap
	body.Options["num_predict"] = -1
	if req.MaxTokens > 0 {
		body.Options["num_predict"] = req.MaxTokens
	}
	var out olResponse
	if err := postJSON(ctx, c.http, c.cfg, c.base+"/api/chat", c.auth, body, &out); err != nil {
		return nil, err
	}
	resp := &Response{Model: out.Model, Content: out.Message.Content, Usage: Usage{InputTokens: out.PromptEvalCount, OutputTokens: out.EvalCount}}
	for i, tc := range out.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: fmt.Sprintf("call_%d", i+1), Name: tc.Function.Name, Arguments: rawArguments(tc.Function.Arguments)})
	}
	switch {
	case len(resp.ToolCalls) > 0:
		resp.StopReason = StopToolCalls
	case out.DoneReason == "length":
		resp.StopReason = StopLength
	default:
		resp.StopReason = StopEnd
	}
	return resp, nil
}

func (c *ollamaClient) ListModels(ctx context.Context) ([]Model, error) {
	var out struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := getJSON(ctx, c.http, c.cfg, c.base+"/api/tags", c.auth, &out); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(out.Models))
	for _, m := range out.Models {
		id := m.Model
		if id == "" {
			id = m.Name
		}
		if id == "" {
			continue
		}
		models = append(models, Model{ID: id, Name: id})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
