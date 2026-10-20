// Package expr evaluates GitHub Actions expressions
package expr

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Context resolves the named roots an expression may reference
type Context interface {
	// Value behind a root such as github, steps, or matrix
	Lookup(name string) (any, error)
	// Job status that success, failure, and cancelled test
	JobStatus() string
	// Workspace hash behind hashFiles
	HashFiles(patterns []string) (string, error)
}

// Indexer lets a value decide how its own properties resolve
type Indexer interface {
	Index(key string) (any, error)
}

// Filtered marks the result of a .* filter so later indexes map over it
type Filtered []any

// Expr is one parsed expression
type Expr struct {
	root node
}

// Parse accepts an expression with or without the ${{ }} wrapper
func Parse(src string) (*Expr, error) {
	toks, err := tokenize(Unwrap(src))
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected token %q", p.toks[p.pos].text)
	}
	return &Expr{root: root}, nil
}

func (e *Expr) Eval(c Context) (any, error) {
	return e.root.eval(c)
}

// Reports whether the expression calls always, success, failure, or cancelled
func (e *Expr) HasStatusFunction() bool {
	return hasStatusCall(e.root)
}

func hasStatusCall(n node) bool {
	switch x := n.(type) {
	case *callNode:
		switch strings.ToLower(x.name) {
		case "always", "success", "failure", "cancelled":
			return true
		}
		for _, a := range x.args {
			if hasStatusCall(a) {
				return true
			}
		}
	case *notNode:
		return hasStatusCall(x.inner)
	case *logicNode:
		return hasStatusCall(x.left) || hasStatusCall(x.right)
	case *binNode:
		return hasStatusCall(x.left) || hasStatusCall(x.right)
	case *indexNode:
		return hasStatusCall(x.inner) || hasStatusCall(x.key)
	case *filterNode:
		return hasStatusCall(x.inner)
	}
	return false
}

// Unwrap strips a surrounding ${{ }} and whitespace
func Unwrap(src string) string {
	src = strings.TrimSpace(src)
	if strings.HasPrefix(src, "${{") && strings.HasSuffix(src, "}}") {
		src = strings.TrimSpace(src[3 : len(src)-2])
	}
	return src
}

func Eval(src string, c Context) (any, error) {
	e, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return e.Eval(c)
}

// Condition applies the implicit success() of an if, empty means success()
func Condition(src string, c Context) (bool, error) {
	status := c.JobStatus()
	ok := status == "" || status == "success"
	if strings.TrimSpace(src) == "" {
		return ok, nil
	}
	e, err := Parse(src)
	if err != nil {
		return false, err
	}
	if !ok && !e.HasStatusFunction() {
		return false, nil
	}
	v, err := e.Eval(c)
	if err != nil {
		return false, err
	}
	return Truthy(v), nil
}

// Interpolate replaces every ${{ }} in s, the first failure is the error
func Interpolate(s string, c Context) (string, error) {
	return Expand(s, func(_, src string) (string, error) {
		v, err := Eval(src, c)
		if err != nil {
			return "", err
		}
		return Stringify(v), nil
	})
}

// Expand hands every ${{ }} of s to eval with its raw text and inner source
func Expand(s string, eval func(raw, src string) (string, error)) (string, error) {
	if !strings.Contains(s, "${{") {
		return s, nil
	}
	var out strings.Builder
	rest := s
	for {
		i := strings.Index(rest, "${{")
		if i < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:i])
		j := closingBrace(rest[i+3:])
		if j < 0 {
			return "", fmt.Errorf("unclosed expression %q", strings.TrimSpace(rest[i:]))
		}
		raw, src := rest[i:i+3+j+2], rest[i+3:i+3+j]
		v, err := eval(raw, src)
		if err != nil {
			return "", fmt.Errorf("expression %q: %w", strings.TrimSpace(src), err)
		}
		out.WriteString(v)
		rest = rest[i+3+j+2:]
	}
}

