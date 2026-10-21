package actions

import (
	"errors"
	"testing"

	"github.com/nickheyer/distroface/pkg/expr"
)

func testContext() *ExprContext {
	return &ExprContext{
		GitHub: map[string]any{
			"event_name": "push",
			"ref":        "refs/heads/main",
			"ref_name":   "main",
			"sha":        "abc123",
			"repository": "acme/widgets",
			"actor":      "nick",
			"run_number": float64(42),
			"event": map[string]any{
				"head_commit": map[string]any{"message": "feat: thing [deploy]"},
				"pull_request": map[string]any{
					"labels": []any{map[string]any{"name": "bug"}, map[string]any{"name": "urgent"}},
					"draft":  false,
				},
			},
		},
		Env:     map[string]string{"CI": "true", "Mode": "fast"},
		Vars:    map[string]string{"REGION": "us-east-1"},
		Secrets: map[string]string{"TOKEN": "s3cret"},
		Needs: map[string]any{
			"build": map[string]any{"result": "success", "outputs": map[string]any{"version": "1.2.3"}},
		},
		Matrix: map[string]any{"os": "linux", "node": float64(20)},
		Inputs: map[string]any{"debug": true, "level": float64(3)},
	}
}

func TestEvalLiteralsAndOperators(t *testing.T) {
	cases := []struct {
		expr string
		want any
	}{
		{"true", true},
		{"false", false},
		{"null", nil},
		{"42", float64(42)},
		{"0x1F", float64(31)},
		{"-3.5", float64(-3.5)},
		{"'hello'", "hello"},
		{"'it''s'", "it's"},
		{"1 == 1", true},
		{"1 == '1'", true},
		{"'Main' == 'main'", true},
		{"null == 0", true},
		{"true == 1", true},
		{"'a' != 'b'", true},
		{"2 < 3", true},
		{"3 <= 3", true},
		{"4 > 5", false},
		{"'10' >= 9", true},
		{"!true", false},
		{"!''", true},
		{"true && 'x'", "x"},
		{"false || 'y'", "y"},
		{"null || 0 || 'z'", "z"},
		{"(1 == 2) || (2 == 2)", true},
		{"!(1 == 1) || 1 < 0", false},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, testContext())
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if !looseEqual(got, c.want) || (got == nil) != (c.want == nil) {
			t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
		}
	}
}

func TestEvalContexts(t *testing.T) {
	cases := []struct {
		expr string
		want any
	}{
		{"github.ref", "refs/heads/main"},
		{"github['ref_name']", "main"},
		{"github.event.head_commit.message", "feat: thing [deploy]"},
		{"github.event.pull_request.draft", false},
		{"github.missing.deeper", nil},
		{"env.CI", "true"},
		{"env.mode", "fast"},
		{"vars.REGION", "us-east-1"},
		{"secrets.TOKEN", "s3cret"},
		{"needs.build.outputs.version", "1.2.3"},
		{"needs.build.result == 'success'", true},
		{"matrix.os", "linux"},
		{"matrix.node == 20", true},
		{"inputs.debug", true},
		{"inputs.level > 2", true},
		{"github.event.pull_request.labels[1].name", "urgent"},
		{"github.event.pull_request.labels.*.name", nil},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, testContext())
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if c.expr == "github.event.pull_request.labels.*.name" {
			if fv, ok := got.(expr.Filtered); !ok || len(fv) != 2 {
				t.Errorf("%s = %#v, want two filtered values", c.expr, got)
			}
			continue
		}
		if !looseEqual(got, c.want) || (got == nil) != (c.want == nil) {
			t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
		}
	}
}

func TestEvalFunctions(t *testing.T) {
	cases := []struct {
		expr string
		want any
	}{
		{"contains('Hello World', 'world')", true},
		{"contains(github.event.pull_request.labels.*.name, 'bug')", true},
		{"contains(github.event.pull_request.labels.*.name, 'docs')", false},
		{"contains(fromJSON('[\"a\",\"b\"]'), 'B')", true},
		{"startsWith('refs/heads/main', 'refs/heads/')", true},
		{"startsWith(github.ref, 'refs/tags/')", false},
		{"endsWith('file.yaml', '.YAML')", true},
		{"format('{0}-{1}-{{x}}', 'a', 2)", "a-2-{x}"},
		{"join(fromJSON('[\"a\",\"b\"]'), ', ')", "a, b"},
		{"join(github.event.pull_request.labels.*.name)", "bug,urgent"},
		{"fromJSON('{\"k\": 7}').k", float64(7)},
		{"fromJSON('true')", true},
		{"toJSON(matrix.os)", "\"linux\""},
		{"always()", true},
		{"success()", true},
		{"failure()", false},
		{"cancelled()", false},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, testContext())
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if !looseEqual(got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
		}
	}
}

