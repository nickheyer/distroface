package registry

import (
	"testing"

	"github.com/nickheyer/distroface/pkg/logger"
)

func newTestMapper(t *testing.T, rules []MappingRule) *PathMapper {
	t.Helper()
	m, err := NewPathMapper(rules, logger.New())
	if err != nil {
		t.Fatalf("NewPathMapper: %v", err)
	}
	return m
}

func TestNewPathMapper(t *testing.T) {
	if m := newTestMapper(t, nil); m != nil {
		t.Error("expected nil mapper for empty rules")
	}

	_, err := NewPathMapper([]MappingRule{{Pattern: `(`, Replace: `x`}}, logger.New())
	if err == nil {
		t.Error("expected error for invalid regex")
	}

	_, err = NewPathMapper([]MappingRule{{Pattern: ``, Replace: `x`}}, logger.New())
	if err == nil {
		t.Error("expected error for empty pattern")
	}

	_, err = NewPathMapper([]MappingRule{{Pattern: `x`, Replace: ``}}, logger.New())
	if err == nil {
		t.Error("expected error for empty replace")
	}
}

func TestMapName(t *testing.T) {
	m := newTestMapper(t, []MappingRule{
		{Pattern: `[^/]+`, Replace: `acme/${0}`},
		{Pattern: `old-team/(.+)`, Replace: `acme/$1`},
	})

	cases := []struct {
		name string
		want string
	}{
		{"myimage", "acme/myimage"},
		{"old-team/thing", "acme/thing"},
		{"old-team/nested/thing", "acme/nested/thing"},
		{"acme/myimage", "acme/myimage"},
		{"a/b/c", "a/b/c"},
	}
	for _, c := range cases {
		if got := m.MapName(c.name); got != c.want {
			t.Errorf("MapName(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMapNameAnchored(t *testing.T) {
	// Substring matches must not apply, rules are anchored to the full name
	m := newTestMapper(t, []MappingRule{{Pattern: `old`, Replace: `new`}})
	if got := m.MapName("old-team/thing"); got != "old-team/thing" {
		t.Errorf("substring pattern applied: got %q", got)
	}
	if got := m.MapName("old"); got != "new" {
		t.Errorf("full-name match not applied: got %q", got)
	}
}

func TestMapNameFirstMatchWins(t *testing.T) {
	m := newTestMapper(t, []MappingRule{
		{Pattern: `app(.*)`, Replace: `first/app$1`},
		{Pattern: `[^/]+`, Replace: `second/${0}`},
	})
	if got := m.MapName("appserverbuild"); got != "first/appserverbuild" {
		t.Errorf("expected first rule to win, got %q", got)
	}
	if got := m.MapName("alpine"); got != "second/alpine" {
		t.Errorf("expected fallthrough to second rule, got %q", got)
	}
}

func TestMapNameRejectsInvalidResult(t *testing.T) {
	m := newTestMapper(t, []MappingRule{
		{Pattern: `bad`, Replace: `/leading-slash`},
		{Pattern: `worse`, Replace: `double//slash`},
	})
	if got := m.MapName("bad"); got != "bad" {
		t.Errorf("invalid mapped name should fall back to original, got %q", got)
	}
	if got := m.MapName("worse"); got != "worse" {
		t.Errorf("invalid mapped name should fall back to original, got %q", got)
	}
}

func TestMapNameNilMapper(t *testing.T) {
	var m *PathMapper
	if got := m.MapName("myimage"); got != "myimage" {
		t.Errorf("nil mapper must be identity, got %q", got)
	}
}
