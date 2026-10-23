package agents

import (
	"strings"
	"testing"
)

func TestSplitLabels(t *testing.T) {
	got := splitLabels(" self-hosted, linux ,,x64 ")
	if strings.Join(got, "|") != "self-hosted|linux|x64" {
		t.Fatalf("got %q", got)
	}
	if splitLabels("  ") != nil {
		t.Fatal("blank input must yield nil")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("it's"); got != `'it'"'"'s'` {
		t.Fatalf("got %s", got)
	}
}
