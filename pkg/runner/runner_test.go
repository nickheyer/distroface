package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

func TestEvalCondition(t *testing.T) {
	c := &exprContext{
		github: map[string]any{"ref": "refs/heads/main", "event_name": "push"},
		env:    map[string]string{"FOO": "bar"},
		steps:  map[string]any{"build": map[string]any{"outcome": "failure", "outputs": map[string]any{"v": "1.2"}}},
		status: "failure",
	}
	// A failed job implies success() unless the condition names a status function
	failed := map[string]bool{
		"":                                 false,
		"success()":                        false,
		"failure()":                        true,
		"always()":                         true,
		"cancelled()":                      false,
		"steps.build.outcome == 'failure'": false,
		"failure() && steps.build.outcome == 'failure'": true,
		"github.ref == 'refs/heads/main'":               false,
		"always() && github.ref == 'refs/heads/main'":   true,
		"env.FOO == 'BAR' && !cancelled()":              true,
		"contains('abc', 'z') || false":                 false,
	}
	for src, want := range failed {
		got, err := evalCondition(src, c)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if got != want {
			t.Errorf("%q after failure: got %v want %v", src, got, want)
		}
	}
	c.status = ""
	healthy := map[string]bool{
		"":                                           true,
		"success()":                                  true,
		"failure()":                                  false,
		"steps.build.outcome == 'failure'":           true,
		"steps.build.outputs.v == 1.2":               true,
		"github.ref == 'refs/heads/main'":            true,
		"startsWith(github.ref, 'refs/heads/')":      true,
		"env.FOO == 'BAR' && !cancelled()":           true,
		"contains('abc', 'z') || false":              false,
		"(1 < 2) && (steps.missing.outcome == null)": true,
	}
	for src, want := range healthy {
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
	got, err := interpolate("say ${{ steps.a.outputs.x }} to ${{ inputs.name }} ${{ format('{0}!', 'now') }}", c)
	if err != nil || got != "say hi to world now!" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := interpolate("${{ nosuch.thing }}", c); err == nil {
		t.Error("unknown contexts must fail")
	}
	if _, err := interpolate("${{ broken(", c); err == nil {
		t.Error("unclosed expressions must fail")
	}
	if _, err := interpolate("${{ frobnicate('x') }}", c); err == nil {
		t.Error("unknown functions must fail")
	}
}

func TestCommandWriter(t *testing.T) {
	var out bytes.Buffer
	var ann []Annotation
	var masks []string
	outputs := map[string]string{}
	state := map[string]string{}
	cw := &commandWriter{out: &out, onAnnotation: func(a Annotation) { ann = append(ann, a) }, onMask: func(s string) { masks = append(masks, s) }, onOutput: func(k, v string) { outputs[k] = v }, onState: func(k, v string) { state[k] = v }}
	_, _ = cw.Write([]byte("::group::Build\nhello\n::add-mask::secret%0A\n::error file=a.go,line=3,title=Bad%3A thing::boom\n::set-output name=v::1\n::save-state name=isPost::true\n::stop-commands::tok\n::error::ignored\n::tok::\n::endgroup::\npartial"))
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
	if state["isPost"] != "true" || strings.Contains(text, "save-state") {
		t.Errorf("state: %v", state)
	}
}

func TestCommandEcho(t *testing.T) {
	var out bytes.Buffer
	cw := &commandWriter{out: &out}
	_, _ = cw.Write([]byte("::echo::on\n::group::Build\n::add-mask::s\n::echo::off\n::endgroup::\n"))
	cw.Flush()
	text := out.String()
	if !strings.Contains(text, "::group::Build\n##[group]Build\n") || !strings.Contains(text, "##[endgroup]\n") {
		t.Errorf("echo on must print the command before acting on it: %q", text)
	}
	for _, unwanted := range []string{"::echo::", "::add-mask::", "::endgroup::"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("%q leaked into %q", unwanted, text)
		}
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

func TestGatewayHosts(t *testing.T) {
	hosts, unreachable := gatewayHosts("http://localhost:8080", "http://127.0.0.1:8080/nick/x.git", "https://[::1]:9443", "https://10.0.0.5:8080", "")
	if len(hosts) != 0 {
		t.Errorf("literal loopback addresses cannot map to the gateway: %v", hosts)
	}
	if strings.Join(unreachable, ",") != "localhost,127.0.0.1,::1" {
		t.Errorf("unreachable = %v", unreachable)
	}
}

func TestMultiLineMask(t *testing.T) {
	l := newLogStream(nil, "job")
	l.AddMask("-----BEGIN KEY-----\nabcdefgh\n-----END KEY-----")
	_, _ = l.Write([]byte("line abcdefgh here\n"))
	if got := string(l.take(false)); got != "line *** here\n" {
		t.Fatalf("got %q", got)
	}
}

func TestShellCommand(t *testing.T) {
	cmd, ext := shellCommand("", true, true)
	if strings.Join(cmd, " ") != "bash -e {0}" || ext != ".sh" {
		t.Errorf("default bash: %v %s", cmd, ext)
	}
	if cmd, _ := shellCommand("", false, true); strings.Join(cmd, " ") != "sh -e {0}" {
		t.Errorf("default sh: %v", cmd)
	}
	if cmd, _ := shellCommand("bash", true, true); strings.Join(cmd, " ") != "bash --noprofile --norc -e -o pipefail {0}" {
		t.Errorf("bash: %v", cmd)
	}
	if cmd, ext := shellCommand("python", true, false); strings.Join(cmd, " ") != "python {0}" || ext != ".py" {
		t.Errorf("python: %v %s", cmd, ext)
	}
	if cmd, _ := shellCommand("perl -w {0}", true, true); strings.Join(cmd, " ") != "perl -w {0}" {
		t.Errorf("custom: %v", cmd)
	}
	if cmd, _ := shellCommand("deno run", true, true); strings.Join(cmd, " ") != "deno run {0}" {
		t.Errorf("custom without placeholder: %v", cmd)
	}
}

func TestRefSpecs(t *testing.T) {
	if got := refSpecs("v1.2", ""); strings.Join(got, ",") != "+refs/heads/v1.2*:refs/remotes/origin/v1.2*,+refs/tags/v1.2*:refs/tags/v1.2*" {
		t.Errorf("got %v", got)
	}
	if got := refSpecs("refs/pull/3/merge", ""); strings.Join(got, ",") != "+refs/pull/3/merge:refs/remotes/pull/3/merge" {
		t.Errorf("got %v", got)
	}
	sha := strings.Repeat("a", 40)
	if got := refSpecs("refs/heads/main", sha); strings.Join(got, ",") != "+"+sha+":refs/remotes/origin/main" {
		t.Errorf("got %v", got)
	}
	if got := refSpecs("", ""); strings.Join(got, ",") != "HEAD" {
		t.Errorf("got %v", got)
	}
	if got := gitAuthKey("http://host:8080/org/app.git"); got != "http.http://host:8080/.extraheader" {
		t.Errorf("auth key = %q", got)
	}
	if got := checkoutLines("refs/heads/main", sha); got != "git checkout -q --force -B 'main' 'refs/remotes/origin/main'\n" {
		t.Errorf("got %q", got)
	}
}

func TestArtifactSelector(t *testing.T) {
	named := artifactSelector{name: "dfcli-dist"}
	if !named.matches("dfcli-dist") || named.matches("other") {
		t.Error("named must match exactly one artifact")
	}
	if named.dir("dfcli-dist") != "" {
		t.Error("named downloads unpack into the destination itself")
	}
	pattern := artifactSelector{pattern: "digest-*"}
	if !pattern.matches("digest-linux-amd64") || pattern.matches("binary-linux") {
		t.Error("pattern must filter")
	}
	if pattern.dir("digest-linux-amd64") != "digest-linux-amd64" {
		t.Error("pattern downloads keep one directory per artifact")
	}
	merged := artifactSelector{pattern: "binary-*", merge: true}
	if merged.dir("binary-linux-amd64") != "" {
		t.Error("merge-multiple flattens into the destination")
	}
	all := artifactSelector{}
	if !all.matches("proto-gen") || all.dir("proto-gen") != "proto-gen" {
		t.Error("no inputs downloads everything into named directories")
	}
}

// Packs files relative to their common root and unpacks them intact
func TestArtifactZipRoundTrip(t *testing.T) {
	src := t.TempDir()
	write := func(rel, body string, mode os.FileMode) string {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	files := []string{
		write("dist/bin/tool", "#!/bin/sh\n", 0o755),
		write("dist/index.html", "<html>", 0o644),
		write("dist/sub/a.txt", "a", 0o644),
	}
	j := &job{}
	var buf bytes.Buffer
	total, err := j.writeArtifactZip(&buf, commonDir(files), files, 0)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if total != int64(len("#!/bin/sh\n")+len("<html>")+1) {
		t.Errorf("total %d", total)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "bin/tool,index.html,sub/a.txt" {
		t.Errorf("entries %v", names)
	}
	dest := t.TempDir()
	n, err := j.extractArtifactZip(zr, dest, "")
	if err != nil || n != 3 {
		t.Fatalf("extract: %d %v", n, err)
	}
	if body, _ := os.ReadFile(filepath.Join(dest, "sub", "a.txt")); string(body) != "a" {
		t.Errorf("sub/a.txt = %q", body)
	}
	if info, err := os.Stat(filepath.Join(dest, "bin", "tool")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("exec bit lost: %v %v", info, err)
	}
}

// Pre zip uploads arrive nested under version and name, and escapes are clipped
func TestExtractArtifactZipLegacyAndEscapes(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"actions-3/frontend-build/index.html": "legacy",
		"../../etc/evil":                      "nope",
		"/abs/path.txt":                       "abs",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	j := &job{}
	if n, err := j.extractArtifactZip(zr, dest, "actions-3/frontend-build/"); err != nil || n != 3 {
		t.Fatalf("extract: %d %v", n, err)
	}
	for _, want := range []string{"index.html", filepath.Join("etc", "evil"), filepath.Join("abs", "path.txt")} {
		if _, err := os.Stat(filepath.Join(dest, want)); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dest)), "etc", "evil")); err == nil {
		t.Error("entry escaped the destination")
	}
}

func TestDropHidden(t *testing.T) {
	files := []string{"/w/dist/a", "/w/dist/.cache/b", "/w/dist/sub/.hidden", "/w/dist/sub/c"}
	if got := dropHidden(files); strings.Join(got, ",") != "/w/dist/a,/w/dist/sub/c" {
		t.Errorf("got %v", got)
	}
}

func TestActionEnvName(t *testing.T) {
	if got := actionEnvName(stepSpec{ID: "build"}); got != "build" {
		t.Errorf("got %q", got)
	}
	if got := actionEnvName(stepSpec{Uses: "actions/checkout@v4"}); got != "__actions_checkout" {
		t.Errorf("got %q", got)
	}
	if got := actionEnvName(stepSpec{Run: "echo"}); got != "__run" {
		t.Errorf("got %q", got)
	}
}

func TestStepRows(t *testing.T) {
	r := &Runner{cfg: &Config{Name: "r"}}
	j := newJob(r, &v1.Task{Steps: []*v1.TaskStep{{Number: 1, Name: "Checkout"}, {Number: 2, Name: "Build"}}, Context: "{}"})
	if len(j.steps) != 3 || j.steps[0].Name != setupStepName || j.steps[j.taskRow(1)].Name != "Build" || j.steps[j.taskRow(1)].Number != 3 {
		t.Fatalf("rows = %v", j.steps)
	}
	if strings.Join(j.stepActions, ",") != "__run,__run_2" {
		t.Errorf("step actions = %v", j.stepActions)
	}
	row := j.addStep("Post Checkout")
	if j.rowNumber(row) != 4 {
		t.Errorf("post row number = %d", j.rowNumber(row))
	}
	j.nameStep(j.taskRow(0), "Checkout code")
	if j.steps[1].Name != "Checkout code" {
		t.Error("interpolated names must replace the raw step name")
	}
	// Pre rows slot in after setup and shift every later row
	j.preSteps = append(j.preSteps, preStep{index: 1, row: 1})
	j.insertRows(1, []string{"Pre Build"})
	if j.steps[1].Name != "Pre Build" || j.steps[1].Number != 2 || j.steps[j.taskRow(1)].Name != "Build" || j.rowNumber(j.taskRow(1)) != 4 || j.rowNumber(row+1) != 5 {
		t.Errorf("rows after insert = %v", j.steps)
	}
	if s, _ := j.exprContext().Lookup("strategy"); stringify(s.(map[string]any)["job-total"]) != "1" {
		t.Errorf("strategy context = %v", s)
	}
}

func TestDockerLoginPost(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	j := &job{dir: dir, root: root, tempDir: filepath.Join(dir, "_temp"), task: &v1.Task{ServerUrl: "http://registry.test"}, log: newLogStream(nil, "job"), dockerAuths: map[string]dockerAuth{}}
	var out bytes.Buffer
	if err := j.dockerLogin("Login", map[string]string{"registry": "ghcr.io", "username": "u", "password": "p"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := j.dockerLogin("Keep", map[string]string{"registry": "quay.io", "username": "u", "password": "p", "logout": "false"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(j.post) != 1 || j.post[0].name != "Login" {
		t.Fatalf("post steps = %+v", j.post)
	}
	if err := j.post[0].run(context.Background(), &commandWriter{out: &out}); err != nil {
		t.Fatal(err)
	}
	if _, ok := j.dockerAuths["ghcr.io"]; ok {
		t.Error("the post step must drop the login")
	}
	if _, ok := j.dockerAuths["quay.io"]; !ok {
		t.Error("logout false must keep the login")
	}
}

func TestKillTreeScriptParses(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	if out, err := exec.Command(sh, "-n", "-c", killTreeScript).CombinedOutput(); err != nil {
		t.Fatalf("kill script does not parse: %v %s", err, out)
	}
}

func TestContainerHealthOptions(t *testing.T) {
	o := parseContainerOptions(`--health-cmd "pg_isready -U postgres" --health-interval 10s --health-timeout 5s --health-retries 5 --health-start-period 2s`)
	if o.health == nil || len(o.health.Test) != 2 || o.health.Test[1] != "pg_isready -U postgres" || o.health.Retries != 5 || o.health.Interval.Seconds() != 10 {
		t.Fatalf("%+v", o.health)
	}
}

func TestSafeName(t *testing.T) {
	if got := safeName("My Runner #1"); got != "my-runner--1" {
		t.Errorf("got %q", got)
	}
	if got := safeName(""); got != "runner" {
		t.Errorf("got %q", got)
	}
}

func TestHostURL(t *testing.T) {
	r := &Runner{cfg: &Config{Server: "http://127.0.0.1:18095"}}
	j := &job{r: r, task: &v1.Task{ServerUrl: "http://host.docker.internal:18095", CloneUrl: "http://host.docker.internal:18095/operator/app.git"}}
	if got := j.hostURL("http://host.docker.internal:18095/operator/app.git"); got != "http://127.0.0.1:18095/operator/app.git" {
		t.Errorf("instance url not rewritten: %q", got)
	}
	if got := j.hostURL("https://github.com/actions/checkout.git"); got != "https://github.com/actions/checkout.git" {
		t.Errorf("foreign url changed: %q", got)
	}
}

func TestJobRootRefusesEscape(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	j := &job{dir: dir, root: root, tempDir: filepath.Join(dir, "_temp"), confined: true}
	if err := j.mkdirAll(j.tempDir); err != nil {
		t.Fatal(err)
	}
	// A step plants a symlink out of the job dir
	if err := os.Symlink(outside, filepath.Join(j.tempDir, "esc")); err != nil {
		t.Fatal(err)
	}
	if err := j.writeFile(filepath.Join(j.tempDir, "esc", "authorized_keys"), []byte("x"), 0o644); err == nil {
		t.Fatal("write through an escaping symlink must fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "authorized_keys")); err == nil {
		t.Fatal("file landed outside the job directory")
	}
	if err := j.mkdirAll(filepath.Join(j.tempDir, "esc", "sub")); err == nil {
		t.Fatal("mkdir through an escaping symlink must fail")
	}
	if _, err := j.readFile(filepath.Join(j.tempDir, "esc", "secret")); err == nil {
		t.Fatal("read through an escaping symlink must fail")
	}
	if err := j.writeFile(filepath.Join(dir, "..", "x"), nil, 0o644); err == nil {
		t.Fatal("paths above the job dir must be refused")
	}
	if err := j.writeFile(filepath.Join(j.tempDir, "ok"), []byte("y"), 0o644); err != nil {
		t.Fatalf("plain write failed: %v", err)
	}
	if _, err := j.openUpload(filepath.Join(j.tempDir, "esc", "secret")); err == nil {
		t.Fatal("upload must not read through an escaping symlink")
	}
}

// Sequential replacement the masker used before the single pass
func sequentialMask(masks []string, s string) string {
	for _, m := range masks {
		s = strings.ReplaceAll(s, m, "***")
	}
	return s
}

func TestMaskPrefixSecrets(t *testing.T) {
	line := "token secret and secretlong and sec end\n"

	// Longer secret first, the single pass matches the old sequential output
	l := newLogStream(nil, "job")
	l.AddMask("secretlong")
	l.AddMask("secret")
	_, _ = l.Write([]byte(line))
	got := string(l.take(false))
	want := sequentialMask([]string{"secretlong", "secret"}, line)
	if got != want || got != "token *** and *** and sec end\n" {
		t.Fatalf("long first: got %q, sequential %q", got, want)
	}

	// Shorter secret first used to leak the tail, the longest mask now wins
	l = newLogStream(nil, "job")
	l.AddMask("secret")
	l.AddMask("secretlong")
	_, _ = l.Write([]byte(line))
	got = string(l.take(false))
	if old := sequentialMask([]string{"secret", "secretlong"}, line); old != "token *** and ***long and sec end\n" {
		t.Fatalf("sequential reference changed: %q", old)
	}
	if got != "token *** and *** and sec end\n" {
		t.Fatalf("short first: got %q", got)
	}

	// Later masks still apply to lines written after the replacer was built
	l.AddMask("sec end")
	_, _ = l.Write([]byte(line))
	if got := string(l.take(false)); got != "token *** and *** and ***\n" {
		t.Fatalf("after rebuild: got %q", got)
	}
}

func TestReportKey(t *testing.T) {
	a := []*v1.JobStep{{Number: 1, Name: "Set up job", Status: v1.ActionsStatus_ACTIONS_STATUS_COMPLETED}}
	b := []*v1.JobStep{{Number: 1, Name: "Set up job", Status: v1.ActionsStatus_ACTIONS_STATUS_COMPLETED}}
	if reportKey(a, "[]") != reportKey(b, "[]") {
		t.Fatal("equal snapshots must share a key")
	}
	b[0].Status = v1.ActionsStatus_ACTIONS_STATUS_RUNNING
	if reportKey(a, "[]") == reportKey(b, "[]") {
		t.Fatal("a status change must change the key")
	}
	if reportKey(a, "[]") == reportKey(a, `[{"level":"error"}]`) {
		t.Fatal("new annotations must change the key")
	}
	if reportKey(nil, "") == reportKey(a, "") {
		t.Fatal("an empty table must differ from a filled one")
	}
}
