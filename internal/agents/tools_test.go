package agents

import (
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func TestToolsForOffersEveryConfiguredTool(t *testing.T) {
	def := &Definition{Kind: KindCustom, Tools: []string{"create_issue", "post_comment", "update_issue", "submit_review", "run_command", "no_such_tool"}}
	names := func(cfg *v1.GitAgentsSettings) []string {
		var out []string
		for _, tool := range toolsFor(def, cfg) {
			out = append(out, tool.Name)
		}
		return out
	}
	// No target, no pull request, nothing but commands policy withholds a tool
	got := names(&v1.GitAgentsSettings{})
	want := []string{"create_issue", "post_comment", "update_issue", "submit_review"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	got = names(&v1.GitAgentsSettings{AllowCommands: proto.Bool(true)})
	if len(got) != 5 || got[4] != "run_command" {
		t.Fatalf("commands allowed, got %v", got)
	}
}

func TestEveryKindDefaultCanOpenIssuesExceptReviewAndChat(t *testing.T) {
	has := func(kind string) bool {
		for _, name := range DefaultTools(kind) {
			if name == "create_issue" {
				return true
			}
		}
		return false
	}
	for _, kind := range []string{KindCode, KindTriage, KindCustom} {
		if !has(kind) {
			t.Errorf("%s should open issues by default", kind)
		}
	}
	for _, kind := range []string{KindReview, KindChat} {
		if has(kind) {
			t.Errorf("%s should not open issues by default", kind)
		}
	}
}
