package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"gopkg.in/yaml.v3"
)

func TestPrependPath(t *testing.T) {
	got := prependPath([]string{"A=1", "PATH=/step"}, []string{"/new", "/old"}, "/base", ":")
	if strings.Join(got, " ") != "A=1 PATH=/new:/old:/step" {
		t.Errorf("step PATH must be the base: %v", got)
	}
	got = prependPath([]string{"A=1"}, []string{"/new"}, "/base", ":")
	if strings.Join(got, " ") != "A=1 PATH=/new:/base" {
		t.Errorf("container PATH must be the base: %v", got)
	}
	got = prependPath([]string{"A=1"}, nil, "/base", ":")
	if strings.Join(got, " ") != "A=1" {
		t.Errorf("no additions must leave the env alone: %v", got)
	}
}

func TestToolCachePath(t *testing.T) {
	d := &dockerExecutor{jobDir: "/host/job", toolCache: "/host/tool"}
	if got := d.Path("/host/tool/node/24.0.0/x64"); got != "/__t/node/24.0.0/x64" {
		t.Errorf("tool cache path = %q", got)
	}
	if got := d.Path("/host/job/_temp/x"); got != "/__w/_temp/x" {
		t.Errorf("job path = %q", got)
	}
	if got := d.Path("/elsewhere/x"); got != "/elsewhere/x" {
		t.Errorf("foreign path = %q", got)
	}
}

func TestMaskVariants(t *testing.T) {
	l := newLogStream(nil, "job")
	secret := `p@ss"word`
	l.AddMask(secret)
	line := `json "p@ss\"word" url p%40ss%22word b64 ` + base64.StdEncoding.EncodeToString([]byte(secret)) + " raw " + secret + "\n"
	_, _ = l.Write([]byte(line))
	if got := string(l.take(false)); got != "json \"***\" url *** b64 *** raw ***\n" {
		t.Errorf("got %q", got)
	}
}

func TestLegacyCommands(t *testing.T) {
	var out bytes.Buffer
	var ann []Annotation
	cw := &commandWriter{out: &out, onAnnotation: func(a Annotation) { ann = append(ann, a) }}
	_, _ = cw.Write([]byte("##[group]Build\n##[error file=a.go;line=3]boom\n##[warning]careful\n##[section]x\n##[endgroup]\n"))
	cw.Flush()
	text := out.String()
	for _, want := range []string{"##[group]Build\n", "##[error]boom\n", "##[warning]careful\n", "##[section]x\n", "##[endgroup]\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	if len(ann) != 2 || ann[0].File != "a.go" || ann[0].Line != 3 || ann[0].Level != "error" || ann[1].Level != "warning" {
		t.Errorf("annotations = %+v", ann)
	}
}

const tscMatcher = `{"problemMatcher":[{"owner":"tsc","pattern":[{"regexp":"^([^\\s].*)\\((\\d+|\\d+,\\d+|\\d+,\\d+,\\d+,\\d+)\\):\\s+(error|warning|info)\\s+(\\w{1,2}\\d+)\\s*:\\s*(.*)$","file":1,"location":2,"severity":3,"code":4,"message":5}]}]}`

const eslintMatcher = `{"problemMatcher":[{"owner":"eslint-stylish","pattern":[{"regexp":"^([^\\s].*)$","file":1},{"regexp":"^\\s+(\\d+):(\\d+)\\s+(error|warning|info)\\s+(.*)\\s\\s+(.*)$","line":1,"column":2,"severity":3,"message":4,"code":5,"loop":true}]}]}`

