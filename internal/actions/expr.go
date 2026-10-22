package actions

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nickheyer/distroface/pkg/expr"
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
	// Called workflow jobs, set while evaluating workflow_call outputs
	Jobs map[string]any
	// Job strategy once its matrix legs exist
	Strategy map[string]any
	// success, failure, cancelled, or empty for success
	Status string
}

func (c *ExprContext) Lookup(name string) (any, error) {
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
		return envMap(expr.StringMap(c.Env)), nil
	case "vars":
		return expr.StringMap(c.Vars), nil
	case "secrets":
		if c.Secrets == nil {
			return nil, ErrDeferred
		}
		return expr.StringMap(c.Secrets), nil
	case "needs":
		// Nil until the needed jobs finish
		if c.Needs == nil {
			return nil, ErrDeferred
		}
		return c.Needs, nil
	case "matrix":
		return orMap(c.Matrix), nil
	case "steps":
		if c.Steps == nil {
			return nil, ErrDeferred
		}
		return c.Steps, nil
	case "inputs":
		return orMap(c.Inputs), nil
	case "jobs":
		if c.Jobs == nil {
			return nil, ErrDeferred
		}
		return c.Jobs, nil
	case "strategy":
		if c.Strategy == nil {
			return nil, ErrDeferred
		}
		return c.Strategy, nil
	case "job", "runner":
		return nil, ErrDeferred
	}
	return nil, fmt.Errorf("unknown context %q", name)
}

func (c *ExprContext) JobStatus() string { return c.Status }

// Only the runner has the workspace
func (c *ExprContext) HashFiles([]string) (string, error) { return "", ErrDeferred }

func orMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Static env whose unknown keys may still arrive from GITHUB_ENV
type envMap map[string]any

func (m envMap) Index(key string) (any, error) {
	if v, ok := m[key]; ok {
		return v, nil
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, nil
		}
	}
	return nil, ErrDeferred
}

func (m envMap) Plain() any { return map[string]any(m) }

// Eval parses and evaluates one expression, wrapper optional
func Eval(src string, c *ExprContext) (any, error) {
	return expr.Eval(src, c)
}

// EvalBool evaluates an if style condition, empty means success()
func EvalBool(src string, c *ExprContext) (bool, error) {
	return expr.Condition(src, c)
}

// Reports whether a condition calls always, success, failure, or cancelled
func HasStatusCheck(src string) bool {
	e, err := expr.Parse(src)
	return err == nil && e.HasStatusFunction()
}

// Interpolate replaces every ${{ }} it can evaluate, deferred ones stay
func Interpolate(s string, c *ExprContext) (string, error) {
	return expr.Expand(s, func(raw, src string) (string, error) {
		v, err := expr.Eval(src, c)
		if errors.Is(err, ErrDeferred) {
			return raw, nil
		}
		if err != nil {
			return "", err
		}
		return expr.Stringify(v), nil
	})
}

// HasDeferred reports whether a string still carries unresolved expressions
func HasDeferred(s string) bool {
	return strings.Contains(s, "${{")
}

func stringify(v any) string   { return expr.Stringify(v) }
func looseEqual(a, b any) bool { return expr.LooseEqual(a, b) }
