// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package jobparser

import (
	"gitea.dev/actionslib/pkg/expreval"
	"gitea.dev/actionslib/pkg/exprparser"
	"go.yaml.in/yaml/v4"
)

// Server side expression evaluation over one interpreter
type ExpressionEvaluator struct {
	eval expreval.Evaluator
}

func NewExpressionEvaluator(interpreter exprparser.Interpreter) *ExpressionEvaluator {
	return &ExpressionEvaluator{eval: expreval.New(interpreter.Evaluate)}
}

// EvaluateBool answers a condition the way a job if does
func (ee ExpressionEvaluator) EvaluateBool(in string) (bool, error) {
	return ee.eval.EvalBool(in, exprparser.DefaultStatusCheckSuccess)
}

func (ee ExpressionEvaluator) EvaluateYamlNode(node *yaml.Node) error {
	return ee.eval.EvaluateYamlNode(node)
}

func (ee ExpressionEvaluator) Interpolate(in string) string {
	out, err := ee.InterpolateErr(in)
	if err != nil {
		return ""
	}
	return out
}

// Expands every expression, reporting malformed or failing ones
func (ee ExpressionEvaluator) InterpolateErr(in string) (string, error) {
	return ee.eval.Interpolate(in)
}
