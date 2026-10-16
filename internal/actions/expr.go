package actions

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

// ErrDeferred marks expressions only the runner can finish
var ErrDeferred = errors.New("expression deferred to runner")

// Contexts available to an expression, nil maps mean deferred
type ExprContext struct {
	GitHub  map[string]any
	Env     map[string]string
	Vars    map[string]string
	Secrets map[string]string
	Needs   map[string]any
	Matrix  map[string]any
	Steps   map[string]any
	Inputs  map[string]any
	// success, failure, cancelled, or empty for success
	Status string
}

func (c *ExprContext) lookup(name string) (any, error) {
	switch name {
	case "github":
		if c.GitHub == nil {
			return nil, ErrDeferred
		}
		return c.GitHub, nil
	case "env":
		if c.Env == nil {
			return nil, ErrDeferred
		}
		return envMap(stringMapAny(c.Env)), nil
	case "vars":
		if c.Vars == nil {
			return map[string]any{}, nil
		}
		return stringMapAny(c.Vars), nil
	case "secrets":
		if c.Secrets == nil {
			return nil, ErrDeferred
		}
		return stringMapAny(c.Secrets), nil
	case "needs":
		if c.Needs == nil {
			return map[string]any{}, nil
		}
		return c.Needs, nil
	case "matrix":
		if c.Matrix == nil {
			return map[string]any{}, nil
		}
		return c.Matrix, nil
	case "steps":
		if c.Steps == nil {
			return nil, ErrDeferred
		}
		return c.Steps, nil
	case "inputs":
		if c.Inputs == nil {
			return map[string]any{}, nil
		}
		return c.Inputs, nil
	case "job", "runner", "strategy":
		return nil, ErrDeferred
	}
	return nil, fmt.Errorf("unknown context %q", name)
}

func stringMapAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Eval parses and evaluates one expression without the ${{ }} wrapper
func Eval(src string, c *ExprContext) (any, error) {
	src = strings.TrimSpace(src)
	if strings.HasPrefix(src, "${{") && strings.HasSuffix(src, "}}") {
		src = strings.TrimSpace(src[3 : len(src)-2])
	}
	p := &exprParser{toks: nil}
	toks, err := exprTokenize(src)
	if err != nil {
		return nil, err
	}
	p.toks = toks
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected token %q", p.toks[p.pos].text)
	}
	return node.eval(c)
}

// EvalBool evaluates an if style condition, empty means success()
func EvalBool(src string, c *ExprContext) (bool, error) {
	if strings.TrimSpace(src) == "" {
		return c.Status == "" || c.Status == "success", nil
	}
	v, err := Eval(src, c)
	if err != nil {
		return false, err
	}
	return truthy(v), nil
}

// Interpolate replaces every ${{ }} it can evaluate, deferred ones stay
func Interpolate(s string, c *ExprContext) (string, error) {
	if !strings.Contains(s, "${{") {
		return s, nil
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
		raw := rest[i : i+3+j+2]
		v, err := Eval(rest[i+3:i+3+j], c)
		switch {
		case errors.Is(err, ErrDeferred):
			out.WriteString(raw)
		case err != nil:
			return "", err
		default:
			out.WriteString(stringify(v))
		}
		rest = rest[i+3+j+2:]
	}
	return out.String(), nil
}

// Finds the closing }} honoring single quoted strings
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

// HasDeferred reports whether a string still carries unresolved expressions
func HasDeferred(s string) bool {
	return strings.Contains(s, "${{")
}

// ── Tokens ───────────────────────────────────────────────────────────────

type exprTokKind int

const (
	tokIdent exprTokKind = iota
	tokNumber
	tokString
	tokOp
)

type exprTok struct {
	kind exprTokKind
	text string
	num  float64
}

func exprTokenize(s string) ([]exprTok, error) {
	var toks []exprTok
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
			toks = append(toks, exprTok{kind: tokString, text: sb.String()})
		case ch >= '0' && ch <= '9' || (ch == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && numberAllowed(toks)) || (ch == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && numberAllowed(toks)):
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
			toks = append(toks, exprTok{kind: tokNumber, text: text, num: f})
			i = j
		case ch == '_' || unicode.IsLetter(rune(ch)):
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] == '-' || unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j]))) {
				j++
			}
			toks = append(toks, exprTok{kind: tokIdent, text: s[i:j]})
			i = j
		default:
			two := ""
			if i+1 < len(s) {
				two = s[i : i+2]
			}
			switch two {
			case "==", "!=", "<=", ">=", "&&", "||":
				toks = append(toks, exprTok{kind: tokOp, text: two})
				i += 2
				continue
			}
			switch ch {
			case '(', ')', '[', ']', '.', ',', '!', '<', '>', '*':
				toks = append(toks, exprTok{kind: tokOp, text: string(ch)})
				i++
			default:
				return nil, fmt.Errorf("unexpected character %q", string(ch))
			}
		}
	}
	return toks, nil
}

