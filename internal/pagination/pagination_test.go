package pagination

import (
	"strings"
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

var testSpec = Spec{
	Fields: map[string]string{"action": "action", "actor": "actor"},
	Text:   []string{"action", "actor"},
}

func TestParseQueryNilSafe(t *testing.T) {
	if !ParseQuery(nil).IsZero() {
		t.Fatal("nil page should parse to zero query")
	}
	if !ParseQuery(&v1.PageRequest{}).IsZero() {
		t.Fatal("empty page should parse to zero query")
	}
}

func TestParseQueryDropsEmptyPredicates(t *testing.T) {
	q := ParseQuery(&v1.PageRequest{Query: &v1.Query{
		Text: "  log  ",
		Filters: []*v1.FieldFilter{
			nil,
			{Field: "", Value: "x"},
			{Field: "actor", Value: "   "},
			{Field: "Actor", Match: v1.MatchKind_MATCH_KIND_EQUALS, Value: " admin "},
		},
	}})
	if q.Text != "log" {
		t.Fatalf("text = %q", q.Text)
	}
	if len(q.Filters) != 1 {
		t.Fatalf("filters = %v", q.Filters)
	}
	f := q.Filters[0]
	if f.Field != "actor" || f.Match != MatchEquals || f.Value != "admin" {
		t.Fatalf("filter = %+v", f)
	}
}

func TestValidateRejectsUnknownField(t *testing.T) {
	bad := Query{Filters: []Filter{{Field: "password_hash", Value: "x"}}}
	if err := testSpec.Validate(bad); err == nil {
		t.Fatal("expected unknown field error")
	}
	good := Query{Filters: []Filter{{Field: "actor", Value: "x"}}}
	if err := testSpec.Validate(good); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSQLEscapesAndCombines(t *testing.T) {
	q := Query{
		Text: "log",
		Filters: []Filter{
			{Field: "actor", Match: MatchEquals, Value: "admin"},
			{Field: "action", Match: MatchPrefix, Value: "Auth_50%"},
		},
	}
	cond, args := testSpec.SQL(q)

	want := `(action LIKE ? ESCAPE '\' OR actor LIKE ? ESCAPE '\') AND actor = ? AND action LIKE ? ESCAPE '\'`
	if cond != want {
		t.Fatalf("cond = %s", cond)
	}
	if len(args) != 4 {
		t.Fatalf("args = %v", args)
	}
	if args[0] != "%log%" || args[2] != "admin" {
		t.Fatalf("args = %v", args)
	}
	if args[3] != `Auth\_50\%%` {
		t.Fatalf("prefix arg = %q, wildcards must be escaped", args[3])
	}
}

func TestSQLDropsNonAllowlistedFields(t *testing.T) {
	q := Query{Filters: []Filter{{Field: "secret", Value: "x"}}}
	cond, args := testSpec.SQL(q)
	if cond != "" || len(args) != 0 {
		t.Fatalf("non allowlisted field leaked: %s %v", cond, args)
	}
}

func TestSQLEmptyQuery(t *testing.T) {
	cond, args := testSpec.SQL(Query{})
	if cond != "" || len(args) != 0 {
		t.Fatalf("empty query produced sql: %s %v", cond, args)
	}
}

func TestOrderByAllowlist(t *testing.T) {
	allowed := map[string]bool{"name": true}
	p := &v1.PageRequest{OrderBy: "name desc"}
	if got := OrderBy(p, allowed, "created_at DESC"); got != "name DESC" {
		t.Fatalf("got %q", got)
	}
	p.OrderBy = "password_hash asc"
	if got := OrderBy(p, allowed, "created_at DESC"); got != "created_at DESC" {
		t.Fatalf("got %q", got)
	}
}

func TestSort(t *testing.T) {
	columns := map[string]func(a, b int) int{
		"value": func(a, b int) int { return a - b },
	}

	items := []int{2, 3, 1}
	Sort(&v1.PageRequest{OrderBy: "value desc"}, items, columns)
	if items[0] != 3 || items[2] != 1 {
		t.Fatalf("desc got %v", items)
	}

	Sort(&v1.PageRequest{OrderBy: "value"}, items, columns)
	if items[0] != 1 || items[2] != 3 {
		t.Fatalf("asc got %v", items)
	}

	// Unknown column and nil page leave order alone
	items = []int{2, 3, 1}
	Sort(&v1.PageRequest{OrderBy: "secret desc"}, items, columns)
	Sort(nil, items, columns)
	if items[0] != 2 || items[1] != 3 || items[2] != 1 {
		t.Fatalf("got %v", items)
	}
}

func TestLikeContainsEscapes(t *testing.T) {
	got := LikeContains(`50%_\`)
	if !strings.Contains(got, `\%`) || !strings.Contains(got, `\_`) || !strings.Contains(got, `\\`) {
		t.Fatalf("got %q", got)
	}
}
