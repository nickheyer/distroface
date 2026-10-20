package agents

import (
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/agents/llm"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func TestCompactTrimsOldToolResults(t *testing.T) {
	big := strings.Repeat("x", 5000)
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "task"},
	}
	for range 8 {
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

func TestTallyUsageCountsTranscriptOnce(t *testing.T) {
	sess := &db.AgentSession{}
	// Prompts resend the whole transcript, so turn two is turn one plus its answer and a tool result
	for _, u := range []llm.Usage{{InputTokens: 1000, OutputTokens: 50}, {InputTokens: 1250, OutputTokens: 50}, {InputTokens: 1500, OutputTokens: 100}} {
		tallyUsage(sess, u)
	}
	if sess.InputTokens != 1400 || sess.OutputTokens != 200 {
		t.Fatalf("transcript in=%d out=%d", sess.InputTokens, sess.OutputTokens)
	}
	if sess.PromptTokens != 3750 || sess.ContextTokens != 1600 {
		t.Fatalf("billed prompt=%d context=%d", sess.PromptTokens, sess.ContextTokens)
	}
	// Compaction shrinks the prompt, nothing new was read and the count never drops
	tallyUsage(sess, llm.Usage{InputTokens: 900, OutputTokens: 10})
	if sess.InputTokens != 1400 || sess.ContextTokens != 910 || sess.PromptTokens != 4650 {
		t.Fatalf("after compaction in=%d context=%d prompt=%d", sess.InputTokens, sess.ContextTokens, sess.PromptTokens)
	}
	// A continuation appends a user prompt, only that is new
	tallyUsage(sess, llm.Usage{InputTokens: 950, OutputTokens: 10, CacheReadTokens: 900, CacheWriteTokens: 50})
	if sess.InputTokens != 1440 || sess.OutputTokens != 220 {
		t.Fatalf("after continue in=%d out=%d", sess.InputTokens, sess.OutputTokens)
	}
	if sess.CacheReadTokens != 900 || sess.CacheWriteTokens != 50 {
		t.Fatalf("cache read=%d write=%d", sess.CacheReadTokens, sess.CacheWriteTokens)
	}
}

func TestCacheTTLFollowsStallRisk(t *testing.T) {
	quick := &runContext{def: &Definition{Approval: ApprovalNone}, settings: &v1.GitAgentsSettings{}}
	if got := cacheTTL(quick); got != "" {
		t.Fatalf("quick agent ttl %q", got)
	}
	gated := &runContext{def: &Definition{Approval: ApprovalCommits}, settings: &v1.GitAgentsSettings{}}
	if got := cacheTTL(gated); got != "1h" {
		t.Fatalf("gated agent ttl %q", got)
	}
	shell := &runContext{def: &Definition{Approval: ApprovalNone}, settings: &v1.GitAgentsSettings{AllowCommands: proto.Bool(true)}}
	if got := cacheTTL(shell); got != "1h" {
		t.Fatalf("shell agent ttl %q", got)
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
	if got := approvalTitle("create_issue", map[string]any{"title": "Default keybind missing"}); got != "Open issue: Default keybind missing" {
		t.Fatalf("got %q", got)
	}
}