// A leading minus or dot starts a number only where an operand may begin
func numberAllowed(toks []exprTok) bool {
	if len(toks) == 0 {
		return true
	}
	last := toks[len(toks)-1]
	if last.kind != tokOp {
		return false
	}
	return last.text != ")" && last.text != "]"
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// ── Parser ───────────────────────────────────────────────────────────────

type exprNode interface {
	eval(c *ExprContext) (any, error)
}

type exprParser struct {
	toks []exprTok
	pos  int
}

func (p *exprParser) peek() *exprTok {
	if p.pos < len(p.toks) {
		return &p.toks[p.pos]
	}
	return nil
}

func (p *exprParser) acceptOp(text string) bool {
	t := p.peek()
	if t != nil && t.kind == tokOp && t.text == text {
		p.pos++
		return true
	}
	return false
}

func (p *exprParser) expectOp(text string) error {
	if !p.acceptOp(text) {
		if t := p.peek(); t != nil {
			return fmt.Errorf("expected %q, found %q", text, t.text)
		}
		return fmt.Errorf("expected %q", text)
	}
	return nil
}

func (p *exprParser) parseOr() (exprNode, error) {
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

func (p *exprParser) parseAnd() (exprNode, error) {
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

func (p *exprParser) parseEq() (exprNode, error) {
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

func (p *exprParser) parseCmp() (exprNode, error) {
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

func (p *exprParser) parseUnary() (exprNode, error) {
	if p.acceptOp("!") {
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &notNode{inner: inner}, nil
	}
	return p.parsePostfix()
}

func (p *exprParser) parsePostfix() (exprNode, error) {
	node, err := p.parsePrimary()
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
				node = &filterNode{inner: node}
				continue
			}
			if t.kind != tokIdent && t.kind != tokNumber {
				return nil, fmt.Errorf("bad property %q", t.text)
			}
			node = &indexNode{inner: node, key: &litNode{val: t.text}}
		case p.acceptOp("["):
			key, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if err := p.expectOp("]"); err != nil {
				return nil, err
			}
			node = &indexNode{inner: node, key: key}
		default:
			return node, nil
		}
	}
}

func (p *exprParser) parsePrimary() (exprNode, error) {
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
			var args []exprNode
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

// ── Nodes ────────────────────────────────────────────────────────────────

type litNode struct{ val any }

func (n *litNode) eval(*ExprContext) (any, error) { return n.val, nil }

type ctxNode struct{ name string }

func (n *ctxNode) eval(c *ExprContext) (any, error) { return c.lookup(strings.ToLower(n.name)) }

type notNode struct{ inner exprNode }

func (n *notNode) eval(c *ExprContext) (any, error) {
	v, err := n.inner.eval(c)
	if err != nil {
		return nil, err
	}
	return !truthy(v), nil
}

type logicNode struct {
	op          string
	left, right exprNode
}

func (n *logicNode) eval(c *ExprContext) (any, error) {
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
	left, right exprNode
}

func (n *binNode) eval(c *ExprContext) (any, error) {
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
	inner exprNode
	key   exprNode
}

func (n *indexNode) eval(c *ExprContext) (any, error) {
	obj, err := n.inner.eval(c)
	if err != nil {
		return nil, err
	}
	key, err := n.key.eval(c)
	if err != nil {
		return nil, err
	}
	if em, ok := obj.(envMap); ok {
		ks := stringify(key)
		if v, ok := em[ks]; ok {
			return v, nil
		}
		for k, v := range em {
			if strings.EqualFold(k, ks) {
				return v, nil
			}
		}
		return nil, ErrDeferred
	}
	return indexValue(obj, key), nil
}

// Static env whose unknown keys may still arrive from GITHUB_ENV
type envMap map[string]any

func indexValue(obj, key any) any {
	switch o := obj.(type) {
	case envMap:
		return indexValue(map[string]any(o), key)
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

// Marks the result of a .* filter so later indexes map over it
type filteredValues []any

type filterNode struct{ inner exprNode }

func (n *filterNode) eval(c *ExprContext) (any, error) {
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
	args []exprNode
}

func (n *callNode) eval(c *ExprContext) (any, error) {
	name := strings.ToLower(n.name)
	if name == "hashfiles" {
		return nil, ErrDeferred
	}
	args := make([]any, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(c)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
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
	case "always":
		return true, nil
	case "success":
		return c.Status == "" || c.Status == "success", nil
	case "failure":
		return c.Status == "failure", nil
	case "cancelled":
		return c.Status == "cancelled", nil
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
		for _, v := range s {
			if looseEqual(v, item) {
				return true
			}
		}
		return false
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

// ── Coercion ─────────────────────────────────────────────────────────────

func normalize(v any) any {
	switch x := v.(type) {
	case filteredValues:
		return []any(x)
	case envMap:
		return map[string]any(x)
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
