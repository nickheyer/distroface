package runner

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitLists(t *testing.T) {
	if got := splitList("a:1, b:2\n\n c:3 "); strings.Join(got, "|") != "a:1|b:2|c:3" {
		t.Fatalf("splitList = %v", got)
	}
	if got := splitLines("type=registry,ref=x\n\nK=V"); len(got) != 2 || got[0] != "type=registry,ref=x" || got[1] != "K=V" {
		t.Fatalf("splitLines = %v", got)
	}
	if got := splitList(""); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
}

func TestParseBuildInput(t *testing.T) {
	in := parseBuildInput(map[string]string{
		"context": "./app", "file": "app/Dockerfile", "push": "true", "tags": "a:1,b:2",
		"build-args": "A=1\nB=2", "platforms": "linux/amd64,linux/arm64", "no-cache": "TRUE",
	})
	if in.Context != "./app" || in.File != "app/Dockerfile" || !in.Push || in.Load || !in.NoCache {
		t.Fatalf("%+v", in)
	}
	if len(in.Tags) != 2 || len(in.BuildArgs) != 2 || len(in.Platforms) != 2 {
		t.Fatalf("%+v", in)
	}
}

func TestBuildctlArgs(t *testing.T) {
	in := buildInput{
		Tags: []string{"localhost:8080/ns/app:v1", "localhost:8080/ns/app:latest"}, Push: true,
		BuildArgs: []string{"VERSION=1.2", "BARE"}, Labels: []string{"org.opencontainers.image.source=x"},
		Target: "runtime", Platforms: []string{"linux/amd64", "linux/arm64"}, NoCache: true, Pull: true,
		CacheFrom:  []string{"type=gha", "type=registry,ref=localhost:8080/ns/app:cache"},
		CacheTo:    []string{"type=registry,ref=localhost:8080/ns/app:cache,mode=max"},
		Provenance: "false",
	}
	plan := buildPlan{
		ContextDir: "/__w/app/app", DockerfileDir: "/__w/app/app", DockerfileName: "Dockerfile",
		MetadataFile: "/__w/_temp/buildkit-metadata.json", InsecureHost: "localhost:8080",
		SecretFiles:   map[string]string{"npm": "/__w/_temp/_build_secrets/npm"},
		LocalContexts: map[string]string{"shared": "/__w/app/app/shared"},
	}
	args, notes, err := buildctlArgs(in, plan)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"build --frontend dockerfile.v0",
		"--local context=/__w/app/app --local dockerfile=/__w/app/app --opt filename=Dockerfile",
		"--opt target=runtime",
		"--opt build-arg:VERSION=1.2",
		"--opt label:org.opencontainers.image.source=x",
		"--opt platform=linux/amd64,linux/arm64",
		"--no-cache",
		"--opt image-resolve-mode=pull",
		"--import-cache type=registry,ref=localhost:8080/ns/app:cache",
		"--export-cache type=registry,ref=localhost:8080/ns/app:cache,mode=max",
		"--secret id=npm,src=/__w/_temp/_build_secrets/npm",
		"--local shared=/__w/app/app/shared --opt context:shared=local:shared",
		"--metadata-file /__w/_temp/buildkit-metadata.json",
		`--output type=image,"name=localhost:8080/ns/app:v1,localhost:8080/ns/app:latest",push=true`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "build-arg:BARE") || strings.Contains(joined, "type=gha") {
		t.Errorf("skipped inputs leaked into %q", joined)
	}
	if len(notes) != 3 {
		t.Errorf("notes = %v", notes)
	}

	// Single tag, secure host, no insecure flag and no quoting
	args, _, err = buildctlArgs(buildInput{Tags: []string{"df.example.com/ns/app:v1"}, Push: true}, buildPlan{ContextDir: "/__w/x", DockerfileDir: "/__w/x", DockerfileName: "Dockerfile", InsecureHost: "localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if last := args[len(args)-1]; last != "type=image,name=df.example.com/ns/app:v1,push=true" {
		t.Errorf("output = %q", last)
	}

	// Load produces a docker tar
	args, _, err = buildctlArgs(buildInput{Tags: []string{"app:dev"}, Load: true}, buildPlan{ContextDir: "/__w/x", DockerfileDir: "/__w/x", DockerfileName: "Dockerfile", LoadTar: "/__w/_temp/buildkit-image.tar"})
	if err != nil {
		t.Fatal(err)
	}
	if last := args[len(args)-1]; last != "type=docker,dest=/__w/_temp/buildkit-image.tar,name=app:dev" {
		t.Errorf("load output = %q", last)
	}

	// Conflicts are refused
	if _, _, err := buildctlArgs(buildInput{Push: true}, plan); err == nil {
		t.Error("push without tags should fail")
	}
	if _, _, err := buildctlArgs(buildInput{Push: true, Load: true, Tags: []string{"a"}}, plan); err == nil {
		t.Error("push and load together should fail")
	}
	if _, _, err := buildctlArgs(buildInput{Load: true, Platforms: []string{"a", "b"}}, plan); err == nil {
		t.Error("multi platform load should fail")
	}

	// Remote context skips the local mounts
	args, _, err = buildctlArgs(buildInput{}, buildPlan{RemoteContext: "https://example.com/repo.git", DockerfileName: "Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	if j := strings.Join(args, " "); !strings.Contains(j, "--opt context=https://example.com/repo.git") || strings.Contains(j, "--local") {
		t.Errorf("remote args = %q", j)
	}
}

func TestTagHost(t *testing.T) {
	cases := map[string]string{
		"alpine":                        "docker.io",
		"library/alpine:3":              "docker.io",
		"localhost/x":                   "localhost",
		"localhost:8080/ns/app:v1":      "localhost:8080",
		"df.example.com/ns/app":         "df.example.com",
		"ghcr.io/owner/image@sha256:aa": "ghcr.io",
	}
	for tag, want := range cases {
		if got := tagHost(tag); got != want {
			t.Errorf("tagHost(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestContainerPath(t *testing.T) {
	job := filepath.Join(string(filepath.Separator), "var", "lib", "runner", "job-1")
	if p, ok := containerPath(job, filepath.Join(job, "app", "app", "src")); !ok || p != "/__w/app/app/src" {
		t.Fatalf("%q %v", p, ok)
	}
	if p, ok := containerPath(job, job); !ok || p != "/__w" {
		t.Fatalf("%q %v", p, ok)
	}
	if _, ok := containerPath(job, filepath.Join(job, "..", "other")); ok {
		t.Fatal("paths outside the job dir must be rejected")
	}
}

func TestBuildkitdConfig(t *testing.T) {
	if buildkitdConfig("df.example.com", false, false, nil) != "" {
		t.Fatal("nothing to configure should yield no file")
	}
	got := buildkitdConfig("localhost:8080", true, true, &dnsConfig{nameservers: []string{"192.168.65.7", "10.0.0.2"}, search: []string{"corp.example"}})
	for _, want := range []string{
		`[registry."localhost:8080"]`, "http = true", `ca = ["` + buildKitCAPath + `"]`,
		"[dns]", `nameservers = ["192.168.65.7", "10.0.0.2"]`, `searchDomains = ["corp.example"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "insecure = true") {
		t.Errorf("insecure must stay off so buildkit uses plain http, got %q", got)
	}
	if strings.Index(got, "[dns]") > strings.Index(got, "[registry.") {
		t.Errorf("dns table must come first so registry keys stay in their table: %q", got)
	}
}

func TestParseResolvConf(t *testing.T) {
	cfg := parseResolvConf("# Generated by Docker Engine.\nnameserver 127.0.0.11\nnameserver 192.168.65.7\nnameserver ::1\nsearch corp.example lan\noptions ndots:0\n")
	if strings.Join(cfg.nameservers, ",") != "192.168.65.7" {
		t.Errorf("nameservers = %v, loopback resolvers must be dropped", cfg.nameservers)
	}
	if strings.Join(cfg.search, ",") != "corp.example,lan" {
		t.Errorf("search = %v", cfg.search)
	}
}

func TestAnnotationAttrs(t *testing.T) {
	attrs, bad := annotationAttrs([]string{
		"manifest:org.opencontainers.image.title=app",
		"index:org.opencontainers.image.description=literally this app, really",
		"org.opencontainers.image.url=http://registry.localdomain:8080/nick/app",
		"bogus",
		"weird:k=v",
	})
	for _, want := range []string{
		",annotation-manifest.org.opencontainers.image.title=app",
		`,"annotation-index.org.opencontainers.image.description=literally this app, really"`,
		",annotation.org.opencontainers.image.url=http://registry.localdomain:8080/nick/app",
	} {
		if !strings.Contains(attrs, want) {
			t.Errorf("missing %q in %q", want, attrs)
		}
	}
	if len(bad) != 2 {
		t.Errorf("bad = %v", bad)
	}
	args, _, err := buildctlArgs(buildInput{Tags: []string{"r/app:1"}, Push: true, Annotations: []string{"manifest:a=b"}, AddHosts: []string{"reg:10.0.0.9", "x:1.2.3.4"}}, buildPlan{ContextDir: "/__w/x", DockerfileDir: "/__w/x", DockerfileName: "Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--opt add-host=reg:10.0.0.9,x:1.2.3.4") || !strings.HasSuffix(joined, "type=image,name=r/app:1,push=true,annotation-manifest.a=b") {
		t.Errorf("args = %q", joined)
	}
}

func TestBuildKitVolumeName(t *testing.T) {
	if got := buildKitVolumeName("Build Box #1", 2); got != "dfcli-buildkit-build-box--1-2" {
		t.Errorf("got %q", got)
	}
	if got := buildKitVolumeName("", 0); got != "dfcli-buildkit-runner-0" {
		t.Errorf("got %q", got)
	}
}

func TestTailWriter(t *testing.T) {
	var sink strings.Builder
	tw := newTailWriter(&sink, 2)
	_, _ = tw.Write([]byte("one\ntwo\nthr"))
	_, _ = tw.Write([]byte("ee\nfour\npart"))
	if sink.String() != "one\ntwo\nthree\nfour\npart" {
		t.Fatalf("forwarded %q", sink.String())
	}
	if got := tw.String(); got != "three\nfour\npart" {
		t.Fatalf("tail %q", got)
	}
}

func TestDockerAuthKey(t *testing.T) {
	for _, in := range []string{"", "docker.io", "index.docker.io", "https://index.docker.io/v1/", "https://index.docker.io/v1"} {
		if got := dockerAuthKey(in); got != dockerHubAuthKey {
			t.Errorf("dockerAuthKey(%q) = %q", in, got)
		}
	}
	if got := dockerAuthKey("https://df.example.com/"); got != "df.example.com" {
		t.Errorf("got %q", got)
	}
	if got := dockerAuthKey("localhost:8080"); got != "localhost:8080" {
		t.Errorf("got %q", got)
	}
}

func TestDockerConfigJSON(t *testing.T) {
	data, err := dockerConfigJSON(map[string]dockerAuth{
		"localhost:8080": {Username: jobTokenUser, Password: "df_token"},
		dockerHubAuthKey: {Username: "me", Password: "pw"},
		"ghcr.io":        {Username: "bot", Password: "ghp"},
		"https://x.y.z/": {Username: "a", Password: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Auths) != 4 {
		t.Fatalf("auths = %v", parsed.Auths)
	}
	raw, _ := base64.StdEncoding.DecodeString(parsed.Auths["localhost:8080"].Auth)
	if string(raw) != jobTokenUser+":df_token" {
		t.Fatalf("instance auth = %q", raw)
	}
	raw, _ = base64.StdEncoding.DecodeString(parsed.Auths[dockerHubAuthKey].Auth)
	if string(raw) != "me:pw" {
		t.Fatalf("hub auth = %q", raw)
	}
}

func TestNodeVersionFor(t *testing.T) {
	cases := map[string]int{"": 20, "node12": 20, "node16": 20, "node20": 20, "node24": 24, "Node24": 24, " node24 ": 24}
	for using, want := range cases {
		if got := nodeVersionFor(using); got != want {
			t.Errorf("nodeVersionFor(%q) = %d, want %d", using, got, want)
		}
	}
}

func TestExternalsLayout(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "var", "lib", "dfcli-runner", "_externals")
	if got := nodeDir(root, 20, flavorGlibc); got != filepath.Join(root, "node20", "glibc") {
		t.Errorf("nodeDir = %q", got)
	}
	if got := nodeContainerDir(24, flavorMusl); got != "/__e/node24/musl" {
		t.Errorf("nodeContainerDir = %q", got)
	}
	if nodeImage(20, flavorGlibc) != "docker.io/library/node:20-bookworm-slim" || nodeImage(24, flavorMusl) != "docker.io/library/node:24-alpine" {
		t.Error("node images wrong")
	}
	script := nodeExtractScript(flavorMusl)
	if !strings.Contains(script, "cp /usr/local/bin/node /out/bin/node") || !strings.Contains(script, "/usr/lib/libstdc++.so.6") || strings.Contains(script, "linux-gnu") {
		t.Errorf("musl script = %q", script)
	}
	if !strings.Contains(nodeExtractScript(flavorGlibc), "$(uname -m)-linux-gnu/libgcc_s.so.1") {
		t.Error("glibc script should pick the multiarch dir")
	}
}

func TestRunnerPlatforms(t *testing.T) {
	p := runnerPlatforms()
	if len(p) != 2 || !strings.HasPrefix(p[0], "linux/") || p[0] == p[1] {
		t.Fatalf("%v", p)
	}
}
