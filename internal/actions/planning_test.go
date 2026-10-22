package actions

import (
	"errors"
	"testing"
)

const exprFieldsWorkflow = `
name: X
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    continue-on-error: ${{ inputs.soft }}
    timeout-minutes: ${{ fromJSON(vars.T) }}
    container:
      image: ghcr.io/org/private:1
      credentials:
        username: bot
        password: ${{ secrets.REG }}
    services:
      db:
        image: ghcr.io/org/db:1
        credentials:
          username: dbbot
          password: ${{ secrets.DB }}
    strategy:
      fail-fast: ${{ inputs.ff }}
      max-parallel: ${{ inputs.par }}
      matrix:
        os: [a, b]
    steps:
      - run: echo
        continue-on-error: ${{ matrix.os == 'a' }}
        timeout-minutes: ${{ matrix.os == 'a' && 5 || 10 }}
      - run: echo
        continue-on-error: true
        timeout-minutes: 3
`

func TestParseExpressionFields(t *testing.T) {
	wf, err := ParseWorkflow([]byte(exprFieldsWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	job := wf.Jobs["build"]
	if job.ContinueOnError || job.ContinueOnErrorExpr != "${{ inputs.soft }}" {
		t.Errorf("job continue-on-error = %v %q", job.ContinueOnError, job.ContinueOnErrorExpr)
	}
	if job.TimeoutMinutes != 0 || job.TimeoutMinutesExpr != "${{ fromJSON(vars.T) }}" {
		t.Errorf("job timeout = %v %q", job.TimeoutMinutes, job.TimeoutMinutesExpr)
	}
	if c := job.Container.Credentials; c == nil || c.Username != "bot" || c.Password != "${{ secrets.REG }}" {
		t.Errorf("container credentials = %+v", c)
	}
	if c := job.Services["db"].Credentials; c == nil || c.Username != "dbbot" {
		t.Errorf("service credentials = %+v", c)
	}
	if st := job.Strategy; st.FailFast != nil || st.FailFastExpr != "${{ inputs.ff }}" || st.MaxParallel != 0 || st.MaxParallelExpr != "${{ inputs.par }}" {
		t.Errorf("strategy = %+v", st)
	}
	if s := job.Steps[0]; s.ContinueOnErrorExpr != "${{ matrix.os == 'a' }}" || s.TimeoutMinutesExpr != "${{ matrix.os == 'a' && 5 || 10 }}" {
		t.Errorf("step 1 = %+v", s)
	}
	if s := job.Steps[1]; !s.ContinueOnError || s.TimeoutMinutes != 3 || s.ContinueOnErrorExpr != "" || s.TimeoutMinutesExpr != "" {
		t.Errorf("step 2 = %+v", s)
	}
}

func TestResolveStrategy(t *testing.T) {
	st := &Strategy{MatrixExpr: "${{ fromJSON(needs.setup.outputs.matrix) }}", FailFastExpr: "${{ inputs.ff }}", MaxParallelExpr: "${{ inputs.par }}"}
	base := &ExprContext{Inputs: map[string]any{"ff": false, "par": float64(2)}}
	if _, deferred, err := resolveStrategy(st, base); err != nil || !deferred {
		t.Fatalf("without needs: deferred=%v err=%v", deferred, err)
	}
	base.Needs = map[string]any{"setup": map[string]any{"result": "success", "outputs": map[string]any{"matrix": `{"os":["linux","mac"],"include":[{"os":"linux","cc":"gcc"}]}`}}}
	out, deferred, err := resolveStrategy(st, base)
	if err != nil || deferred {
		t.Fatalf("with needs: deferred=%v err=%v", deferred, err)
	}
	if len(out.Matrix["os"]) != 2 || len(out.Include) != 1 || out.MatrixExpr != "" {
		t.Errorf("matrix = %+v", out)
	}
	if out.FailFast == nil || *out.FailFast || out.FailFastExpr != "" {
		t.Errorf("fail-fast = %+v", out)
	}
	if out.MaxParallel != 2 || out.MaxParallelExpr != "" {
		t.Errorf("max-parallel = %+v", out)
	}
	if _, _, err := resolveStrategy(&Strategy{MaxParallelExpr: "${{ 'many' }}"}, base); err == nil {
		t.Error("a non numeric max-parallel must fail")
	}
	if out, deferred, err := resolveStrategy(nil, base); out != nil || deferred || err != nil {
		t.Error("nil strategy must pass through")
	}
}

const deferredMatrixWorkflow = `
name: X
on: push
jobs:
  setup:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.m.outputs.matrix }}
    steps:
      - id: m
        run: echo
  build:
    needs: setup
    name: Build ${{ needs.setup.outputs.label }}
    runs-on: ubuntu-latest
    strategy:
      matrix: ${{ fromJSON(needs.setup.outputs.matrix) }}
    steps:
      - run: echo
`

func TestPlanDeferredMatrix(t *testing.T) {
	wf, err := ParseWorkflow([]byte(deferredMatrixWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	base := &ExprContext{GitHub: map[string]any{}, Env: map[string]string{}, Inputs: map[string]any{}, Vars: map[string]string{}}
	rows := m.planJobs(wf, base)
	if len(rows) != 2 || rows[1].JobKey != "build" || rows[1].Status != StatusWaiting {
		t.Fatalf("rows = %+v", rows)
	}
	def, err := decodeDefinition(rows[1])
	if err != nil || !def.Deferred || def.Error != "" {
		t.Fatalf("placeholder definition = %+v, %v", def, err)
	}
	if rows[1].Name != "Build ${{ needs.setup.outputs.label }}" {
		t.Errorf("placeholder keeps the raw name, got %q", rows[1].Name)
	}

	ectx := *base
	ectx.Needs = map[string]any{"setup": map[string]any{"result": "success", "outputs": map[string]any{"matrix": `{"os":["linux","mac"]}`, "label": "v1"}}}
	strategy, deferred, err := resolveStrategy(def.Job.Strategy, &ectx)
	if err != nil || deferred {
		t.Fatalf("resolve: deferred=%v err=%v", deferred, err)
	}
	legs, err := m.matrixRows(def.Job, *def, strategy, &ectx)
	if err != nil || len(legs) != 2 {
		t.Fatalf("legs = %+v, %v", legs, err)
	}
	if legs[0].JobKey != "build (linux)" || legs[0].Name != "Build v1 (linux)" || legs[0].MatrixGroup != "build" || legs[1].JobKey != "build (mac)" {
		t.Errorf("legs = %q %q / %q", legs[0].JobKey, legs[0].Name, legs[1].JobKey)
	}
	legDef, err := decodeDefinition(legs[0])
	if err != nil || legDef.Deferred || legDef.Job.Strategy.MatrixExpr != "" || len(legDef.MatrixOrder) != 1 || legDef.MatrixOrder[0] != "os" {
		t.Errorf("leg definition = %+v, %v", legDef, err)
	}
}

func TestNeedsDeferredUntilKnown(t *testing.T) {
	c := &ExprContext{GitHub: map[string]any{}}
	if _, err := Eval("needs.setup.outputs.x", c); !errors.Is(err, ErrDeferred) {
		t.Errorf("needs before completion: %v", err)
	}
	if s, err := Interpolate("v${{ needs.setup.outputs.x }}", c); err != nil || s != "v${{ needs.setup.outputs.x }}" {
		t.Errorf("interpolate = %q, %v", s, err)
	}
	c.Needs = map[string]any{}
	if v, err := Eval("needs.setup.outputs.x", c); err != nil || v != nil {
		t.Errorf("known empty needs = %v, %v", v, err)
	}
	if _, err := Eval("strategy.job-index", c); !errors.Is(err, ErrDeferred) {
		t.Errorf("strategy before legs: %v", err)
	}
	c.Strategy = map[string]any{"job-index": 1}
	if v, _ := Eval("strategy.job-index", c); stringify(v) != "1" {
		t.Errorf("strategy = %v", v)
	}
}
