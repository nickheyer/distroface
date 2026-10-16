package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"
)

// Every context the runner can resolve
type exprContext struct {
	github    map[string]any
	env       map[string]string
	vars      map[string]string
	secrets   map[string]string
	needs     map[string]any
	matrix    map[string]any
	steps     map[string]any
	inputs    map[string]any
	job       map[string]any
	runner    map[string]any
	status    string
	workspace string
}

func (c *exprContext) lookup(name string) (any, error) {
	switch name {
	case "github":
		return orMap(c.github), nil
	case "env":
		return stringMapAny(c.env), nil
	case "vars":
		return stringMapAny(c.vars), nil
	case "secrets":
		return stringMapAny(c.secrets), nil
	case "needs":
		return orMap(c.needs), nil
	case "matrix":
		return orMap(c.matrix), nil
	case "steps":
		return orMap(c.steps), nil
	case "inputs":
		return orMap(c.inputs), nil
	case "job":
		return orMap(c.job), nil
	case "runner":
		return orMap(c.runner), nil
	case "strategy":
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("unknown context %q", name)
}

func orMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func stringMapAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Parses and evaluates one expression, wrapper optional
func evalExpr(src string, c *exprContext) (any, error) {
	src = strings.TrimSpace(src)
	if strings.HasPrefix(src, "${{") && strings.HasSuffix(src, "}}") {
		src = strings.TrimSpace(src[3 : len(src)-2])
	}
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected token %q", p.toks[p.pos].text)
	}
	return node.eval(c)
}

// Empty condition means success()
func evalCondition(src string, c *exprContext) (bool, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return c.status == "" || c.status == "success", nil
	}
	v, err := evalExpr(src, c)
	if err != nil {
		return false, err
	}
	return truthy(v), nil
}

// Replaces every expression it can evaluate, failures stay verbatim
func interpolate(s string, c *exprContext) string {
	if !strings.Contains(s, "${{") {
		return s
	}
	var out strings.Builder
	rest := s
	for {
		i := strings.Index(rest, "${{")
		if i < 0 {
			out.WriteString(rest)
			break
		}
		out.WriteString(rest[:i])
		j := exprEnd(rest[i+3:])
		if j < 0 {
			out.WriteString(rest[i:])
			break
		}
		v, err := evalExpr(rest[i+3:i+3+j], c)
		if err != nil {
			out.WriteString(rest[i : i+3+j+2])
		} else {
			out.WriteString(stringify(v))
		}
		rest = rest[i+3+j+2:]
	}
	return out.String()
}

func interpolateMap(m map[string]string, c *exprContext) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = interpolate(v, c)
	}
	return out
}

func exprEnd(s string) int {
	inStr := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\'':
			if inStr && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inStr = !inStr
		case !inStr && s[i] == '}' && i+1 < len(s) && s[i+1] == '}':
			return i
		}
	}
	return -1
}

type tokKind int

const (
	tokIdent tokKind = iota
	tokNumber
	tokString
	tokOp
)

type token struct {
	kind tokKind
	text string
	num  float64
}

func tokenize(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			i++
		case ch == '\'':
			var sb strings.Builder
			i++
			closed := false
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						sb.WriteByte('\'')
						i += 2
						continue
					}
					closed = true
					i++
					break
				}
				sb.WriteByte(s[i])
				i++
			}
			if !closed {
				return nil, errors.New("unterminated string")
			}
			toks = append(toks, token{kind: tokString, text: sb.String()})
		case ch >= '0' && ch <= '9' || (ch == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && numberAllowed(toks)):
			j := i + 1
			if ch == '0' && j < len(s) && (s[j] == 'x' || s[j] == 'X') {
				j++
				for j < len(s) && isHex(s[j]) {
					j++
				}
			} else {
				for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.' || s[j] == 'e' || s[j] == 'E' || ((s[j] == '-' || s[j] == '+') && (s[j-1] == 'e' || s[j-1] == 'E'))) {
					j++
				}
			}
			text := s[i:j]
			var f float64
			if strings.HasPrefix(strings.ToLower(text), "0x") {
				u, err := strconv.ParseUint(text[2:], 16, 64)
				if err != nil {
					return nil, fmt.Errorf("bad number %q", text)
				}
				f = float64(u)
			} else {
				var err error
				f, err = strconv.ParseFloat(text, 64)
				if err != nil {
					return nil, fmt.Errorf("bad number %q", text)
				}
			}
			toks = append(toks, token{kind: tokNumber, text: text, num: f})
			i = j
		case ch == '_' || unicode.IsLetter(rune(ch)):
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] == '-' || unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j]))) {
				j++
			}
			toks = append(toks, token{kind: tokIdent, text: s[i:j]})
			i = j
		default:
			if i+1 < len(s) {
				switch two := s[i : i+2]; two {
				case "==", "!=", "<=", ">=", "&&", "||":
					toks = append(toks, token{kind: tokOp, text: two})
					i += 2
					continue
				}
			}
			switch ch {
			case '(', ')', '[', ']', '.', ',', '!', '<', '>', '*':
				toks = append(toks, token{kind: tokOp, text: string(ch)})
				i++
			default:
				return nil, fmt.Errorf("unexpected character %q", string(ch))
			}
		}
	}
	return toks, nil
}

