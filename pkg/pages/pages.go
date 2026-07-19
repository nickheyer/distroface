package pages

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"gorm.io/gorm"
)

const DefaultPageSize = 20
const MaxPageSize = 500

// Parse decodes an offset cursor and clamps the page size
func Parse(p *v1.PageRequest) (limit, offset int) {
	limit = DefaultPageSize
	if p == nil {
		return
	}
	if p.PageSize > 0 {
		limit = int(p.PageSize)
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}

	if p.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(p.PageToken)
		if err == nil {
			if v, err := strconv.Atoi(string(decoded)); err == nil && v >= 0 {
				offset = v
			}
		}
	}
	return
}

func splitOrderBy(p *v1.PageRequest) (col string, desc bool) {
	if p == nil {
		return "", false
	}
	fields := strings.Fields(strings.ToLower(p.OrderBy))
	if len(fields) == 0 {
		return "", false
	}
	return fields[0], len(fields) > 1 && fields[1] == "desc"
}

// OrderBy resolves "column direction" against an allowlist
func OrderBy(p *v1.PageRequest, allowed map[string]bool, def string) string {
	col, desc := splitOrderBy(p)
	if !allowed[col] {
		return def
	}
	if desc {
		return col + " DESC"
	}
	return col + " ASC"
}

// For sorting in memory only when sql isn't there to do it for us
func Sort[T any](p *v1.PageRequest, items []T, columns map[string]func(a, b T) int) {
	col, desc := splitOrderBy(p)
	cmp := columns[col]
	if cmp == nil {
		return
	}
	if desc {
		asc := cmp
		cmp = func(a, b T) int { return -asc(a, b) }
	}
	slices.SortStableFunc(items, cmp)
}

// Info builds the response cursor from the served window
func Info(offset, limit int, total int64) *v1.PageInfo {
	info := &v1.PageInfo{TotalCount: total}
	next := offset + limit
	if int64(next) < total {
		info.NextPageToken = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(next)))
	}
	return info
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// LikeContains escapes wildcards and wraps for substring match
func LikeContains(q string) string {
	return "%" + likeEscaper.Replace(q) + "%"
}

// LikePrefix escapes wildcards and wraps for prefix match
func LikePrefix(q string) string {
	return likeEscaper.Replace(q) + "%"
}

// Match is how a filter value is compared
type Match int

const (
	MatchContains Match = iota
	MatchEquals
	MatchPrefix
)

// Filter is one allowlisted field predicate
type Filter struct {
	Field string
	Match Match
	Value string
}

// Query is the neutral form of the proto query message
type Query struct {
	Text    string
	Filters []Filter
}

func (q Query) IsZero() bool {
	return q.Text == "" && len(q.Filters) == 0
}

// ParseQuery converts the proto query, nil safe, drops empty predicates
func ParseQuery(p *v1.PageRequest) Query {
	if p == nil || p.Query == nil {
		return Query{}
	}
	q := Query{Text: strings.TrimSpace(p.Query.Text)}
	for _, f := range p.Query.Filters {
		if f == nil {
			continue
		}
		field := strings.ToLower(strings.TrimSpace(f.Field))
		value := strings.TrimSpace(f.Value)
		if field == "" || value == "" {
			continue
		}
		m := MatchContains
		switch f.Match {
		case v1.MatchKind_MATCH_KIND_EQUALS:
			m = MatchEquals
		case v1.MatchKind_MATCH_KIND_PREFIX:
			m = MatchPrefix
		}
		q.Filters = append(q.Filters, Filter{Field: field, Match: m, Value: value})
	}
	return q
}

// Spec is one endpoint's allowlisted filter surface
type Spec struct {
	Fields map[string]string // Public field name to column
	Text   []string          // Columns matched by free text
}

// Validate rejects filter fields outside the allowlist
func (s Spec) Validate(q Query) error {
	for _, f := range q.Filters {
		if _, ok := s.Fields[f.Field]; !ok {
			return fmt.Errorf("unknown filter field %q", f.Field)
		}
	}
	return nil
}

// SQL renders the query as a where fragment for raw statements
func (s Spec) SQL(q Query) (string, []any) {
	var conds []string
	var args []any

	if q.Text != "" && len(s.Text) > 0 {
		parts := make([]string, len(s.Text))
		for i, col := range s.Text {
			parts[i] = col + ` LIKE ? ESCAPE '\'`
			args = append(args, LikeContains(q.Text))
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}

	for _, f := range q.Filters {
		col, ok := s.Fields[f.Field]
		if !ok {
			continue
		}
		switch f.Match {
		case MatchEquals:
			conds = append(conds, col+" = ?")
			args = append(args, f.Value)
		case MatchPrefix:
			conds = append(conds, col+` LIKE ? ESCAPE '\'`)
			args = append(args, LikePrefix(f.Value))
		default:
			conds = append(conds, col+` LIKE ? ESCAPE '\'`)
			args = append(args, LikeContains(f.Value))
		}
	}

	return strings.Join(conds, " AND "), args
}

// Scope applies the query as a gorm scope, non allowlisted fields dropped
func (s Spec) Scope(q Query) func(*gorm.DB) *gorm.DB {
	cond, args := s.SQL(q)
	return func(tx *gorm.DB) *gorm.DB {
		if cond == "" {
			return tx
		}
		return tx.Where(cond, args...)
	}
}