func TestEvalStatusFunctions(t *testing.T) {
	ctx := testContext()
	ctx.Status = ConclusionFailure
	for expr, want := range map[string]bool{
		"success()": false, "failure()": true, "always()": true, "cancelled()": false, "": false,
		"github.ref == 'refs/heads/main'":                 false,
		"${{ github.ref == 'refs/heads/main' }}":          false,
		"always() && github.ref == 'refs/heads/main'":     true,
		"failure() || github.ref == 'refs/heads/main'":    true,
		"!cancelled() && github.ref == 'refs/heads/main'": true,
	} {
		got, err := EvalBool(expr, ctx)
		if err != nil {
			t.Fatalf("%q: %v", expr, err)
		}
		if got != want {
			t.Errorf("%q with failure status = %v, want %v", expr, got, want)
		}
	}
	ctx.Status = ConclusionSkipped
	if ok, _ := EvalBool("github.ref == 'refs/heads/main'", ctx); ok {
		t.Error("skipped needs should skip a job without a status function")
	}
	ctx.Status = ""
	if ok, _ := EvalBool("", ctx); !ok {
		t.Error("empty condition should default to success()")
	}
	if ok, _ := EvalBool("${{ github.ref == 'refs/heads/main' }}", ctx); !ok {
		t.Error("wrapped condition should evaluate")
	}
}

func TestEvalDeferred(t *testing.T) {
	ctx := testContext()
	for _, expr := range []string{"steps.build.outputs.x", "hashFiles('**/go.sum')", "env.UNKNOWN", "runner.os", "job.status"} {
		_, err := Eval(expr, ctx)
		if !errors.Is(err, ErrDeferred) {
			t.Errorf("%s: want ErrDeferred, got %v", expr, err)
		}
	}
	ctx.Steps = map[string]any{"build": map[string]any{"outputs": map[string]any{"x": "1"}, "outcome": "success"}}
	got, err := Eval("steps.build.outcome", ctx)
	if err != nil || got != "success" {
		t.Errorf("steps lookup = %v, %v", got, err)
	}
}

func TestEvalErrors(t *testing.T) {
	for _, expr := range []string{"1 +", "unknownfn()", "'unterminated", "github.", "(1", "bogus.ref", "contains(1)"} {
		if _, err := Eval(expr, testContext()); err == nil {
			t.Errorf("%q should fail", expr)
		}
	}
}

func TestInterpolate(t *testing.T) {
	ctx := testContext()
	cases := []struct {
		in, want string
	}{
		{"plain text", "plain text"},
		{"ref=${{ github.ref_name }}", "ref=main"},
		{"${{ matrix.os }}-${{ matrix.node }}", "linux-20"},
		{"v${{ needs.build.outputs.version }}", "v1.2.3"},
		{"${{ format('{0}/{1}', github.repository, github.sha) }}", "acme/widgets/abc123"},
		{"${{ steps.x.outputs.y }} stays", "${{ steps.x.outputs.y }} stays"},
		{"${{ hashFiles('**/lock') }}", "${{ hashFiles('**/lock') }}"},
		{"${{ env.CI }} ${{ env.LATER }}", "true ${{ env.LATER }}"},
		{"quote ${{ '}}' }} end", "quote }} end"},
		{"${{ toJSON(inputs.debug) }}", "true"},
		{"${{ 'a' == 'A' }}", "true"},
	}
	for _, c := range cases {
		got, err := Interpolate(c.in, ctx)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %q, want %q", c.in, got, c.want)
		}
	}
	if _, err := Interpolate("${{ 1 + }}", ctx); err == nil {
		t.Error("invalid expression inside interpolation should fail")
	}
}

func TestStringify(t *testing.T) {
	cases := map[any]string{nil: "", true: "true", float64(3): "3", float64(2.5): "2.5", "x": "x"}
	for in, want := range cases {
		if got := stringify(in); got != want {
			t.Errorf("stringify(%#v) = %q, want %q", in, got, want)
		}
	}
	if stringify(map[string]any{}) != "Object" || stringify([]any{}) != "Array" {
		t.Error("collections should stringify like GitHub")
	}
}
