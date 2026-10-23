package agents

import (
	"testing"
)

func TestBranchAllowed(t *testing.T) {
	if !branchAllowed(nil, "main") {
		t.Fatal("empty filter must allow")
	}
	if !branchAllowed([]string{"release/*"}, "release/1.2") || branchAllowed([]string{"release/*"}, "main") {
		t.Fatal("glob mismatch")
	}
	if !branchAllowed([]string{"main"}, "refs/heads/main") {
		t.Fatal("ref prefix must be stripped")
	}
}

func TestPathsMatch(t *testing.T) {
	changed := []string{"src/a.go", "docs/readme.md"}
	if !pathsMatch([]string{"src/**"}, nil, changed) {
		t.Fatal("include should match src")
	}
	if pathsMatch([]string{"web/**"}, nil, changed) {
		t.Fatal("include should not match")
	}
	if pathsMatch(nil, []string{"**"}, changed) {
		t.Fatal("ignore all should block")
	}
	if !pathsMatch(nil, []string{"docs/**"}, changed) {
		t.Fatal("ignoring docs leaves src")
	}
}

func TestStripMention(t *testing.T) {
	got := stripMention("@reviewer please look at this, thanks @reviewer-agent", []string{"reviewer", "reviewer-agent"})
	if got != "please look at this, thanks" {
		t.Fatalf("got %q", got)
	}
	keep := stripMention("email me@reviewer.example", []string{"reviewer"})
	if keep != "email me@reviewer.example" {
		t.Fatalf("addresses must survive, got %q", keep)
	}
}

func TestPayloadHelpers(t *testing.T) {
	payload := map[string]any{
		"pull_request":  map[string]any{"number": float64(7)},
		"label":         map[string]any{"name": "agent"},
		"assignee_ids":  []string{"u1", "u2"},
		"requested_ids": []any{"a", 1},
	}
	if payloadNumber(payload) != 7 {
		t.Fatal("number")
	}
	if payloadLabel(payload) != "agent" {
		t.Fatal("label")
	}
	if ids := payloadStrings(payload["assignee_ids"]); len(ids) != 2 {
		t.Fatal("string slice")
	}
	if ids := payloadStrings(payload["requested_ids"]); len(ids) != 1 || ids[0] != "a" {
		t.Fatal("any slice")
	}
	if !containsFold([]string{"Agent"}, " agent ") {
		t.Fatal("case folding")
	}
}
