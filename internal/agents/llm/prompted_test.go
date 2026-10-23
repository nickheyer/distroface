package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeClient struct {
	last  *Request
	reply string
}

func (f *fakeClient) Complete(_ context.Context, req *Request) (*Response, error) {
	f.last = req
	return &Response{Content: f.reply, StopReason: StopEnd}, nil
}

func (f *fakeClient) ListModels(context.Context) ([]Model, error) { return nil, nil }

func TestParseToolBlocks(t *testing.T) {
	text := "Let me look.\n```tool\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"a.go\"}}\n```\n```json\n{\"tool\": \"list_files\", \"input\": {}}\n```"
	calls, rest := parseToolBlocks(text)
	if len(calls) != 2 {
		t.Fatalf("calls %d", len(calls))
	}
	if calls[0].Name != "read_file" || string(calls[0].Arguments) != `{"path": "a.go"}` {
		t.Fatalf("first call %+v", calls[0])
	}
	if calls[1].Name != "list_files" || string(calls[1].Arguments) != "{}" {
		t.Fatalf("second call %+v", calls[1])
	}
	if rest != "Let me look." {
		t.Fatalf("rest %q", rest)
	}
	if calls, rest := parseToolBlocks("done, no tools"); len(calls) != 0 || rest != "done, no tools" {
		t.Fatal("plain text must pass through")
	}
}

func TestPromptedToolsFlattensConversation(t *testing.T) {
	fake := &fakeClient{reply: "```tool\n{\"name\":\"ping\",\"arguments\":{\"v\":1}}\n```"}
	client := &promptedTools{inner: fake}
	req := &Request{
		Model:  "m",
		System: "sys",
		Tools:  []Tool{{Name: "ping", Description: "probe", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Messages: []Message{
			{Role: RoleUser, Content: "go"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "ping", Arguments: json.RawMessage(`{"v":0}`)}}},
			{Role: RoleTool, ToolCallID: "c1", ToolName: "ping", Content: "pong"},
		},
	}
	resp, err := client.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "ping" || resp.StopReason != StopToolCalls {
		t.Fatalf("response %+v", resp)
	}
	if !strings.Contains(fake.last.System, "# Tool protocol") || !strings.Contains(fake.last.System, "## ping") {
		t.Fatal("tool protocol missing from system prompt")
	}
	if len(fake.last.Tools) != 0 {
		t.Fatal("native tools must not reach the inner client")
	}
	if fake.last.Messages[1].Role != RoleAssistant || !strings.Contains(fake.last.Messages[1].Content, "```tool") {
		t.Fatalf("assistant call not flattened: %+v", fake.last.Messages[1])
	}
	if fake.last.Messages[2].Role != RoleUser || !strings.Contains(fake.last.Messages[2].Content, "pong") {
		t.Fatalf("tool result not flattened: %+v", fake.last.Messages[2])
	}
}

func TestRawArguments(t *testing.T) {
	if string(rawArguments(json.RawMessage(`"{\"a\":1}"`))) != `{"a":1}` {
		t.Fatal("quoted json must be unwrapped")
	}
	if string(rawArguments(nil)) != "{}" {
		t.Fatal("empty becomes an object")
	}
	if string(rawArguments(json.RawMessage(`{"b":2}`))) != `{"b":2}` {
		t.Fatal("objects pass through")
	}
}

func TestAPIErrorClassification(t *testing.T) {
	if e := newAPIError(400, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`); e.class != ErrContextLength {
		t.Fatalf("context length not detected: %v", e.class)
	}
	if e := newAPIError(429, "slow down"); e.class != ErrRateLimited {
		t.Fatal("rate limit")
	}
	if e := newAPIError(401, "nope"); e.class != ErrUnauthorized {
		t.Fatal("auth")
	}
	if e := newAPIError(503, "down"); e.class != ErrUnavailable {
		t.Fatal("unavailable")
	}
}

func TestCheckIP(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
		ok      bool
	}{
		{"127.0.0.1", false, false},
		{"127.0.0.1", true, true},
		{"10.1.2.3", false, false},
		{"10.1.2.3", true, true},
		{"169.254.169.254", true, true},
		{"169.254.169.254", false, false},
		{"8.8.8.8", false, true},
	}
	for _, c := range cases {
		err := checkIP(mustIP(c.ip), c.private)
		if (err == nil) != c.ok {
			t.Errorf("%s private=%v: err=%v", c.ip, c.private, err)
		}
	}
}
