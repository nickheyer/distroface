package jobparser

import (
	"errors"
	"strings"
	"testing"

	"gitea.com/gitea/runner/act/exprparser"
)

const needsFieldsSource = `on: push
jobs:
  a:
    runs-on: x
    steps: [{run: true}]
  b:
    needs: a
    continue-on-error: ${{ needs.a.outputs.flaky == 'true' }}
    runs-on: x
    steps: [{run: true}]
  c:
    needs: a
    timeout-minutes: ${{ needs.a.outputs.t }}
    runs-on: x
    steps: [{run: true}]
`

func TestNeedsInContinueOnErrorAndTimeoutDefer(t *testing.T) {
	swfs, err := Parse([]byte(needsFieldsSource))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*SingleWorkflow{}
	for _, swf := range swfs {
		id, _ := swf.Job()
		byID[id] = swf
	}
	if byID["a"].Deferred || !byID["b"].Deferred || !byID["c"].Deferred {
		t.Fatalf("deferred a=%v b=%v c=%v", byID["a"].Deferred, byID["b"].Deferred, byID["c"].Deferred)
	}
	swfs, err = Parse([]byte(needsFieldsSource), WithJobResults(map[string]string{"a": "success"}), WithJobOutputs(map[string]map[string]string{"a": {"flaky": "true", "t": "7"}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, swf := range swfs {
		id, job := swf.Job()
		if swf.Deferred {
			t.Fatalf("%s still deferred with results", id)
		}
		switch id {
		case "b":
			if !job.GetContinueOnError() {
				t.Fatalf("b continue-on-error = %v", job.RawContinueOnError.Value)
			}
		case "c":
			if !strings.Contains(job.TimeoutMinutes, "needs.a.outputs.t") {
				t.Fatalf("c timeout-minutes = %q, the runner evaluates it", job.TimeoutMinutes)
			}
		}
	}
}

func TestUnclosedExpressionIsAnError(t *testing.T) {
	src := "run-name: \"${{ a }} ${{ b\"\non: push\njobs:\n  a:\n    runs-on: x\n    steps: [{run: true}]\n"
	if _, err := Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), "unclosed expression") {
		t.Fatalf("err = %v", err)
	}
	ee := NewExpressionEvaluator(exprparser.NewInterpeter(&exprparser.EvaluationEnvironment{}, exprparser.Config{}))
	if _, err := ee.InterpolateErr("${{ 'x' }} ${{ y"); err == nil {
		t.Fatal("InterpolateErr must report the unclosed expression")
	}
	if got := ee.Interpolate("${{ 'x' }} ${{ y"); got != "" {
		t.Fatalf("Interpolate = %q", got)
	}
	if got, err := ee.InterpolateErr("a ${{ 'b' }} c"); err != nil || got != "a b c" {
		t.Fatalf("InterpolateErr = %q, %v", got, err)
	}
}

func TestDecodePanicBecomesError(t *testing.T) {
	_, err := Parse([]byte("on: push\njobs:\n  a:\n    runs-on: [{a: b}]\n    steps: [{run: true}]\n"))
	var de *DecodeError
	if err == nil || !errors.As(err, &de) {
		t.Fatalf("err = %v, want a DecodeError", err)
	}
	if de.Node.Line == 0 || !strings.Contains(de.Error(), "cannot decode") {
		t.Fatalf("decode error = %v", de)
	}
}
