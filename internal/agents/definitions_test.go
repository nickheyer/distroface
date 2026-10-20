package agents

import (
	"strings"
	"testing"
)

func TestParseAgentFileFrontMatter(t *testing.T) {
	src := `---
name: Security Reviewer
description: Finds vulnerabilities
kind: review
provider: openai-prod
model: gpt-4.1
tools: [read_file, get_diff, submit_review]
approval: all_writes
max_turns: 12
temperature: 0.2
triggers:
  pull_request: [opened, synchronize]
  review_requested: true
  labels: [security]
  base_branches: ["main", "release/*"]
  mention: true
---
Focus on injection and authentication issues.
`
	d, err := ParseAgentFile(".distroface/agents/security.md", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Slug != "security" || d.Name != "Security Reviewer" || d.Kind != KindReview {
		t.Fatalf("unexpected identity %+v", d)
	}
	if d.ProviderID != "openai-prod" || d.Model != "gpt-4.1" || d.Approval != ApprovalAllWrites || d.MaxTurns != 12 {
		t.Fatalf("unexpected config %+v", d)
	}
	if d.Temperature == nil || *d.Temperature != 0.2 {
		t.Fatalf("temperature not parsed")
	}
	if len(d.Tools) != 3 || d.Tools[2] != "submit_review" {
		t.Fatalf("tools %v", d.Tools)
	}
	tr := d.Triggers
	if !tr.PullRequestOpened || !tr.PullRequestSynchronize || tr.PullRequestReadyForReview || !tr.ReviewRequested || !tr.Mention {
		t.Fatalf("triggers %+v", tr)
	}
	if len(tr.IssueLabels) != 1 || tr.IssueLabels[0] != "security" || len(tr.BaseBranches) != 2 {
		t.Fatalf("label or branch filters %+v", tr)
	}
	if !strings.Contains(d.Instructions, "injection") {
		t.Fatalf("instructions lost: %q", d.Instructions)
	}
}

func TestParseAgentFileWithoutFrontMatter(t *testing.T) {
	d, err := ParseAgentFile(".github/agents/docs-writer.agent.md", "Write docs for every change.")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Slug != "docs-writer" || d.Kind != KindCustom || d.Source != SourceFile {
		t.Fatalf("unexpected %+v", d)
	}
	if d.Instructions != "Write docs for every change." {
		t.Fatalf("instructions %q", d.Instructions)
	}
}

func TestParseAgentFileRejectsUnknownTool(t *testing.T) {
	d, err := ParseAgentFile(".distroface/agents/xray.md", "---\ntools: [launch_missiles]\n---\nbody")
	if err == nil || d == nil {
		t.Fatalf("expected an error with a definition, got %v %v", d, err)
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("error %v", err)
	}
}

func TestParseAgentFileEmptyIsSkipped(t *testing.T) {
	d, err := ParseAgentFile(".distroface/agents/empty.md", "   \n")
	if d != nil || err != nil {
		t.Fatalf("expected nothing, got %v %v", d, err)
	}
}

func TestValidSlug(t *testing.T) {
	for _, ok := range []string{"reviewer", "code-bot", "ab1"} {
		if !ValidSlug(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "A", "-lead", "trail-", "has space", "admin", "actions", "x", "a1"} {
		if ValidSlug(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestDefaultToolsAreRegistered(t *testing.T) {
	for _, kind := range []string{KindReview, KindCode, KindChat, KindTriage, KindCustom} {
		for _, name := range DefaultTools(kind) {
			if _, ok := toolRegistry[name]; !ok {
				t.Errorf("%s default tool %s is not registered", kind, name)
			}
		}
	}
}

func TestDefinitionSnapshotRoundTrip(t *testing.T) {
	d, err := ParseAgentFile(".distroface/agents/code.md", "---\nkind: code\ntriggers:\n  issue_assigned: true\n---\nDo it.")
	if err != nil {
		t.Fatal(err)
	}
	back := definitionFromJSON(d.marshal())
	if back == nil || back.Slug != "code" || back.Kind != KindCode || !back.Triggers.IssueAssigned || back.Instructions != "Do it." {
		t.Fatalf("round trip lost data: %+v", back)
	}
}
