package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func anthropicTestClient(t *testing.T, url string) Client {
	t.Helper()
	c, err := New(Config{Kind: KindAnthropic, BaseURL: url, APIKey: "k", ToolsSupported: true, AllowPrivate: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAnthropicUnlimitedLearnsTheModelCeiling(t *testing.T) {
	var mu sync.Mutex
	var sent []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sent = append(sent, body.MaxTokens)
		mu.Unlock()
		if body.MaxTokens > 64000 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: %d > 64000, which is the maximum allowed number of output tokens for test-model"}}`, body.MaxTokens)
			return
		}
		fmt.Fprint(w, `{"model":"test-model","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
	}))
	defer server.Close()
	c := anthropicTestClient(t, server.URL)
	req := &Request{Model: "test-model", Messages: []Message{{Role: RoleUser, Content: "hello"}}}
	resp, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi" || resp.StopReason != StopEnd {
		t.Fatalf("unexpected response %+v", resp)
	}
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []int{unboundedMaxTokens, 64000, 64000}
	if fmt.Sprint(sent) != fmt.Sprint(want) {
		t.Fatalf("max_tokens sent %v, want %v", sent, want)
	}
}

func TestAnthropicExplicitCapIsSentAsIs(t *testing.T) {
	var got atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int64 `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		got.Store(body.MaxTokens)
		fmt.Fprint(w, `{"model":"test-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer server.Close()
	c := anthropicTestClient(t, server.URL)
	if _, err := c.Complete(context.Background(), &Request{Model: "test-model", MaxTokens: 777, Messages: []Message{{Role: RoleUser, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if got.Load() != 777 {
		t.Fatalf("sent %d, want the configured 777", got.Load())
	}
}

func TestAnthropicSwitchesToTheEventStreamWhenAsked(t *testing.T) {
	var streamed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(raw, &body)
		if !body.Stream {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Streaming is strongly recommended for operations that may take longer than 10 minutes."}}`)
			return
		}
		streamed.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`event: message_start`, `data: {"type":"message_start","message":{"model":"test-model","content":[],"usage":{"input_tokens":12,"output_tokens":1}}}`, ``,
			`event: ping`, `data: {"type":"ping"}`, ``,
			`event: content_block_start`, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, ``,
			`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Reading "}}`, ``,
			`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"now."}}`, ``,
			`event: content_block_stop`, `data: {"type":"content_block_stop","index":0}`, ``,
			`event: content_block_start`, `data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`, ``,
			`event: content_block_delta`, `data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\": \"src/"}}`, ``,
			`event: content_block_delta`, `data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"tui.rs\"}"}}`, ``,
			`event: content_block_stop`, `data: {"type":"content_block_stop","index":1}`, ``,
			`event: message_delta`, `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":40}}`, ``,
			`event: message_stop`, `data: {"type":"message_stop"}`, ``,
		}
		fmt.Fprint(w, strings.Join(events, "\n")+"\n")
	}))
	defer server.Close()
	c := anthropicTestClient(t, server.URL)
	req := &Request{Model: "test-model", Messages: []Message{{Role: RoleUser, Content: "go"}}}
	resp, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Reading now." {
		t.Fatalf("content %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" || resp.ToolCalls[0].ID != "toolu_1" {
		t.Fatalf("tool calls %+v", resp.ToolCalls)
	}
	var args map[string]any
	if err := json.Unmarshal(resp.ToolCalls[0].Arguments, &args); err != nil || args["path"] != "src/tui.rs" {
		t.Fatalf("arguments %s (%v)", resp.ToolCalls[0].Arguments, err)
	}
	if resp.StopReason != StopToolCalls || resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 40 {
		t.Fatalf("stop %q usage %+v", resp.StopReason, resp.Usage)
	}
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if streamed.Load() != 2 {
		t.Fatalf("the second call must stream straight away, streamed %d times", streamed.Load())
	}
}

func TestReadEventStreamReportsMidStreamErrors(t *testing.T) {
	body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"m\"}}\n\nevent: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	_, err := readEventStream(strings.NewReader(body))
	if err == nil || !strings.Contains(err.Error(), "Overloaded") {
		t.Fatalf("expected the provider error, got %v", err)
	}
}