func numberAllowed(toks []token) bool {
	if len(toks) == 0 {
		return true
	}
	last := toks[len(toks)-1]
	return last.kind == tokOp && last.text != ")" && last.text != "]"
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

type node interface {
	eval(c *exprContext) (any, error)
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() *token {
	if p.pos < len(p.toks) {
		return &p.toks[p.pos]
	}
	return nil
}

func (p *parser) acceptOp(text string) bool {
	t := p.peek()
	if t != nil && t.kind == tokOp && t.text == text {
		p.pos++
		return true
	}
	return false
}

func (p *parser) expectOp(text string) error {
	if !p.acceptOp(text) {
		if t := p.peek(); t != nil {
			return fmt.Errorf("expected %q, found %q", text, t.text)
		}
		return fmt.Errorf("expected %q", text)
	}
	return nil
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.acceptOp("||") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &logicNode{op: "||", left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for p.acceptOp("&&") {
		right, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		left = &logicNode{op: "&&", left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseCmp() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != tokOp {
			return left, nil
		}
		switch t.text {
		case "==", "!=", "<", "<=", ">", ">=":
		default:
			return left, nil
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &binNode{op: t.text, left: left, right: right}
	}
}

func (p *parser) parseUnary() (node, error) {
	if p.acceptOp("!") {
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &notNode{inner: inner}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.acceptOp("."):
			t := p.peek()
			if t == nil {
				return nil, errors.New("expected property name")
			}
			p.pos++
			if t.kind == tokOp && t.text == "*" {
				n = &filterNode{inner: n}
				continue
			}
			if t.kind != tokIdent && t.kind != tokNumber {
				return nil, fmt.Errorf("bad property %q", t.text)
			}
			n = &indexNode{inner: n, key: &litNode{val: t.text}}
		case p.acceptOp("["):
			key, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp("]"); err != nil {
				return nil, err
			}
			n = &indexNode{inner: n, key: key}
		default:
			return n, nil
		}
	}
}

func (p *parser) parsePrimary() (node, error) {
	t := p.peek()
	if t == nil {
		return nil, errors.New("unexpected end of expression")
	}
	p.pos++
	switch t.kind {
	case tokNumber:
		return &litNode{val: t.num}, nil
	case tokString:
		return &litNode{val: t.text}, nil
	case tokIdent:
		switch strings.ToLower(t.text) {
		case "true":
			return &litNode{val: true}, nil
		case "false":
			return &litNode{val: false}, nil
		case "null":
			return &litNode{val: nil}, nil
		}
		if p.acceptOp("(") {
			var args []node
			if !p.acceptOp(")") {
				for {
					a, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.acceptOp(")") {
						break
					}
					if err := p.expectOp(","); err != nil {
						return nil, err
					}
				}
			}
			return &callNode{name: t.text, args: args}, nil
		}
		return &ctxNode{name: t.text}, nil
	case tokOp:
		if t.text == "(" {
			inner, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return inner, nil
		}
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

type litNode struct{ val any }

func (n *litNode) eval(*exprContext) (any, error) { return n.val, nil }

type ctxNode struct{ name string }

func (n *ctxNode) eval(c *exprContext) (any, error) { return c.lookup(strings.ToLower(n.name)) }

type notNode struct{ inner node }

func (n *notNode) eval(c *exprContext) (any, error) {
	v, err := n.inner.eval(c)
	if err != nil {
		return nil, err
	}
	return !truthy(v), nil
}

type logicNode struct {
	op          string
	left, right node
}

func (n *logicNode) eval(c *exprContext) (any, error) {
	l, err := n.left.eval(c)
	if err != nil {
		return nil, err
	}
	if n.op == "&&" {
		if !truthy(l) {
			return l, nil
		}
		return n.right.eval(c)
	}
	if truthy(l) {
		return l, nil
	}
	return n.right.eval(c)
}

type binNode struct {
	op          string
	left, right node
}

func (n *binNode) eval(c *exprContext) (any, error) {
	l, err := n.left.eval(c)
	if err != nil {
		return nil, err
	}
	r, err := n.right.eval(c)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "==":
		return looseEqual(l, r), nil
	case "!=":
		return !looseEqual(l, r), nil
	}
	a, b := toNumber(l), toNumber(r)
	if math.IsNaN(a) || math.IsNaN(b) {
		return false, nil
	}
	switch n.op {
	case "<":
		return a < b, nil
	case "<=":
		return a <= b, nil
	case ">":
		return a > b, nil
	case ">=":
		return a >= b, nil
	}
	return nil, fmt.Errorf("unknown operator %s", n.op)
}

type indexNode struct {
	inner node
	key   node
}

func (n *indexNode) eval(c *exprContext) (any, error) {
	obj, err := n.inner.eval(c)
	if err != nil {
		return nil, err
	}
	key, err := n.key.eval(c)
	if err != nil {
		return nil, err
	}
	return indexValue(obj, key), nil
}

type filteredValues []any

func indexValue(obj, key any) any {
	switch o := obj.(type) {
	case map[string]any:
		ks := stringify(key)
		if v, ok := o[ks]; ok {
			return v
		}
		for k, v := range o {
			if strings.EqualFold(k, ks) {
				return v
			}
		}
		return nil
	case []any:
		f := toNumber(key)
		if math.IsNaN(f) {
			return nil
		}
		i := int(f)
		if i < 0 || i >= len(o) {
			return nil
		}
		return o[i]
	case filteredValues:
		var out []any
		for _, item := range o {
			if v := indexValue(item, key); v != nil {
				out = append(out, v)
			}
		}
		return filteredValues(out)
	}
	return nil
}

type filterNode struct{ inner node }

func (n *filterNode) eval(c *exprContext) (any, error) {
	obj, err := n.inner.eval(c)
	if err != nil {
		return nil, err
	}
	switch o := obj.(type) {
	case map[string]any:
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(filteredValues, 0, len(o))
		for _, k := range keys {
			out = append(out, o[k])
		}
		return out, nil
	case []any:
		return filteredValues(o), nil
	case filteredValues:
		return o, nil
	}
	return filteredValues{}, nil
}

type callNode struct {
	name string
	args []node
}

func (n *callNode) eval(c *exprContext) (any, error) {
	args := make([]any, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(c)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	name := strings.ToLower(n.name)
	switch name {
	case "contains":
		if len(args) != 2 {
			return nil, errors.New("contains takes 2 arguments")
		}
		return fnContains(args[0], args[1]), nil
	case "startswith":
		if len(args) != 2 {
			return nil, errors.New("startsWith takes 2 arguments")
		}
		return strings.HasPrefix(strings.ToLower(stringify(args[0])), strings.ToLower(stringify(args[1]))), nil
	case "endswith":
		if len(args) != 2 {
			return nil, errors.New("endsWith takes 2 arguments")
		}
		return strings.HasSuffix(strings.ToLower(stringify(args[0])), strings.ToLower(stringify(args[1]))), nil
	case "format":
		if len(args) < 1 {
			return nil, errors.New("format needs a template")
		}
		return fnFormat(stringify(args[0]), args[1:])
	case "join":
		if len(args) < 1 || len(args) > 2 {
			return nil, errors.New("join takes 1 or 2 arguments")
		}
		sep := ","
		if len(args) == 2 {
			sep = stringify(args[1])
		}
		return fnJoin(args[0], sep), nil
	case "tojson":
		if len(args) != 1 {
			return nil, errors.New("toJSON takes 1 argument")
		}
		b, err := json.MarshalIndent(normalize(args[0]), "", "  ")
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case "fromjson":
		if len(args) != 1 {
			return nil, errors.New("fromJSON takes 1 argument")
		}
		var v any
		if err := json.Unmarshal([]byte(stringify(args[0])), &v); err != nil {
			return nil, fmt.Errorf("fromJSON: %w", err)
		}
		return v, nil
	case "hashfiles":
		patterns := make([]string, 0, len(args))
		for _, a := range args {
			patterns = append(patterns, stringify(a))
		}
		return hashFiles(c.workspace, patterns), nil
	case "always":
		return true, nil
	case "success":
		return c.status == "" || c.status == "success", nil
	case "failure":
		return c.status == "failure", nil
	case "cancelled":
		return c.status == "cancelled", nil
	}
	return nil, fmt.Errorf("unknown function %s", n.name)
}

func fnContains(search, item any) bool {
	switch s := search.(type) {
	case []any:
		for _, v := range s {
			if looseEqual(v, item) {
				return true
			}
		}
		return false
	case filteredValues:
		return fnContains([]any(s), item)
	}
	return strings.Contains(strings.ToLower(stringify(search)), strings.ToLower(stringify(item)))
}

func fnFormat(tpl string, args []any) (string, error) {
	var out strings.Builder
	for i := 0; i < len(tpl); i++ {
		ch := tpl[i]
		switch {
		case ch == '{' && i+1 < len(tpl) && tpl[i+1] == '{':
			out.WriteByte('{')
			i++
		case ch == '}' && i+1 < len(tpl) && tpl[i+1] == '}':
			out.WriteByte('}')
			i++
		case ch == '{':
			j := strings.IndexByte(tpl[i:], '}')
			if j < 0 {
				return "", errors.New("format: unclosed placeholder")
			}
			idx, err := strconv.Atoi(tpl[i+1 : i+j])
			if err != nil || idx < 0 || idx >= len(args) {
				return "", fmt.Errorf("format: bad placeholder %q", tpl[i:i+j+1])
			}
			out.WriteString(stringify(args[idx]))
			i += j
		default:
			out.WriteByte(ch)
		}
	}
	return out.String(), nil
}

func fnJoin(v any, sep string) string {
	switch s := v.(type) {
	case []any:
		parts := make([]string, len(s))
		for i, item := range s {
			parts[i] = stringify(item)
		}
		return strings.Join(parts, sep)
	case filteredValues:
		return fnJoin([]any(s), sep)
	}
	return stringify(v)
}

// Sha256 over sorted per file digests, empty without matches
func hashFiles(root string, patterns []string) string {
	if root == "" {
		return ""
	}
	fsys := os.DirFS(root)
	var files []string
	seen := map[string]bool{}
	for _, pat := range patterns {
		pat = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(pat)), "./")
		if pat == "" {
			continue
		}
		matches, err := doublestar.Glob(fsys, pat, doublestar.WithFilesOnly())
		if err != nil {
			continue
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	total := sha256.New()
	for _, f := range files {
		h := sha256.New()
		fh, err := os.Open(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		io.Copy(h, fh)
		fh.Close()
		total.Write(h.Sum(nil))
	}
	return hex.EncodeToString(total.Sum(nil))
}

func normalize(v any) any {
	if f, ok := v.(filteredValues); ok {
		return []any(f)
	}
	return v
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

func toNumber(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if strings.HasPrefix(strings.ToLower(s), "0x") {
			if u, err := strconv.ParseUint(s[2:], 16, 64); err == nil {
				return float64(u)
			}
			return math.NaN()
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	return math.NaN()
}

func looseEqual(a, b any) bool {
	a, b = normalize(a), normalize(b)
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return strings.EqualFold(as, bs)
	}
	if a == nil && b == nil {
		return true
	}
	_, amap := a.(map[string]any)
	_, bmap := b.(map[string]any)
	_, aarr := a.([]any)
	_, barr := b.([]any)
	if amap || bmap || aarr || barr {
		return false
	}
	x, y := toNumber(a), toNumber(b)
	if math.IsNaN(x) || math.IsNaN(y) {
		return false
	}
	return x == y
}

func stringify(v any) string {
	switch x := normalize(v).(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case map[string]any:
		return "Object"
	case []any:
		return "Array"
	}
	return fmt.Sprint(v)
}
