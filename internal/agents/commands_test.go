package agents

import (
	"strings"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
)

func TestJobNotesCarryAnnotationsAndFailedSteps(t *testing.T) {
	j := &db.ActionsJob{
		Annotations: `[{"level":"error","message":"no registered runner matches runs-on ubuntu-latest"},{"level":"","message":" "}]`,
		Steps:       `[{"number":1,"name":"Checkout","status":"completed","conclusion":"success"},{"number":2,"name":"Run","status":"completed","conclusion":"failure"}]`,
	}
	notes := jobNotes(j)
	if len(notes) != 2 {
		t.Fatalf("expected the annotation and the failed step, got %q", notes)
	}
	if !strings.HasPrefix(notes[0], "error: no registered runner") {
		t.Fatalf("annotation line %q", notes[0])
	}
	if notes[1] != `step "Run" ended with failure` {
		t.Fatalf("step line %q", notes[1])
	}
}

func TestJobNotesToleratesEmptyColumns(t *testing.T) {
	if notes := jobNotes(&db.ActionsJob{}); len(notes) != 0 {
		t.Fatalf("empty job must yield no notes, got %q", notes)
	}
	if notes := jobNotes(&db.ActionsJob{Annotations: "not json", Steps: "[]"}); len(notes) != 0 {
		t.Fatalf("bad json must yield no notes, got %q", notes)
	}
}

func TestSplitLabels(t *testing.T) {
	got := splitLabels(" self-hosted, linux ,,x64 ")
	if strings.Join(got, "|") != "self-hosted|linux|x64" {
		t.Fatalf("got %q", got)
	}
	if splitLabels("  ") != nil {
		t.Fatal("blank input must yield nil")
	}
}