// Offset of the closing }} honoring single quoted strings
func closingBrace(s string) int {
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

// Tokens

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
		case isDigit(ch) || (ch == '-' || ch == '.') && i+1 < len(s) && isDigit(s[i+1]) && operandExpected(toks):
			j := i + 1
			if ch == '0' && j < len(s) && (s[j] == 'x' || s[j] == 'X') {
				j++
				for j < len(s) && isHex(s[j]) {
					j++
				}
			} else {
				for j < len(s) && (isDigit(s[j]) || s[j] == '.' || s[j] == 'e' || s[j] == 'E' || ((s[j] == '-' || s[j] == '+') && (s[j-1] == 'e' || s[j-1] == 'E'))) {
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
				if f, err = strconv.ParseFloat(text, 64); err != nil {
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

// A sign or leading dot starts a number only where an operand may begin
func operandExpected(toks []token) bool {
	if len(toks) == 0 {
		return true
	}
	last := toks[len(toks)-1]
	return last.kind == tokOp && last.text != ")" && last.text != "]"
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// Parser, lowest precedence first: || && (== !=) (< <= > >=) ! postfix

type node interface {
	eval(c Context) (any, error)
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
	left, err := p.parseEq()
	if err != nil {
		return nil, err
	}
	for p.acceptOp("&&") {
		right, err := p.parseEq()
		if err != nil {
			return nil, err
		}
		left = &logicNode{op: "&&", left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseEq() (node, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != tokOp || (t.text != "==" && t.text != "!=") {
			return left, nil
		}
		p.pos++
		right, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		left = &binNode{op: t.text, left: left, right: right}
	}
}

func (p *parser) parseCmp() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != tokOp || (t.text != "<" && t.text != "<=" && t.text != ">" && t.text != ">=") {
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

// Nodes

type litNode struct{ val any }

func (n *litNode) eval(Context) (any, error) { return n.val, nil }

type ctxNode struct{ name string }

func (n *ctxNode) eval(c Context) (any, error) { return c.Lookup(strings.ToLower(n.name)) }

type notNode struct{ inner node }

func (n *notNode) eval(c Context) (any, error) {
	v, err := n.inner.eval(c)
	if err != nil {
		return nil, err
	}
	return !Truthy(v), nil
}

type logicNode struct {
	op          string
	left, right node
}

func (n *logicNode) eval(c Context) (any, error) {
	l, err := n.left.eval(c)
	if err != nil {
		return nil, err
	}
	if n.op == "&&" {
		if !Truthy(l) {
			return l, nil
		}
		return n.right.eval(c)
	}
	if Truthy(l) {
		return l, nil
	}
	return n.right.eval(c)
}

type binNode struct {
	op          string
	left, right node
}

func (n *binNode) eval(c Context) (any, error) {
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
		return LooseEqual(l, r), nil
	case "!=":
		return !LooseEqual(l, r), nil
	}
	a, b := ToNumber(l), ToNumber(r)
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

func (n *indexNode) eval(c Context) (any, error) {
	obj, err := n.inner.eval(c)
	if err != nil {
		return nil, err
	}
	key, err := n.key.eval(c)
	if err != nil {
		return nil, err
	}
	return index(obj, key)
}

func index(obj, key any) (any, error) {
	switch o := obj.(type) {
	case Indexer:
		return o.Index(Stringify(key))
	case map[string]any:
		ks := Stringify(key)
		if v, ok := o[ks]; ok {
			return v, nil
		}
		for k, v := range o {
			if strings.EqualFold(k, ks) {
				return v, nil
			}
		}
		return nil, nil
	case []any:
		f := ToNumber(key)
		if math.IsNaN(f) {
			return nil, nil
		}
		i := int(f)
		if i < 0 || i >= len(o) {
			return nil, nil
		}
		return o[i], nil
	case Filtered:
		out := Filtered{}
		for _, item := range o {
			v, err := index(item, key)
			if err != nil {
				return nil, err
			}
			if v != nil {
				out = append(out, v)
			}
		}
		return out, nil
	}
	return nil, nil
}

type filterNode struct{ inner node }

func (n *filterNode) eval(c Context) (any, error) {
	obj, err := n.inner.eval(c)
	if err != nil {
		return nil, err
	}
	switch o := obj.(type) {
	case Indexer:
		return Filtered{}, nil
	case map[string]any:
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(Filtered, 0, len(o))
		for _, k := range keys {
			out = append(out, o[k])
		}
		return out, nil
	case []any:
		return Filtered(o), nil
	case Filtered:
		return o, nil
	}
	return Filtered{}, nil
}

type callNode struct {
	name string
	args []node
}

func (n *callNode) eval(c Context) (any, error) {
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
		return contains(args[0], args[1]), nil
	case "startswith":
		if len(args) != 2 {
			return nil, errors.New("startsWith takes 2 arguments")
		}
		return strings.HasPrefix(strings.ToLower(Stringify(args[0])), strings.ToLower(Stringify(args[1]))), nil
	case "endswith":
		if len(args) != 2 {
			return nil, errors.New("endsWith takes 2 arguments")
		}
		return strings.HasSuffix(strings.ToLower(Stringify(args[0])), strings.ToLower(Stringify(args[1]))), nil
	case "format":
		if len(args) < 1 {
			return nil, errors.New("format needs a template")
		}
		return format(Stringify(args[0]), args[1:])
	case "join":
		if len(args) < 1 || len(args) > 2 {
			return nil, errors.New("join takes 1 or 2 arguments")
		}
		sep := ","
		if len(args) == 2 {
			sep = Stringify(args[1])
		}
		return join(args[0], sep), nil
	case "tojson":
		if len(args) != 1 {
			return nil, errors.New("toJSON takes 1 argument")
		}
		b, err := json.MarshalIndent(plain(args[0]), "", "  ")
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case "fromjson":
		if len(args) != 1 {
			return nil, errors.New("fromJSON takes 1 argument")
		}
		var v any
		if err := json.Unmarshal([]byte(Stringify(args[0])), &v); err != nil {
			return nil, fmt.Errorf("fromJSON: %w", err)
		}
		return v, nil
	case "hashfiles":
		if len(args) < 1 {
			return nil, errors.New("hashFiles needs at least one pattern")
		}
		patterns := make([]string, 0, len(args))
		for _, a := range args {
			patterns = append(patterns, Stringify(a))
		}
		return c.HashFiles(patterns)
	case "always":
		return true, nil
	case "success":
		s := c.JobStatus()
		return s == "" || s == "success", nil
	case "failure":
		return c.JobStatus() == "failure", nil
	case "cancelled":
		return c.JobStatus() == "cancelled", nil
	}
	return nil, fmt.Errorf("unknown function %s", n.name)
}

func contains(search, item any) bool {
	switch s := plain(search).(type) {
	case []any:
		for _, v := range s {
			if LooseEqual(v, item) {
				return true
			}
		}
		return false
	}
	return strings.Contains(strings.ToLower(Stringify(search)), strings.ToLower(Stringify(item)))
}

func format(tpl string, args []any) (string, error) {
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
			out.WriteString(Stringify(args[idx]))
			i += j
		default:
			out.WriteByte(ch)
		}
	}
	return out.String(), nil
}

func join(v any, sep string) string {
	if s, ok := plain(v).([]any); ok {
		parts := make([]string, len(s))
		for i, item := range s {
			parts[i] = Stringify(item)
		}
		return strings.Join(parts, sep)
	}
	return Stringify(v)
}

// Coercion

// Filtered and indexers as plain json shaped values
func plain(v any) any {
	switch x := v.(type) {
	case Filtered:
		return []any(x)
	case interface{ Plain() any }:
		return x.Plain()
	}
	return v
}

func Truthy(v any) bool {
	switch x := plain(v).(type) {
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

func ToNumber(v any) float64 {
	switch x := plain(v).(type) {
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
	case int32:
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

// LooseEqual compares the way == does, case insensitive for strings
func LooseEqual(a, b any) bool {
	a, b = plain(a), plain(b)
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
	x, y := ToNumber(a), ToNumber(b)
	if math.IsNaN(x) || math.IsNaN(y) {
		return false
	}
	return x == y
}

// Stringify renders a value the way an expression does inside text
func Stringify(v any) string {
	switch x := plain(v).(type) {
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
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case map[string]any:
		return "Object"
	case []any:
		return "Array"
	}
	return fmt.Sprint(v)
}

// StringMap widens a string map for use as a context value
func StringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
