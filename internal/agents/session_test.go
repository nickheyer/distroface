package agents

import (
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/agents/llm"
)

func TestCompactTrimsOldToolResults(t *testing.T) {
	big := strings.Repeat("x", 5000)
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "task"},
	}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c", Name: "read_file"}}})
		msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: "c", Content: big})
	}
	out := compact(msgs, 25000)
	if estimateChars(out) > 25000 {
		t.Fatalf("still %d chars", estimateChars(out))
	}
	if out[0].Role != llm.RoleSystem || out[1].Role != llm.RoleUser {
		t.Fatal("system and first user turn must survive")
	}
	last := out[len(out)-1]
	if last.Role != llm.RoleTool || len(last.Content) != 5000 {
		t.Fatal("the newest tool result must stay intact")
	}
}

func TestEffectiveLimits(t *testing.T) {
	def := &Definition{MaxTurns: 10, MaxTokens: 500}
	if got := effectiveTurns(def, fakeLimits{turns: 40}); got != 10 {
		t.Fatalf("turns %d", got)
	}
	if got := effectiveTurns(&Definition{}, fakeLimits{turns: 0}); got != 0 {
		t.Fatalf("unlimited turns %d", got)
	}
	if got := effectiveTurns(&Definition{MaxTurns: 100}, fakeLimits{turns: 40}); got != 40 {
		t.Fatalf("cap %d", got)
	}
	if got := effectiveTokens(def, fakeLimits{tokens: 1000}); got != 500 {
		t.Fatalf("tokens %d", got)
	}
	if got := effectiveTokens(&Definition{MaxTokens: 5000}, fakeLimits{tokens: 1000}); got != 1000 {
		t.Fatalf("token cap %d", got)
	}
}

type fakeLimits struct {
	turns  int32
	tokens int64
}

func (f fakeLimits) GetMaxTurns() int32              { return f.turns }
func (f fakeLimits) GetTokenBudgetPerSession() int64 { return f.tokens }

func TestNeedsApproval(t *testing.T) {
	commit := toolRegistry["commit"]
	comment := toolRegistry["post_comment"]
	read := toolRegistry["read_file"]
	if needsApproval(&Definition{Approval: ApprovalNone}, commit) {
		t.Fatal("none never pauses")
	}
	if !needsApproval(&Definition{Approval: ApprovalCommits}, commit) || needsApproval(&Definition{Approval: ApprovalCommits}, comment) {
		t.Fatal("commits policy gates commits only")
	}
	if !needsApproval(&Definition{Approval: ApprovalAllWrites}, comment) || needsApproval(&Definition{Approval: ApprovalAllWrites}, read) {
		t.Fatal("all writes gates writes but not reads")
	}
}

func TestApprovalTitle(t *testing.T) {
	if got := approvalTitle("commit", map[string]any{"message": "Fix bug\n\nmore"}); got != "Commit: Fix bug" {
		t.Fatalf("got %q", got)
	}
	if got := approvalTitle("run_command", map[string]any{"command": "go test ./..."}); got != "Run command: go test ./..." {
		t.Fatalf("got %q", got)
	}
}
