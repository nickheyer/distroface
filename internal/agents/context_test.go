package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/agents/llm"
)

// Transcript of n read calls each answered with the body
func toolTurns(n int, body string) []llm.Message {
	msgs := []llm.Message{{Role: llm.RoleSystem, Content: "sys"}, {Role: llm.RoleUser, Content: "task"}}
	for i := range n {
		id := fmt.Sprintf("c%d", i)
		args := json.RawMessage(fmt.Sprintf(`{"path":"f%d.go"}`, i))
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_file", Arguments: args}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, ToolName: "read_file", Content: body},
		)
	}
	return msgs
}

func TestPruneStubsOldResultsInBatches(t *testing.T) {
	rc := &runContext{}
	if rc.prune(toolTurns(pruneAfter+3, "short"), false); len(rc.pruned) != 0 {
		t.Fatalf("small results were pruned: %v", rc.pruned)
	}
	body := strings.Repeat("x\n", 6000)
	big := toolTurns(pruneAfter+3, body)
	out := rc.prune(big, false)
	if len(rc.pruned) != 3 {
		t.Fatalf("pruned %d results, want the 3 older than %d turns", len(rc.pruned), pruneAfter)
	}
	for _, m := range out {
		if m.Role != llm.RoleTool {
			continue
		}
		if stub, ok := rc.pruned[m.ToolCallID]; ok {
			if m.Content != stub || !strings.Contains(stub, "read_file {\"path\":") || !strings.Contains(stub, "dropped from context") {
				t.Fatalf("stub %q", m.Content)
			}
		} else if m.Content != body {
			t.Fatalf("a recent result was changed: %q", m.Content[:20])
		}
	}
	again := rc.prune(big, false)
	for i := range out {
		if out[i].Content != again[i].Content {
			t.Fatalf("message %d differs between two requests", i)
		}
	}
	// A lone eligible result waits for a batch
	more := toolTurns(pruneAfter+4, body)
	if rc.prune(more, false); len(rc.pruned) != 3 {
		t.Fatalf("pruned %d, a lone result should wait for a batch", len(rc.pruned))
	}
	if rc.prune(more, true); len(rc.pruned) != 4 {
		t.Fatalf("pruned %d, near the window every eligible result goes", len(rc.pruned))
	}
	if len(big[3].Content) != len(body) {
		t.Fatal("pruning must not touch the transcript itself")
	}
}

func TestBindReasoningFollowsThePrefix(t *testing.T) {
	reasoning := json.RawMessage(`[{"type":"thinking","thinking":"","signature":"s"}]`)
	turn := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{}`)}}, Reasoning: reasoning}
	result := llm.Message{Role: llm.RoleTool, ToolCallID: "c1", ToolName: "read_file", Content: "body"}
	ask := func(system, task string) *llm.Request {
		return &llm.Request{Model: "m", System: system, Messages: []llm.Message{{Role: llm.RoleUser, Content: task}, turn, result}}
	}
	rc := &runContext{}
	rc.bindReasoning(&llm.Request{Model: "m", System: "sys", Messages: []llm.Message{{Role: llm.RoleUser, Content: "task"}}})
	rc.bound = rc.sent
	same := ask("sys", "task")
	if rc.bindReasoning(same); same.Messages[1].Reasoning == nil {
		t.Fatal("reasoning dropped though nothing before it changed")
	}
	moved := ask("sys2", "task")
	if rc.bindReasoning(moved); moved.Messages[1].Reasoning != nil {
		t.Fatal("reasoning resent under a different system prompt")
	}
	stubbed := ask("sys", "task rewritten")
	if rc.bindReasoning(stubbed); stubbed.Messages[1].Reasoning != nil {
		t.Fatal("reasoning resent after an earlier message changed")
	}
	fresh := &runContext{}
	first := ask("sys", "task")
	if fresh.bindReasoning(first); first.Messages[1].Reasoning != nil {
		t.Fatal("a new run replayed reasoning it did not produce")
	}
	// Only the newest turn ever carries reasoning
	older := ask("sys", "task")
	older.Messages = append(older.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c2", Name: "read_file", Arguments: json.RawMessage(`{}`)}}, Reasoning: reasoning},
		llm.Message{Role: llm.RoleTool, ToolCallID: "c2", ToolName: "read_file", Content: "body"},
	)
	rc.bound = binding(older, 3)
	rc.bindReasoning(older)
	if older.Messages[1].Reasoning != nil || older.Messages[3].Reasoning == nil {
		t.Fatal("only the newest turn should keep its reasoning")
	}
}

func TestDedupPointsAtTheEarlierIdenticalResult(t *testing.T) {
	rc := &runContext{}
	spec := toolRegistry["read_file"]
	body := strings.Repeat("line\n", 200)
	first := llm.ToolCall{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"path": "a.go"}`)}
	if got := rc.dedup(first, spec, body); got != body {
		t.Fatalf("first call changed: %q", got)
	}
	rc.results = append(rc.results, toolRecord{id: "c1", name: "read_file", args: compactJSON(first.Arguments), text: body, ok: true})
	second := llm.ToolCall{ID: "c2", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}
	got := rc.dedup(second, spec, body)
	if !strings.Contains(got, "c1") || !strings.Contains(got, "unchanged") {
		t.Fatalf("repeat not pointed at the earlier result: %q", got)
	}
	if got := rc.dedup(second, spec, body+"changed\n"); got != body+"changed\n" {
		t.Fatal("a different body must go out whole")
	}
	if got := rc.dedup(second, toolRegistry["write_file"], body); got != body {
		t.Fatal("writes are never deduplicated")
	}
	if got := rc.dedup(llm.ToolCall{ID: "c3", Name: "search_code", Arguments: json.RawMessage(`{}`)}, toolRegistry["search_code"], "no matches"); got != "no matches" {
		t.Fatal("short results are not worth a pointer")
	}
	rc.pruned = map[string]string{"c1": "stub"}
	if got := rc.dedup(second, spec, body); got != body {
		t.Fatal("a pruned result cannot be pointed at")
	}
}

func TestCapResultKeepsTheStartAndTheEnd(t *testing.T) {
	if got := capResult("ok"); got != "ok" {
		t.Fatalf("short result changed: %q", got)
	}
	var b strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&b, "line %05d\n", i)
	}
	text := b.String()
	out := capResult(text)
	if len(out) > maxResultBytes+200 {
		t.Fatalf("capped result is %d bytes", len(out))
	}
	if !strings.HasPrefix(out, "line 00000\n") || !strings.HasSuffix(out, "line 04999\n") {
		t.Fatalf("start or end lost:\n%s", out[:40])
	}
	var omitted, total int
	if _, err := fmt.Sscanf(out[strings.Index(out, "["):], "[%d of %d bytes omitted", &omitted, &total); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if total != len(text) || omitted+len(out)-len(fmt.Sprintf("[%d of %d bytes omitted from the middle, narrow the call to see them]\n", omitted, total)) != total {
		t.Fatalf("marker counts %d of %d do not add up to %d", omitted, total, len(out))
	}
}
