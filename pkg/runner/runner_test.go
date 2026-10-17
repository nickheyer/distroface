package runner

import (
	"bytes"
	"strings"
	"testing"
)

func TestEvalCondition(t *testing.T) {
	c := &exprContext{
		github: map[string]any{"ref": "refs/heads/main", "event_name": "push"},
		env:    map[string]string{"FOO": "bar"},
		steps:  map[string]any{"build": map[string]any{"outcome": "failure", "outputs": map[string]any{"v": "1.2"}}},
		status: "failure",
	}
	cases := map[string]bool{
		"":                                      false,
		"success()":                             false,
		"failure()":                             true,
		"always()":                              true,
		"cancelled()":                           false,
		"steps.build.outcome == 'failure'":      true,
		"steps.build.outputs.v == 1.2":          true,
		"github.ref == 'refs/heads/main'":       true,
		"startsWith(github.ref, 'refs/heads/')": true,
		"env.FOO == 'BAR' && !cancelled()":      true,
		"contains('abc', 'z') || false":         false,
		"(1 < 2) && (steps.missing.outcome == null)": true,
	}
	for src, want := range cases {
		got, err := evalCondition(src, c)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if got != want {
			t.Errorf("%q: got %v want %v", src, got, want)
		}
	}
	if _, err := evalCondition("github.ref ==", c); err == nil {
		t.Error("expected parse error")
	}
}

func TestInterpolate(t *testing.T) {
	c := &exprContext{steps: map[string]any{"a": map[string]any{"outputs": map[string]any{"x": "hi"}}}, inputs: map[string]any{"name": "world"}}
	got := interpolate("say ${{ steps.a.outputs.x }} to ${{ inputs.name }} ${{ format('{0}!', 'now') }}", c)
	if got != "say hi to world now!" {
		t.Fatalf("got %q", got)
	}
	if got := interpolate("${{ broken(", c); got != "${{ broken(" {
		t.Fatalf("broken expression should stay verbatim, got %q", got)
	}
}

func TestCommandWriter(t *testing.T) {
	var out bytes.Buffer
	var ann []Annotation
	var masks []string
	outputs := map[string]string{}
	cw := &commandWriter{out: &out, onAnnotation: func(a Annotation) { ann = append(ann, a) }, onMask: func(s string) { masks = append(masks, s) }, onOutput: func(k, v string) { outputs[k] = v }}
	_, _ = cw.Write([]byte("::group::Build\nhello\n::add-mask::secret%0A\n::error file=a.go,line=3,title=Bad%3A thing::boom\n::set-output name=v::1\n::stop-commands::tok\n::error::ignored\n::tok::\n::endgroup::\npartial"))
	cw.Flush()
	text := out.String()
	for _, want := range []string{"##[group]Build\n", "hello\n", "##[error]boom\n", "::error::ignored\n", "##[endgroup]\n", "partial\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "add-mask") {
		t.Error("mask command leaked into the log")
	}
	if len(masks) != 1 || masks[0] != "secret\n" {
		t.Errorf("masks: %v", masks)
	}
	if len(ann) != 1 || ann[0].File != "a.go" || ann[0].Line != 3 || ann[0].Title != "Bad: thing" || ann[0].Level != "error" {
		t.Errorf("annotations: %+v", ann)
	}
	if outputs["v"] != "1" {
		t.Errorf("outputs: %v", outputs)
	}
}

func TestParseKeyValueFile(t *testing.T) {
	data := "a=1\nmulti<<EOF\nline1\nline2\nEOF\nb=x=y\n"
	got := parseKeyValueFile([]byte(data))
	want := [][2]string{{"a", "1"}, {"multi", "line1\nline2"}, {"b", "x=y"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestLogMasking(t *testing.T) {
	l := newLogStream(nil, "job")
	l.AddMask("hunter2")
	_, _ = l.Write([]byte("password is hun"))
	_, _ = l.Write([]byte("ter2 ok\ntail"))
	if got := string(l.take(false)); got != "password is *** ok\n" {
		t.Fatalf("got %q", got)
	}
	if got := string(l.take(true)); got != "tail" {
		t.Fatalf("partial got %q", got)
	}
}

func TestContainerOptions(t *testing.T) {
	o := parseContainerOptions(`--privileged -v /a:/b --user=root -e "X=1 2" --cpus 2 --add-host h:1.2.3.4`)
	if !o.privileged || o.user != "root" || len(o.volumes) != 1 || o.volumes[0] != "/a:/b" || len(o.env) != 1 || o.env[0] != "X=1 2" || len(o.hosts) != 1 {
		t.Fatalf("%+v", o)
	}
}

func TestImageFor(t *testing.T) {
	cfg := &Config{Images: map[string]string{"ubuntu-latest": "ubuntu:24.04", "host": "host"}}
	cfg.normalize()
	if img, host, ok := cfg.ImageFor([]string{"Ubuntu-Latest"}); !ok || host || img != "ubuntu:24.04" {
		t.Fatalf("%v %v %v", img, host, ok)
	}
	if _, _, ok := cfg.ImageFor([]string{"host"}); ok {
		t.Fatal("host must stay disabled by default")
	}
	cfg.HostEnabled = true
	if _, host, ok := cfg.ImageFor([]string{"host"}); !ok || !host {
		t.Fatal("host executor should resolve when enabled")
	}
}
