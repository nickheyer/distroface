package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Tool protocol for models without native function calling, calls ride in fenced blocks
type promptedTools struct {
	inner Client
}

var toolBlockPattern = regexp.MustCompile("(?s)```(?:tool|tool_call|json)[^\n]*\n(.*?)```")

const promptedInstructions = `

# Tool protocol
You can call tools. To call one, reply with a fenced block exactly like this and nothing else after it:

` + "```tool" + `
{"name": "<tool name>", "arguments": {<json arguments>}}
` + "```" + `

Use one block per call. You may emit several blocks to call several tools at once. Wait for the results before answering. When you are finished, reply with plain text and no tool block. Available tools:
`

func (p *promptedTools) ListModels(ctx context.Context) ([]Model, error) {
	return p.inner.ListModels(ctx)
}

func (p *promptedTools) Complete(ctx context.Context, req *Request) (*Response, error) {
	if len(req.Tools) == 0 {
		return p.inner.Complete(ctx, req)
	}
	flat := &Request{Model: req.Model, MaxTokens: req.MaxTokens, Temperature: req.Temperature, System: req.System + describeTools(req.Tools)}
	for _, m := range req.Messages {
		flat.Messages = append(flat.Messages, flatten(m))
	}
	resp, err := p.inner.Complete(ctx, flat)
	if err != nil {
		return nil, err
	}
	calls, text := parseToolBlocks(resp.Content)
	resp.Content = text
	for i := range calls {
		calls[i].ID = fmt.Sprintf("call_%d", i+1)
	}
	resp.ToolCalls = calls
	if len(calls) > 0 {
		resp.StopReason = StopToolCalls
	}
	return resp, nil
}

// Tool definitions as the protocol section of a system prompt
func describeTools(tools []Tool) string {
	var b strings.Builder
	b.WriteString(promptedInstructions)
	for _, t := range tools {
		fmt.Fprintf(&b, "\n## %s\n%s\n", t.Name, t.Description)
		if len(t.Parameters) > 0 {
			fmt.Fprintf(&b, "Arguments schema: %s\n", string(t.Parameters))
		}
	}
	return b.String()
}

// Assistant calls become fenced blocks, tool results become user text
func flatten(m Message) Message {
	switch m.Role {
	case RoleAssistant:
		if len(m.ToolCalls) == 0 {
			return m
		}
		var b strings.Builder
		if m.Content != "" {
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		}
		for _, tc := range m.ToolCalls {
			body, _ := json.Marshal(map[string]any{"name": tc.Name, "arguments": rawArguments(tc.Arguments)})
			fmt.Fprintf(&b, "```tool\n%s\n```\n", body)
		}
		return Message{Role: RoleAssistant, Content: strings.TrimSpace(b.String())}
	case RoleTool:
		name := m.ToolName
		if name == "" {
			name = m.ToolCallID
		}
		return Message{Role: RoleUser, Content: fmt.Sprintf("Result of %s:\n%s", name, m.Content)}
	}
	return m
}

// Pulls tool blocks out of a reply, returning the remaining prose
func parseToolBlocks(text string) ([]ToolCall, string) {
	var calls []ToolCall
	rest := toolBlockPattern.ReplaceAllStringFunc(text, func(block string) string {
		sub := toolBlockPattern.FindStringSubmatch(block)
		if len(sub) < 2 {
			return block
		}
		call, ok := parseToolJSON(sub[1])
		if !ok {
			return block
		}
		calls = append(calls, call)
		return ""
	})
	if len(calls) == 0 {
		// Bare json objects naming a tool count too
		if call, ok := parseToolJSON(text); ok {
			return []ToolCall{call}, ""
		}
	}
	return calls, strings.TrimSpace(rest)
}

func parseToolJSON(raw string) (ToolCall, bool) {
	var obj struct {
		Name      string          `json:"name"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		Input     json.RawMessage `json:"input"`
		Params    json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &obj); err != nil {
		return ToolCall{}, false
	}
	name := obj.Name
	if name == "" {
		name = obj.Tool
	}
	if name == "" {
		return ToolCall{}, false
	}
	args := obj.Arguments
	if len(args) == 0 {
		args = obj.Input
	}
	if len(args) == 0 {
		args = obj.Params
	}
	return ToolCall{Name: name, Arguments: rawArguments(args)}, true
}
