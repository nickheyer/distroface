package runner

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gitea.com/gitea/runner/act/model"
	shellquote "github.com/kballard/go-shellquote"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/sirupsen/logrus"
)

func testReporter(steps ...*model.Step) (*reporter, *logStream) {
	l := newLogStream(nil, "job1")
	j := &job{task: &v1.Task{JobId: "job1"}, log: l}
	r := newReporter(j)
	r.setSteps(steps)
	return r, l
}

func entry(level logrus.Level, msg string, fields logrus.Fields) *logrus.Entry {
	e := logrus.NewEntry(logrus.New())
	e.Level, e.Message, e.Data = level, msg, fields
	return e
}

func stepFields(stage string, number int, name string, extra logrus.Fields) logrus.Fields {
	f := logrus.Fields{"stage": stage, "stepNumber": number, "step": name, "stepID": []string{fmt.Sprint(number)}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func TestReporterBuildsTranscriptAndSteps(t *testing.T) {
	r, l := testReporter(&model.Step{Uses: "actions/checkout@v4"}, &model.Step{Name: "Build", Run: "make"})
	r.startSetup()
	_ = r.Fire(entry(logrus.InfoLevel, "pulling image", nil))
	_ = r.Fire(entry(logrus.InfoLevel, "Run Main actions/checkout@v4", stepFields("Main", 0, "actions/checkout@v4", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "cloning", stepFields("Main", 0, "actions/checkout@v4", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "Success - Main actions/checkout@v4", stepFields("Main", 0, "actions/checkout@v4", logrus.Fields{"stepResult": "success"})))
	_ = r.Fire(entry(logrus.InfoLevel, "Run Main Build", stepFields("Main", 1, "Build", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "::group::Compiling", stepFields("Main", 1, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "::error file=main.go,line=3::boom", stepFields("Main", 1, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "::endgroup::", stepFields("Main", 1, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "::add-mask::hunter2", stepFields("Main", 1, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "password is hunter2", stepFields("Main", 1, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.ErrorLevel, "##[error]process completed with exit code 1", stepFields("Main", 1, "Build", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "Failure - Main Build", stepFields("Main", 1, "Build", logrus.Fields{"stepResult": "failure"})))
	_ = r.Fire(entry(logrus.InfoLevel, "Job failed", logrus.Fields{"jobResult": "failure"}))
	r.finish(v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE)

	out := string(l.take(true))
	want := []string{
		"##[group]Set up job\n", "pulling image\n", "##[endgroup]\n##[group]Run actions/checkout@v4\n", "cloning\n",
		"##[group]Build\n", "##[group]Compiling\n", "##[error]boom\n", "##[endgroup]\n", "password is ***\n",
		"##[error]process completed with exit code 1\n", "##[group]Complete job\n",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("transcript lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("mask leaked:\n%s", out)
	}
	steps, _, ann := r.snapshot()
	if len(steps) != 4 || steps[0].Name != setupStepName || steps[1].Name != "Run actions/checkout@v4" || steps[2].Name != "Build" || steps[3].Name != completeStepName {
		t.Fatalf("steps = %+v", steps)
	}
	if steps[1].Conclusion != v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS || steps[2].Conclusion != v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE {
		t.Fatalf("conclusions = %v %v", steps[1].Conclusion, steps[2].Conclusion)
	}
	if !strings.Contains(ann, `"file":"main.go"`) || !strings.Contains(ann, `"line":3`) || !strings.Contains(ann, `"step":3`) || !strings.Contains(ann, "exit code 1") {
		t.Fatalf("annotations = %s", ann)
	}
	if r.result() != "failure" {
		t.Fatalf("result = %q", r.result())
	}
}

func TestReporterIgnoresCompositeInnerSteps(t *testing.T) {
	r, _ := testReporter(&model.Step{Name: "composite", Uses: "./composite"})
	r.startSetup()
	_ = r.Fire(entry(logrus.InfoLevel, "run", stepFields("Main", 0, "composite", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "inner", logrus.Fields{"stage": "Main", "stepNumber": 0, "step": "inner step", "stepID": []string{"0", "0"}}))
	_ = r.Fire(entry(logrus.InfoLevel, "inner done", logrus.Fields{"stage": "Main", "stepNumber": 0, "step": "inner step", "stepID": []string{"0", "0"}, "stepResult": "failure"}))
	steps, _, _ := r.snapshot()
	if len(steps) != 2 || steps[1].Name != "composite" || steps[1].Status != v1.ActionsStatus_ACTIONS_STATUS_RUNNING {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestReporterMasksAnnotations(t *testing.T) {
	r, l := testReporter(&model.Step{Name: "Leak", Run: "x"})
	l.AddMask("s3cretvalue")
	l.AddMask("jobtoken123")
	r.startSetup()
	_ = r.Fire(entry(logrus.InfoLevel, "::error title=token jobtoken123::secret is s3cretvalue", stepFields("Main", 0, "Leak", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.ErrorLevel, "##[error]auth with s3cretvalue failed", stepFields("Main", 0, "Leak", nil)))
	r.failSetup(fmt.Errorf("setup saw jobtoken123"))
	_, _, ann := r.snapshot()
	if strings.Contains(ann, "s3cretvalue") || strings.Contains(ann, "jobtoken123") {
		t.Fatalf("annotations leak: %s", ann)
	}
	for _, w := range []string{`"message":"secret is ***"`, `"title":"token ***"`, `"message":"auth with *** failed"`, `"message":"setup saw ***"`} {
		if !strings.Contains(ann, w) {
			t.Fatalf("annotations lack %s: %s", w, ann)
		}
	}
	if out := string(l.take(true)); strings.Contains(out, "s3cretvalue") || strings.Contains(out, "jobtoken123") {
		t.Fatalf("transcript leak: %s", out)
	}
}

func TestReporterNamesStepsLikeGitHub(t *testing.T) {
	script := "echo one\necho two\n"
	named := "Line one\nLine two"
	r, l := testReporter(&model.Step{Run: script}, &model.Step{Uses: "actions/setup-go@v5"}, &model.Step{Name: named, Run: "true"})
	r.startSetup()
	_ = r.Fire(entry(logrus.InfoLevel, "Run Main "+script, stepFields("Main", 0, script, nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "Success", stepFields("Main", 0, script, logrus.Fields{"stepResult": "success"})))
	_ = r.Fire(entry(logrus.InfoLevel, "out", stepFields("Pre", 1, "actions/setup-go@v5", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "Success", stepFields("Pre", 1, "actions/setup-go@v5", logrus.Fields{"stepResult": "success"})))
	_ = r.Fire(entry(logrus.InfoLevel, "Run Main", stepFields("Main", 1, "actions/setup-go@v5", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "Success", stepFields("Main", 1, "actions/setup-go@v5", logrus.Fields{"stepResult": "success"})))
	_ = r.Fire(entry(logrus.InfoLevel, "Run Main", stepFields("Main", 2, named, nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "Success", stepFields("Main", 2, named, logrus.Fields{"stepResult": "success"})))
	_ = r.Fire(entry(logrus.InfoLevel, "out", stepFields("Post", 1, "actions/setup-go@v5", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "Success", stepFields("Post", 1, "actions/setup-go@v5", logrus.Fields{"stepResult": "success"})))
	steps, _, _ := r.snapshot()
	want := []string{setupStepName, "Run echo one", "Pre Run actions/setup-go@v5", "Run actions/setup-go@v5", "Line one Line two", "Post Run actions/setup-go@v5"}
	if len(steps) != len(want) {
		t.Fatalf("steps = %+v", steps)
	}
	for i, w := range want {
		if steps[i].Name != w {
			t.Fatalf("step %d = %q, want %q", i, steps[i].Name, w)
		}
	}
	out := string(l.take(true))
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "##[group]") && !strings.Contains(out, line+"\n") {
			t.Fatalf("broken group marker %q", line)
		}
	}
	for _, w := range want {
		if !strings.Contains(out, "##[group]"+w+"\n") {
			t.Fatalf("transcript lacks group %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "##[group]Run echo one\necho two") || strings.Contains(out, "##[group]Line one\nLine two") {
		t.Fatalf("multi line group marker:\n%s", out)
	}
}

func TestReporterSkipsGhostPreAndPostRows(t *testing.T) {
	r, _ := testReporter(&model.Step{Uses: "actions/checkout@v4"}, &model.Step{Uses: "actions/cache@v4"})
	r.startSetup()
	// Clone progress under the Pre logger, no pre phase on this action
	_ = r.Fire(entry(logrus.InfoLevel, "git clone 'https://github.com/actions/checkout' # ref=v4", stepFields("Pre", 0, "actions/checkout@v4", nil)))
	_ = r.Fire(entry(logrus.DebugLevel, "  cloning https://github.com/actions/checkout to /cache", stepFields("Pre", 0, "actions/checkout@v4", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "git clone 'https://github.com/actions/cache' # ref=v4", stepFields("Pre", 1, "actions/cache@v4", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "Run Main actions/checkout@v4", stepFields("Main", 0, "actions/checkout@v4", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "Success", stepFields("Main", 0, "actions/checkout@v4", logrus.Fields{"stepResult": "success"})))
	// Main skipped by its if, act reports the skip at debug level
	_ = r.Fire(entry(logrus.DebugLevel, "Skipping step 'actions/cache@v4' due to 'false'", stepFields("Main", 1, "actions/cache@v4", logrus.Fields{"stepResult": "skipped"})))
	// Post of the skipped main is announced skipped at debug level
	_ = r.Fire(entry(logrus.DebugLevel, "skipping post step for 'actions/cache@v4'; main step was skipped", stepFields("Post", 1, "actions/cache@v4", logrus.Fields{"stepResult": "skipped"})))
	// Checkout really has a post, it prints and reports success
	_ = r.Fire(entry(logrus.InfoLevel, "Run Post actions/checkout@v4", stepFields("Post", 0, "actions/checkout@v4", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "Temporarily overriding HOME", stepFields("Post", 0, "actions/checkout@v4", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "Success - Post actions/checkout@v4", stepFields("Post", 0, "actions/checkout@v4", logrus.Fields{"stepResult": "success"})))
	r.finish(v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS)
	steps, _, _ := r.snapshot()
	want := []string{setupStepName, "Run actions/checkout@v4", "Run actions/cache@v4", "Post Run actions/checkout@v4", completeStepName}
	if len(steps) != len(want) {
		t.Fatalf("steps = %+v", steps)
	}
	for i, w := range want {
		if steps[i].Name != w {
			t.Fatalf("step %d = %q, want %q", i, steps[i].Name, w)
		}
	}
	if steps[2].Conclusion != v1.ActionsConclusion_ACTIONS_CONCLUSION_SKIPPED || steps[3].Conclusion != v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS {
		t.Fatalf("conclusions = %v %v", steps[2].Conclusion, steps[3].Conclusion)
	}
}

func TestReporterOpensPreRowOnFailure(t *testing.T) {
	r, _ := testReporter(&model.Step{Uses: "org/action@v1"})
	r.startSetup()
	_ = r.Fire(entry(logrus.ErrorLevel, "##[error]exit status 1", stepFields("Pre", 0, "org/action@v1", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "Failure - Pre org/action@v1", stepFields("Pre", 0, "org/action@v1", logrus.Fields{"stepResult": "failure"})))
	steps, _, ann := r.snapshot()
	if len(steps) != 2 || steps[1].Name != "Pre Run org/action@v1" || steps[1].Conclusion != v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE {
		t.Fatalf("steps = %+v", steps)
	}
	if !strings.Contains(ann, `"step":2`) {
		t.Fatalf("annotations = %s", ann)
	}
}

// Act's createContainerName, copied so the prefix contract is checked against the real rules
func actContainerName(parts ...string) string {
	name := strings.Join(parts, "-")
	name = regexp.MustCompile("[^a-zA-Z0-9]").ReplaceAllString(name, "-")
	name = strings.ReplaceAll(name, "--", "-")
	hash := sha256.Sum256([]byte(name))
	if len(name) > 63 {
		name = name[:63]
	}
	return fmt.Sprintf("%s-%x", strings.Trim(name, "-"), hash)
}

func TestContainerPrefixMatchesActNames(t *testing.T) {
	for _, name := range []string{"Nicks-MacBook.local", "ci--box_01", "a---b", "build.1.example.org", "runner"} {
		r := &Runner{cfg: &Config{Name: name}}
		j := newJob(r, &v1.Task{JobId: "0123456789abcdef"})
		container := actContainerName(j.containerPrefix(), "WORKFLOW-Build and test", "JOB-unit (ubuntu, 1.22)")
		if !strings.HasPrefix(container, j.containerPrefix()) {
			t.Fatalf("%s: container %q lacks job prefix %q", name, container, j.containerPrefix())
		}
		if !strings.HasPrefix(container, r.containerPrefix()) {
			t.Fatalf("%s: container %q lacks runner prefix %q", name, container, r.containerPrefix())
		}
		service := actContainerName(container, "postgres")
		network := container + "-unit-network"
		for _, n := range []string{service, network, container + "-env"} {
			if !strings.HasPrefix(n, r.containerPrefix()) {
				t.Fatalf("%s: %q lacks runner prefix %q", name, n, r.containerPrefix())
			}
		}
		if strings.Contains(j.containerPrefix(), ".") || strings.Contains(j.containerPrefix(), "--") {
			t.Fatalf("%s: prefix %q is not act stable", name, j.containerPrefix())
		}
	}
}

func TestContainerBindsSurviveSpaces(t *testing.T) {
	r := &Runner{cfg: &Config{Name: "ci", WorkDir: "/tmp/work dir", Images: DefaultImages(), ContainerOptions: "--add-host a:1.2.3.4"}}
	j := newJob(r, &v1.Task{JobId: "0123456789abcdef", Context: "{}", RunsOn: []string{DefaultLabel}})
	j.session = newSession(j)
	cfg, err := j.actConfig(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	args, err := shellquote.Split(cfg.ContainerOptions)
	if err != nil {
		t.Fatalf("split %q: %v", cfg.ContainerOptions, err)
	}
	want := []string{"--add-host", "a:1.2.3.4", "-v", "/tmp/work dir/job-ci-01234567/docker:/__df/docker", "-v", "/tmp/work dir/job-ci-01234567/session:/__df/session"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("options = %q", args)
	}
	if cfg.ForcePull {
		t.Fatal("act must not pull again, images are pre pulled")
	}
}

func TestParseEnvDump(t *testing.T) {
	env := parseEnvDump([]byte("PATH=/usr/bin\x00MULTI=a\nb\x00GITHUB_WORKSPACE=/w\x00"))
	if env["PATH"] != "/usr/bin" || env["MULTI"] != "a\nb" || env["GITHUB_WORKSPACE"] != "/w" {
		t.Fatalf("nul separated = %v", env)
	}
	env = parseEnvDump([]byte("PATH=/usr/bin\nMULTI=a\nb\nGITHUB_WORKSPACE=/w\n"))
	if env["PATH"] != "/usr/bin" || env["MULTI"] != "a\nb" || env["GITHUB_WORKSPACE"] != "/w" {
		t.Fatalf("newline separated = %v", env)
	}
}

func TestHeadTailKeepsBothEnds(t *testing.T) {
	b := &headTail{keep: 16}
	for i := 0; i < 10; i++ {
		_, _ = b.Write([]byte("line-" + strings.Repeat("x", 5) + "\n"))
	}
	out := b.String()
	if !strings.HasPrefix(out, "line-xxxxx\n") || !strings.Contains(out, "elided") || !strings.HasSuffix(out, "line-xxxxx\n") {
		t.Fatalf("out = %q", out)
	}
}

func TestReporterSwallowsActStepHeader(t *testing.T) {
	r, l := testReporter(&model.Step{Uses: "actions/setup-go@v5"})
	r.startSetup()
	_ = r.Fire(entry(logrus.InfoLevel, "::group::Starting job container", logrus.Fields{"raw_output": true}))
	_ = r.Fire(entry(logrus.InfoLevel, "image: ubuntu", logrus.Fields{"raw_output": true}))
	_ = r.Fire(entry(logrus.InfoLevel, "::endgroup::", logrus.Fields{"raw_output": true}))
	_ = r.Fire(entry(logrus.InfoLevel, "Run Main actions/setup-go@v5", stepFields("Main", 0, "actions/setup-go@v5", nil)))
	raw := func(s string) {
		_ = r.Fire(entry(logrus.InfoLevel, s, stepFields("Main", 0, "actions/setup-go@v5", logrus.Fields{"raw_output": true})))
	}
	raw("::group::Run actions/setup-go@v5")
	raw("with:")
	raw("  go-version: 1.20")
	raw("::group::Download go")
	raw("fetching")
	raw("::endgroup::")
	raw("::error::Unable to find Go version '1.20'")
	raw("::endgroup::")
	_ = r.Fire(entry(logrus.InfoLevel, "Failure - Main actions/setup-go@v5", stepFields("Main", 0, "actions/setup-go@v5", logrus.Fields{"stepResult": "failure"})))
	_ = r.Fire(entry(logrus.ErrorLevel, "##[error]exit with `FAILURE`: 1", stepFields("Main", 0, "actions/setup-go@v5", nil)))
	r.finish(v1.ActionsConclusion_ACTIONS_CONCLUSION_FAILURE)
	out := string(l.take(true))
	want := strings.Join([]string{
		"##[group]Set up job",
		"##[group]Starting job container",
		"image: ubuntu",
		"##[endgroup]",
		"##[endgroup]",
		"##[group]Run actions/setup-go@v5",
		"with:",
		"  go-version: 1.20",
		"##[group]Download go",
		"fetching",
		"##[endgroup]",
		"##[error]Unable to find Go version '1.20'",
		"##[error]exit with `FAILURE`: 1",
		"##[endgroup]",
		"##[group]Complete job",
		"Cleaning up the job environment",
		"##[endgroup]",
		"",
	}, "\n")
	if out != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", out, want)
	}
	_, _, ann := r.snapshot()
	if strings.Count(ann, `"step":2`) != 2 || strings.Contains(ann, `"step":0`) {
		t.Fatalf("annotations = %s", ann)
	}
}

func TestReporterClosesStrayRawGroups(t *testing.T) {
	r, l := testReporter(&model.Step{Name: "Build", Run: "make"}, &model.Step{Uses: "actions/checkout@v4"})
	r.startSetup()
	_ = r.Fire(entry(logrus.InfoLevel, "Run Main Build", stepFields("Main", 0, "Build", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "::group::Run make", stepFields("Main", 0, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "make", stepFields("Main", 0, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "::endgroup::", stepFields("Main", 0, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "::group::Left open", stepFields("Main", 0, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "::endgroup::", stepFields("Main", 0, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "::endgroup::", stepFields("Main", 0, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "::group::Never closed", stepFields("Main", 0, "Build", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "Success", stepFields("Main", 0, "Build", logrus.Fields{"stepResult": "success"})))
	_ = r.Fire(entry(logrus.InfoLevel, "Run Post actions/checkout@v4", stepFields("Post", 1, "actions/checkout@v4", nil)))
	_ = r.Fire(entry(logrus.InfoLevel, "cleanup", stepFields("Post", 1, "actions/checkout@v4", logrus.Fields{"raw_output": true})))
	_ = r.Fire(entry(logrus.InfoLevel, "Success", stepFields("Post", 1, "actions/checkout@v4", logrus.Fields{"stepResult": "success"})))
	r.finish(v1.ActionsConclusion_ACTIONS_CONCLUSION_SUCCESS)
	out := string(l.take(true))
	depth := 0
	for i, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "##[group]"):
			depth++
		case line == "##[endgroup]":
			depth--
		}
		if depth < 0 {
			t.Fatalf("group stack underflows at line %d:\n%s", i+1, out)
		}
	}
	if depth != 0 {
		t.Fatalf("groups left open: %d\n%s", depth, out)
	}
	if !strings.Contains(out, "##[group]Build\nmake\n##[group]Left open\n##[endgroup]\n##[group]Never closed\n##[endgroup]\n##[endgroup]\n##[group]Post Run actions/checkout@v4\n") {
		t.Fatalf("transcript:\n%s", out)
	}
}