func TestProblemMatchers(t *testing.T) {
	files := map[string]string{"/w/tsc.json": tscMatcher, "/w/eslint.json": eslintMatcher}
	var out bytes.Buffer
	var ann []Annotation
	cw := &commandWriter{
		out: &out, matchers: newMatcherSet(), fileBase: "/w/repo",
		onAnnotation: func(a Annotation) { ann = append(ann, a) },
		readFile: func(p string) ([]byte, error) {
			s, ok := files[p]
			if !ok {
				return nil, errors.New("missing")
			}
			return []byte(s), nil
		},
	}
	_, _ = cw.Write([]byte(strings.Join([]string{
		"##[add-matcher]/w/tsc.json",
		"/w/repo/src/a.ts(3,7): error TS2322: Type 'x' is not assignable",
		"plain line",
		"::add-matcher::/w/eslint.json",
		"src/b.js",
		"  1:1  error  Missing semi  semi",
		"  2:3  warning  Unused var  no-unused",
		"src/c.js",
		"  4:4  error  Bad  rule",
		"::remove-matcher owner=eslint-stylish::",
		"src/d.js",
		"  5:5  error  Gone  rule",
		"::add-matcher::/w/nope.json",
	}, "\n") + "\n"))
	cw.Flush()
	text := out.String()
	if len(ann) != 4 {
		t.Fatalf("annotations = %+v", ann)
	}
	if a := ann[0]; a.File != "src/a.ts" || a.Line != 3 || a.Col != 7 || a.Level != "error" || a.Title != "TS2322" || a.Message != "Type 'x' is not assignable" {
		t.Errorf("tsc annotation = %+v", a)
	}
	if a := ann[1]; a.File != "src/b.js" || a.Line != 1 || a.Col != 1 || a.Level != "error" || a.Message != "Missing semi" || a.Title != "semi" {
		t.Errorf("eslint first = %+v", a)
	}
	if a := ann[2]; a.File != "src/b.js" || a.Line != 2 || a.Level != "warning" {
		t.Errorf("eslint loop = %+v", a)
	}
	if a := ann[3]; a.File != "src/c.js" || a.Line != 4 {
		t.Errorf("eslint next file = %+v", a)
	}
	for _, want := range []string{
		"##[error]/w/repo/src/a.ts(3,7): error TS2322",
		"Added matchers: \"tsc\".",
		"Added matchers: \"eslint-stylish\".",
		"Removed matchers: \"eslint-stylish\"\n",
		"\n  5:5  error  Gone  rule\n",
		"##[warning]Unable to process matcher file /w/nope.json",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	if _, err := parseMatchers([]byte(`{"problemMatcher":[{"owner":"x","pattern":[{"regexp":"^(a)$","file":1}]}]}`)); err == nil {
		t.Error("a matcher without a message group must be rejected")
	}
	if _, err := parseMatchers([]byte(`{"problemMatcher":[{"owner":"x","pattern":[{"regexp":"^(a)$","message":1,"loop":true}]}]}`)); err == nil {
		t.Error("loop on a single pattern must be rejected")
	}
}

func TestStepExprFields(t *testing.T) {
	ectx := &exprContext{matrix: map[string]any{"experimental": true, "t": float64(7)}}
	st := stepSpec{ContinueOnErrorExpr: "${{ matrix.experimental }}", TimeoutMinutesExpr: "${{ matrix.t }}"}
	if coe, err := st.continueOnError(ectx); err != nil || !coe {
		t.Errorf("continue-on-error = %v, %v", coe, err)
	}
	if limit, err := st.timeoutMinutes(ectx); err != nil || limit != 7 {
		t.Errorf("timeout = %v, %v", limit, err)
	}
	if _, err := (stepSpec{TimeoutMinutesExpr: "${{ 'soon' }}"}).timeoutMinutes(ectx); err == nil {
		t.Error("a non numeric timeout must fail")
	}
	lit := stepSpec{ContinueOnError: true, TimeoutMinutes: 3}
	if coe, _ := lit.continueOnError(ectx); !coe {
		t.Error("literal continue-on-error lost")
	}
	if limit, _ := lit.timeoutMinutes(ectx); limit != 3 {
		t.Error("literal timeout lost")
	}
	if coe, e := boolOrExpr("${{ inputs.x }}"); coe || e != "${{ inputs.x }}" {
		t.Errorf("boolOrExpr expr = %v %q", coe, e)
	}
	if coe, e := boolOrExpr(true); !coe || e != "" {
		t.Errorf("boolOrExpr bool = %v %q", coe, e)
	}
	if coe, e := boolOrExpr("TRUE"); !coe || e != "" {
		t.Errorf("boolOrExpr string = %v %q", coe, e)
	}
}

func TestInputsEnvOnlyForNodeAndDocker(t *testing.T) {
	j := &job{task: &v1.Task{}}
	load := func(src string) *actionDef {
		act := &actionDef{ref: "a/b@v1", repo: "a/b", version: "v1"}
		if err := yaml.Unmarshal([]byte(src), &act.meta); err != nil {
			t.Fatal(err)
		}
		return act
	}
	node := load("inputs:\n  node-version: {}\nruns:\n  using: node20\n  main: index.js\n")
	composite := load("inputs:\n  node-version: {}\nruns:\n  using: composite\n  steps: []\n")
	with := map[string]string{"node-version": "24"}
	call, err := j.prepareCall(node, with, map[string]string{"A": "1"}, &exprContext{}, "0", "node", io.Discard)
	if err != nil || call.env["INPUT_NODE-VERSION"] != "24" || call.env["A"] != "1" {
		t.Errorf("node action env = %v, %v", call.env, err)
	}
	call, err = j.prepareCall(composite, with, map[string]string{}, &exprContext{}, "1", "composite", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := call.env["INPUT_NODE-VERSION"]; ok {
		t.Error("composite actions must not export INPUT_ variables")
	}
	if call.inputs["node-version"] != "24" {
		t.Errorf("composite inputs = %v", call.inputs)
	}
}

// Real docker exec, env intact and cancel kills the tree
func TestDockerExecEnvAndCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("docker")
	}
	cli, err := newDockerClient("")
	if err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skip("docker daemon unreachable")
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o777)
	ex := &dockerExecutor{cli: cli, jobID: uuid.NewString(), jobDir: dir, image: "docker.io/library/debian:bookworm-slim", log: newLogStream(nil, "job"), labels: map[string]string{labelJob: "test"}}
	if err := ex.Start(ctx); err != nil {
		t.Skipf("start: %v", err)
	}
	defer func() { _ = ex.Close(ctx) }()
	var out bytes.Buffer
	code, err := ex.Exec(ctx, execSpec{Cmd: []string{"env"}, Env: []string{"INPUT_CACHE-DEPENDENCY-PATH=web/package-lock.json"}, Path: []string{"/opt/tool/bin"}, Out: &out})
	if err != nil || code != 0 {
		t.Fatalf("env: %d %v %s", code, err, out.String())
	}
	if !strings.Contains(out.String(), "INPUT_CACHE-DEPENDENCY-PATH=web/package-lock.json\n") {
		t.Errorf("hyphenated env lost:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "PATH=/opt/tool/bin:/usr/local/sbin:") {
		t.Errorf("GITHUB_PATH prepend missing:\n%s", out.String())
	}
	if which := ex.Which(ctx, "sh"); which != "/bin/sh" && which != "/usr/bin/sh" {
		t.Errorf("which sh = %q", which)
	}
	cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := ex.Exec(cctx, execSpec{Cmd: []string{"sh", "-c", "sleep 300 & wait"}, Out: io.Discard}); err == nil {
		t.Fatal("cancelled exec must report the context error")
	}
	if time.Since(started) > 20*time.Second {
		t.Fatalf("cancel took %s", time.Since(started))
	}
	out.Reset()
	_, _ = ex.Exec(ctx, execSpec{Cmd: []string{"sh", "-c", `n=0; for f in /proc/[0-9]*/cmdline; do c=$(tr '\0' ' ' < "$f" 2>/dev/null); case "$c" in "sleep 300 "*) n=$((n+1));; esac; done; echo $n`}, Out: &out})
	if strings.TrimSpace(out.String()) != "0" {
		t.Errorf("sleep survived the cancel: %q", out.String())
	}
}
