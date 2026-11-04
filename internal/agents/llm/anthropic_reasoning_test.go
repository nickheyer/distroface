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
	"testing"
)

func TestAnthropicReasoningRoundTripsAheadOfTheTurn(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		fmt.Fprint(w, `{"model":"test-model","content":[{"type":"thinking","thinking":"","signature":"sig-1"},{"type":"text","text":"Reading."},{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"a.go"}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":1}}`)
	}))
	defer server.Close()
	c := anthropicTestClient(t, server.URL)
	req := &Request{Model: "test-model", Messages: []Message{{Role: RoleUser, Content: "go"}}}
	resp, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Reasoning) == 0 || !strings.Contains(string(resp.Reasoning), `"signature":"sig-1"`) {
		t.Fatalf("reasoning %s", resp.Reasoning)
	}
	req.Messages = append(req.Messages,
		Message{Role: RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls, Reasoning: resp.Reasoning},
		Message{Role: RoleTool, ToolCallID: "toolu_1", Content: "package a"},
	)
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var body struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(bodies[1]), &body); err != nil {
		t.Fatal(err)
	}
	turn := body.Messages[1].Content
	if turn[0]["type"] != "thinking" || turn[0]["signature"] != "sig-1" {
		t.Fatalf("reasoning should lead the turn, got %v", turn)
	}
	if v, ok := turn[0]["thinking"]; !ok || v != "" {
		t.Fatalf("an empty thinking field must be sent, got %v", turn[0])
	}
	if _, marked := turn[0]["cache_control"]; marked {
		t.Fatal("reasoning blocks take no cache marker")
	}
	if turn[1]["type"] != "text" || turn[2]["type"] != "tool_use" {
		t.Fatalf("turn order %v", turn)
	}
}

func TestAnthropicStreamAssemblesThinkingDeltas(t *testing.T) {
	events := []string{
		`data: {"type":"message_start","message":{"model":"m","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, ``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`, ``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"step one"}}`, ``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc"}}`, ``,
		`data: {"type":"content_block_stop","index":0}`, ``,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`, ``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"done"}}`, ``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`, ``,
	}
	out, err := readEventStream(strings.NewReader(strings.Join(events, "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Content) != 2 || out.Content[0].Type != "thinking" || out.Content[0].Thinking != "step one" || out.Content[0].Signature != "abc" {
		t.Fatalf("blocks %+v", out.Content)
	}
}

func TestAnthropicDropsTheTemperatureOnceRefused(t *testing.T) {
	var mu sync.Mutex
	var sent []bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Temperature *float64 `json:"temperature"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sent = append(sent, body.Temperature != nil)
		mu.Unlock()
		if body.Temperature != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"temperature: this model does not support temperature"}}`)
			return
		}
		fmt.Fprint(w, `{"model":"test-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{}}`)
	}))
	defer server.Close()
	c := anthropicTestClient(t, server.URL)
	temp := 0.2
	req := &Request{Model: "test-model", Temperature: &temp, Messages: []Message{{Role: RoleUser, Content: "x"}}}
	for range 2 {
		if _, err := c.Complete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(sent) != fmt.Sprint([]bool{true, false, false}) {
		t.Fatalf("temperature sent %v, want it once then never", sent)
	}
}

func TestAnthropicListModelsReadsTheContextWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"test-model","display_name":"Test","max_input_tokens":200000},{"id":"old-model"}]}`)
	}))
	defer server.Close()
	c := anthropicTestClient(t, server.URL)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[1].ID != "test-model" || models[1].ContextWindow != 200000 || models[0].ContextWindow != 0 {
		t.Fatalf("models %+v", models)
	}
}
