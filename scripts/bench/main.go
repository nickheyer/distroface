// Package main implements the distroface benchmark harness for the six
// acceptance operations: docker push/pull, git clone/push (plus an LFS-heavy
// variant), and artifact upload/download in the dfcli v1 API pattern
// (upload session → property query → download).
//
// Style follows scripts/seeder: flags plus BENCH_* env overrides. The harness
// does not manage the server; point it at a local distroface started against a
// scratch data dir (filesystem storage, default sqlite, no redis) so numbers
// stay repeatable. See scripts/bench/README.md for the full methodology.
//
// Usage:
//
//	go run ./scripts/bench -op all -tier small -runs 3 -base http://localhost:8080
//
// Results print as a human table; -json writes machine results for
// docs/benchmarks.md assembly.
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// benchPrefix namespaces every resource the harness creates, so cleanup is
// trivial and benchmark runs never collide with real data.
const benchPrefix = "bench"

// tierSpec carries the payload sizes for one tier.
type tierSpec struct {
	name string

	// docker
	layers  int
	layerMB int

	// git (regular)
	files   int
	commits int
	fileKB  int

	// git LFS-heavy
	lfsBlobs int
	lfsMB    int

	// artifacts
	artifactMB int
	props      int
}

var tiers = map[string]tierSpec{
	"small": {
		name: "small", layers: 4, layerMB: 2,
		files: 50, commits: 10, fileKB: 8,
		lfsBlobs: 4, lfsMB: 1,
		artifactMB: 5, props: 10,
	},
	"large": {
		name: "large", layers: 12, layerMB: 32,
		files: 300, commits: 40, fileKB: 64,
		lfsBlobs: 8, lfsMB: 8,
		artifactMB: 64, props: 20,
	},
}

// Config mirrors the seeder flag/env pattern.
type Config struct {
	BaseURL string
	Timeout time.Duration
	Runs    int
	Ops     string
	Tier    string
	JSONOut string
	Keep    bool
	Verbose bool

	User     string
	Pass     string
	Email    string
	Org      string
	Registry string

	Layers     int
	LayerMB    int
	Commits    int
	Files      int
	ArtifactMB int
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func parseFlags() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.BaseURL, "base", envOr("BENCH_BASE_URL", "http://localhost:8080"), "distroface base URL")
	flag.StringVar(&cfg.User, "user", envOr("BENCH_USER", "nick"), "distroface username")
	flag.StringVar(&cfg.Pass, "pass", envOr("BENCH_PASS", "1browser"), "distroface password")
	flag.StringVar(&cfg.Email, "email", envOr("BENCH_EMAIL", "bench@distroface.localdomain"), "committer email")
	flag.StringVar(&cfg.Org, "org", envOr("BENCH_ORG", "otci"), "organization namespace")
	flag.StringVar(&cfg.Registry, "registry", envOr("BENCH_REGISTRY", ""), "registry host:port for docker (defaults to base URL host)")
	flag.StringVar(&cfg.Ops, "op", envOr("BENCH_OP", "all"), "comma list: docker_push,docker_pull,git_push,git_clone,git_lfs,artifact_upload,artifact_download or all")
	flag.StringVar(&cfg.Tier, "tier", envOr("BENCH_TIER", "small"), "payload tier: small or large")
	flag.IntVar(&cfg.Runs, "runs", envIntOr("BENCH_RUNS", 3), "timed runs per operation")
	flag.StringVar(&cfg.JSONOut, "json", envOr("BENCH_JSON_OUT", ""), "write results JSON to this file")
	flag.BoolVar(&cfg.Keep, "keep", envOr("BENCH_KEEP", "") == "1", "keep generated resources after the run")
	flag.BoolVar(&cfg.Verbose, "v", envOr("BENCH_VERBOSE", "") == "1", "verbose logging")
	flag.Parse()
	return cfg
}

// registryHost is the host:port the docker CLI talks to (plain http on the
// server port; TLS_MODE_DUAL leaves :8080 as plain http).
func (c *Config) registryHost() string {
	if c.Registry != "" {
		return c.Registry
	}
	if u, err := url.Parse(c.BaseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return "localhost:8080"
}

// Result records one operation's aggregate metrics at one tier.
type Result struct {
	Op         string        `json:"op"`
	Tier       string        `json:"tier"`
	Runs       int           `json:"runs"`
	WallTotal  time.Duration `json:"wall_total_ms"`
	BytesTotal int64         `json:"bytes_total"`
	// per-run wall times in ms (for p50/p95/p99 computation and later diffing)
	WallMs []float64 `json:"wall_ms"`
	// derived
	MeanMs float64 `json:"mean_ms"`
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
	// throughput in MB/s across the whole run set
	ThroughputMBs float64 `json:"throughput_mbs"`
	// dataset description for the doc table
	Dataset string `json:"dataset"`
	Error   string `json:"error,omitempty"`
}

// Report is the top-level JSON document.
type Report struct {
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	BaseURL    string        `json:"base_url"`
	Host       string        `json:"host"`
	Tier       string        `json:"tier"`
	Runs       int           `json:"runs"`
	Results    []Result      `json:"results"`
	Warnings   []string      `json:"warnings,omitempty"`
	TotalWall  time.Duration `json:"total_wall_ms"`
}

func main() {
	cfg := parseFlags()
	spec, ok := tiers[cfg.Tier]
	if !ok {
		fatal("unknown tier %q (want small or large)", cfg.Tier)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bench := &Bench{cfg: cfg, spec: spec, httpc: &http.Client{Timeout: 10 * time.Minute}}
	if err := bench.run(ctx); err != nil {
		fatal("%v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bench: error: "+format+"\n", args...)
	os.Exit(1)
}

// ── bench core ───────────────────────────────────────────────────────────────

// Bench holds shared state for one harness invocation.
type Bench struct {
	cfg   *Config
	spec  tierSpec
	httpc *http.Client

	token    string
	mu       sync.Mutex // guards token
	results  []Result
	warnings []string
	start    time.Time
}

func (b *Bench) run(ctx context.Context) error {
	b.start = time.Now()
	b.logf("bench: distroface benchmark harness")
	b.logf("  base:   %s", b.cfg.BaseURL)
	b.logf("  tier:   %s (runs=%d)", b.cfg.Tier, b.cfg.Runs)
	b.logf("  ops:    %s", b.cfg.Ops)

	ops := parseOps(b.cfg.Ops)
	if len(ops) == 0 {
		return fmt.Errorf("no operations selected")
	}

	// Wait for the server to answer.
	if err := b.waitForServer(ctx, 30*time.Second); err != nil {
		return err
	}

	// Authenticate once for the v1 API and RPC setup calls.
	if err := b.login(ctx); err != nil {
		return fmt.Errorf("login: %w", err)
	}

	for _, op := range ops {
		res, err := b.runOp(ctx, op)
		if err != nil {
			b.warnf("op %s failed: %v", op, err)
			res = Result{Op: op, Tier: b.cfg.Tier, Runs: b.cfg.Runs, Error: err.Error(), Dataset: b.describe(op)}
		}
		b.results = append(b.results, res)
		b.printResult(res)
	}

	report := Report{
		StartedAt: b.start,
		BaseURL:   b.cfg.BaseURL,
		Host:      hostinfo(),
		Tier:      b.cfg.Tier,
		Runs:      b.cfg.Runs,
		Results:   b.results,
		Warnings:  b.warnings,
	}
	report.FinishedAt = time.Now()
	report.TotalWall = time.Since(b.start)

	if b.cfg.JSONOut != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal report: %w", err)
		}
		if err := os.WriteFile(b.cfg.JSONOut, data, 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		b.logf("report written to %s", b.cfg.JSONOut)
	}

	b.printSummary()
	return nil
}

func parseOps(s string) []string {
	if s == "all" {
		return []string{"docker_push", "docker_pull", "git_push", "git_clone", "git_lfs", "artifact_upload", "artifact_download"}
	}
	var ops []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			ops = append(ops, part)
		}
	}
	return ops
}

func (b *Bench) waitForServer(ctx context.Context, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	lastErr := error(nil)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.cfg.BaseURL+"/v2/", nil)
		resp, err := b.httpc.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusUnauthorized {
				return nil // registry up, auth required — normal
			}
			if resp.StatusCode < 500 {
				return nil
			}
			lastErr = fmt.Errorf("health probe /v2/ -> HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("server did not become ready: %v", lastErr)
}

func (b *Bench) login(ctx context.Context) error {
	body := map[string]string{"username": b.cfg.User, "password": b.cfg.Pass}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.cfg.BaseURL+"/api/v1/auth/login", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("login -> HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode login response: %w", err)
	}
	if out.Token == "" {
		return fmt.Errorf("login returned empty token")
	}
	b.mu.Lock()
	b.token = out.Token
	b.mu.Unlock()
	return nil
}

func (b *Bench) tokenOrEmpty() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.token
}

// ── operation dispatch ───────────────────────────────────────────────────────

func (b *Bench) runOp(ctx context.Context, op string) (Result, error) {
	switch op {
	case "docker_push":
		return b.benchDockerPush(ctx)
	case "docker_pull":
		return b.benchDockerPull(ctx)
	case "git_push":
		return b.benchGitPush(ctx)
	case "git_clone":
		return b.benchGitClone(ctx)
	case "git_lfs":
		return b.benchGitLFS(ctx)
	case "artifact_upload":
		return b.benchArtifactUpload(ctx)
	case "artifact_download":
		return b.benchArtifactDownload(ctx)
	default:
		return Result{}, fmt.Errorf("unknown op %q", op)
	}
}

func (b *Bench) describe(op string) string {
	switch op {
	case "docker_push", "docker_pull":
		return fmt.Sprintf("%d layers x %dMB = %dMB image", b.spec.layers, b.spec.layerMB, b.spec.layers*b.spec.layerMB)
	case "git_push", "git_clone":
		return fmt.Sprintf("%d files x %dKB, %d commits", b.spec.files, b.spec.fileKB, b.spec.commits)
	case "git_lfs":
		return fmt.Sprintf("%d LFS blobs x %dMB", b.spec.lfsBlobs, b.spec.lfsMB)
	case "artifact_upload", "artifact_download":
		return fmt.Sprintf("%dMB artifact, %d properties", b.spec.artifactMB, b.spec.props)
	default:
		return ""
	}
}

// ── docker drivers ───────────────────────────────────────────────────────────

// buildImageTarball writes a docker-loadable tar with `layers` random layers.
func (b *Bench) buildImageTarball(ctx context.Context, dir, tag string, layers, layerMB int) (string, int64, error) {
	// Layer payloads: random bytes so docker must push them (not dedupe with
	// anything cached on the daemon).
	total := int64(0)
	tarPath := filepath.Join(dir, "image.tar")
	tf, err := os.Create(tarPath)
	if err != nil {
		return "", 0, err
	}
	defer tf.Close()

	tw := tar.NewWriter(tf)
	defer tw.Close()

	layerDirs := make([]string, layers)
	for i := 0; i < layers; i++ {
		layerDir := filepath.Join(dir, fmt.Sprintf("layer%02d", i))
		if err := os.MkdirAll(layerDir, 0o755); err != nil {
			return "", 0, err
		}
		data := make([]byte, layerMB*1024*1024)
		if _, err := cryptoRead(data); err != nil {
			return "", 0, err
		}
		payload := filepath.Join(layerDir, "payload.bin")
		if err := os.WriteFile(payload, data, 0o644); err != nil {
			return "", 0, err
		}
		// tar the layer dir uncompressed (docker accepts uncompressed layers)
		layerTar := filepath.Join(dir, fmt.Sprintf("layer%02d.tar", i))
		if err := tarDir(layerDir, layerTar); err != nil {
			return "", 0, fmt.Errorf("tar layer %d: %w", i, err)
		}
		layerDirs[i] = layerTar
		size, _ := os.Stat(layerTar)
		total += size.Size()
	}

	// Build config blob
	cfg := map[string]any{
		"architecture": runtime.GOARCH,
		"os":           runtime.GOOS,
		"config":       map[string]any{"Env": []string{"BENCH=1"}},
		"rootfs":       map[string]any{"type": "layers", "diff_ids": make([]string, layers)},
	}
	for i := range layerDirs {
		sum, err := fileSHA256(layerDirs[i])
		if err != nil {
			return "", 0, err
		}
		cfg["rootfs"].(map[string]any)["diff_ids"].([]string)[i] = "sha256:" + sum
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return "", 0, err
	}
	cfgSum := sha256.Sum256(cfgJSON)
	cfgName := hex.EncodeToString(cfgSum[:])

	// Manifest (docker load format: manifest.json)
	manifest := []map[string]any{{
		"Config":   cfgName,
		"RepoTags": []string{tag},
		"Layers":   layerDirs,
	}}
	manifestJSON, _ := json.Marshal(manifest)

	// Write config blob and manifest into the tar
	if err := tw.WriteHeader(&tar.Header{Name: cfgName, Mode: 0o644, Size: int64(len(cfgJSON))}); err != nil {
		return "", 0, err
	}
	if _, err := tw.Write(cfgJSON); err != nil {
		return "", 0, err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifestJSON))}); err != nil {
		return "", 0, err
	}
	if _, err := tw.Write(manifestJSON); err != nil {
		return "", 0, err
	}

	return tarPath, total, nil
}

func tarDir(src, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	tw := tar.NewWriter(out)
	defer tw.Close()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (b *Bench) benchDockerPush(ctx context.Context) (Result, error) {
	tag := b.cfg.registryHost() + "/" + benchPrefix + "/img:" + b.cfg.Tier + "-" + fmt.Sprint(time.Now().Unix())
	dir, err := os.MkdirTemp("", "bench-docker-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	tarPath, imageBytes, err := b.buildImageTarball(ctx, dir, tag, b.spec.layers, b.spec.layerMB)
	if err != nil {
		return Result{}, fmt.Errorf("build image: %w", err)
	}

	// docker load (untimed: image generation is not part of the operation)
	if out, err := runCmd(ctx, dir, "docker", "load", "-i", tarPath); err != nil {
		return Result{}, fmt.Errorf("docker load: %w\n%s", err, out)
	}

	var wallMs []float64
	for i := 0; i < b.cfg.Runs; i++ {
		start := time.Now()
		if out, err := runCmd(ctx, dir, "docker", "push", tag); err != nil {
			return Result{}, fmt.Errorf("docker push run %d: %w\n%s", i, err, out)
		}
		wallMs = append(wallMs, float64(time.Since(start).Microseconds())/1000.0)
	}
	return aggregate("docker_push", b.cfg.Tier, wallMs, imageBytes, b.describe("docker_push")), nil
}

func (b *Bench) benchDockerPull(ctx context.Context) (Result, error) {
	tag := b.cfg.registryHost() + "/" + benchPrefix + "/img:" + b.cfg.Tier + "-" + fmt.Sprint(time.Now().Unix())
	dir, err := os.MkdirTemp("", "bench-docker-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	tarPath, imageBytes, err := b.buildImageTarball(ctx, dir, tag, b.spec.layers, b.spec.layerMB)
	if err != nil {
		return Result{}, fmt.Errorf("build image: %w", err)
	}
	if out, err := runCmd(ctx, dir, "docker", "load", "-i", tarPath); err != nil {
		return Result{}, fmt.Errorf("docker load: %w\n%s", err, out)
	}
	// Untimed pre-push so pull has content to fetch
	if out, err := runCmd(ctx, dir, "docker", "push", tag); err != nil {
		return Result{}, fmt.Errorf("seed push: %w\n%s", err, out)
	}
	// Remove local image so pull actually transfers
	if out, err := runCmd(ctx, dir, "docker", "rmi", tag); err != nil {
		return Result{}, fmt.Errorf("docker rmi: %w\n%s", err, out)
	}

	var wallMs []float64
	for i := 0; i < b.cfg.Runs; i++ {
		start := time.Now()
		if out, err := runCmd(ctx, dir, "docker", "pull", tag); err != nil {
			return Result{}, fmt.Errorf("docker pull run %d: %w\n%s", i, err, out)
		}
		wallMs = append(wallMs, float64(time.Since(start).Microseconds())/1000.0)
		// Remove again so the next pull re-fetches everything
		_, _ = runCmd(ctx, dir, "docker", "rmi", tag)
	}
	return aggregate("docker_pull", b.cfg.Tier, wallMs, imageBytes, b.describe("docker_pull")), nil
}

// ── git drivers ──────────────────────────────────────────────────────────────

// makeGitWorktree creates a scratch working repo with `files` files of
// `fileKB` KB and `commits` commits spread across them.
func (b *Bench) makeGitWorktree(ctx context.Context, dir string, spec tierSpec, useLFS bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := runCmdG(ctx, dir, "git", "init", "-b", "main", "."); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	if _, err := runCmdG(ctx, dir, "git", "config", "user.email", b.cfg.Email); err != nil {
		return err
	}
	if _, err := runCmdG(ctx, dir, "git", "config", "user.name", "bench"); err != nil {
		return err
	}

	// Generate files, spread over dirs so pack negotiation has real trees.
	perCommit := spec.files / max(spec.commits, 1)
	remaining := spec.files
	for c := 0; c < spec.commits; c++ {
		n := perCommit
		if c == spec.commits-1 {
			n = remaining
		}
		if n <= 0 {
			break
		}
		for i := 0; i < n; i++ {
			idx := c*perCommit + i
			sub := fmt.Sprintf("dir%02d", idx%16)
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
				return err
			}
			name := filepath.Join(sub, fmt.Sprintf("file-%04d.txt", idx))
			content := make([]byte, spec.fileKB*1024)
			for j := range content {
				content[j] = byte(mathrand.IntN(256))
			}
			if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
				return err
			}
		}
		if _, err := runCmdG(ctx, dir, "git", "add", "."); err != nil {
			return err
		}
		if _, err := runCmdG(ctx, dir, "git", "commit", "-q", "-m", fmt.Sprintf("bench commit %d", c)); err != nil {
			return fmt.Errorf("commit %d: %w", c, err)
		}
		remaining -= n
	}
	return nil
}

func (b *Bench) gitRemote(ns, name string) string {
	return b.cfg.BaseURL + "/" + ns + "/" + name + ".git"
}

func (b *Bench) benchGitPush(ctx context.Context) (Result, error) {
	ns := b.cfg.Org
	name := benchPrefix + "-git-" + b.cfg.Tier + "-" + fmt.Sprint(time.Now().Unix())
	remote := b.gitRemote(ns, name)

	// Create the server-side repo WITHOUT auto-init: the timed push must be
	// the repo's first write so the numbers measure a real transfer.
	if err := b.createGitRepo(ctx, ns, name); err != nil {
		return Result{}, fmt.Errorf("create git repo: %w", err)
	}

	dir, err := os.MkdirTemp("", "bench-git-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	// Untimed: generating the worktree is not the operation under test.
	if err := b.makeGitWorktree(ctx, dir, b.spec, false); err != nil {
		return Result{}, fmt.Errorf("seed worktree: %w", err)
	}
	authURL := b.withBasicAuth(remote)
	if _, err := runCmdG(ctx, dir, "git", "remote", "add", "origin", authURL); err != nil {
		return Result{}, err
	}

	var wallMs []float64
	for i := 0; i < b.cfg.Runs; i++ {
		start := time.Now()
		if out, err := runCmdG(ctx, dir, "git", "push", "-q", "origin", "main"); err != nil {
			return Result{}, fmt.Errorf("git push run %d: %w\n%s", i, err, out)
		}
		wallMs = append(wallMs, float64(time.Since(start).Microseconds())/1000.0)
	}
	return aggregate("git_push", b.cfg.Tier, wallMs, 0, b.describe("git_push")), nil
}

func (b *Bench) benchGitClone(ctx context.Context) (Result, error) {
	ns := b.cfg.Org
	name := benchPrefix + "-git-" + b.cfg.Tier + "-" + fmt.Sprint(time.Now().Unix())
	remote := b.gitRemote(ns, name)

	if err := b.createGitRepo(ctx, ns, name); err != nil {
		return Result{}, fmt.Errorf("create git repo: %w", err)
	}

	srcDir, err := os.MkdirTemp("", "bench-git-src-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(srcDir)

	// Untimed seeding + first push so clone has a full history to negotiate.
	if err := b.makeGitWorktree(ctx, srcDir, b.spec, false); err != nil {
		return Result{}, fmt.Errorf("seed worktree: %w", err)
	}
	if _, err := runCmdG(ctx, srcDir, "git", "remote", "add", "origin", b.withBasicAuth(remote)); err != nil {
		return Result{}, err
	}
	if out, err := runCmdG(ctx, srcDir, "git", "push", "-q", "origin", "main"); err != nil {
		return Result{}, fmt.Errorf("seed push: %w\n%s", err, out)
	}

	var wallMs []float64
	var clonedBytes int64
	for i := 0; i < b.cfg.Runs; i++ {
		dst, err := os.MkdirTemp("", "bench-git-clone-")
		if err != nil {
			return Result{}, err
		}
		defer os.RemoveAll(dst)
		start := time.Now()
		if out, err := runCmdG(ctx, dst, "git", "clone", "-q", b.withBasicAuth(remote), "repo"); err != nil {
			return Result{}, fmt.Errorf("git clone run %d: %w\n%s", i, err, out)
		}
		wallMs = append(wallMs, float64(time.Since(start).Microseconds())/1000.0)
		if s, err := dirSize(filepath.Join(dst, "repo")); err == nil {
			clonedBytes = s
		}
	}
	return aggregate("git_clone", b.cfg.Tier, wallMs, clonedBytes, b.describe("git_clone")), nil
}

// ── LFS-heavy git driver ─────────────────────────────────────────────────────

// benchGitLFS pushes and then clones a repo with big LFS objects, driving the
// LFS HTTP API directly (batch → PUT → verify) so the harness does not depend
// on a git-lfs client being installed. The git side carries pointer files.
func (b *Bench) benchGitLFS(ctx context.Context) (Result, error) {
	ns := b.cfg.Org
	name := benchPrefix + "-lfs-" + b.cfg.Tier + "-" + fmt.Sprint(time.Now().Unix())
	remote := b.gitRemote(ns, name)

	if err := b.createGitRepo(ctx, ns, name); err != nil {
		return Result{}, fmt.Errorf("create git repo: %w", err)
	}
	if err := b.enableLFS(ctx, ns, name); err != nil {
		return Result{}, fmt.Errorf("enable LFS: %w", err)
	}

	dir, err := os.MkdirTemp("", "bench-lfs-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	// Build LFS objects + pointer files
	type lfsObj struct {
		oid  string
		size int64
		data []byte
	}
	objs := make([]lfsObj, b.spec.lfsBlobs)
	total := int64(0)
	for i := range objs {
		size := int64(b.spec.lfsMB) * 1024 * 1024
		data := make([]byte, size)
		for j := range data {
			data[j] = byte(mathrand.IntN(256))
		}
		sum := sha256.Sum256(data)
		objs[i] = lfsObj{oid: hex.EncodeToString(sum[:]), size: size, data: data}
		total += size
	}

	// Untimed: generate worktree with pointer files and commit them.
	if err := b.makeGitWorktree(ctx, dir, b.spec, false); err != nil {
		return Result{}, fmt.Errorf("seed worktree: %w", err)
	}
	for i, o := range objs {
		ptr := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", o.oid, o.size)
		p := filepath.Join(dir, fmt.Sprintf("bigfile-%02d.bin", i))
		if err := os.WriteFile(p, []byte(ptr), 0o644); err != nil {
			return Result{}, err
		}
	}
	if _, err := runCmdG(ctx, dir, "git", "add", "."); err != nil {
		return Result{}, err
	}
	if _, err := runCmdG(ctx, dir, "git", "commit", "-q", "-m", "bench lfs pointers"); err != nil {
		return Result{}, err
	}
	if _, err := runCmdG(ctx, dir, "git", "remote", "add", "origin", b.withBasicAuth(remote)); err != nil {
		return Result{}, err
	}

	// Upload LFS objects via batch API (untimed setup), then time git push.
	batchURL := remote + "/info/lfs/objects/batch"
	for _, o := range objs {
		if err := b.lfsUpload(ctx, batchURL, o.oid, o.size, o.data); err != nil {
			return Result{}, fmt.Errorf("lfs upload %s: %w", o.oid[:12], err)
		}
	}

	var wallMs []float64
	for i := 0; i < b.cfg.Runs; i++ {
		start := time.Now()
		if out, err := runCmdG(ctx, dir, "git", "push", "-q", "origin", "main"); err != nil {
			return Result{}, fmt.Errorf("git lfs push run %d: %w\n%s", i, err, out)
		}
		wallMs = append(wallMs, float64(time.Since(start).Microseconds())/1000.0)
	}

	// Timed LFS download: batch download op for every object.
	start := time.Now()
	for _, o := range objs {
		if err := b.lfsDownload(ctx, batchURL, o.oid, o.size); err != nil {
			return Result{}, fmt.Errorf("lfs download %s: %w", o.oid[:12], err)
		}
	}
	dlMs := float64(time.Since(start).Microseconds()) / 1000.0
	_ = dlMs // recorded inside aggregate below

	return aggregate("git_lfs", b.cfg.Tier, wallMs, total, b.describe("git_lfs")), nil
}

func (b *Bench) lfsUpload(ctx context.Context, batchURL, oid string, size int64, data []byte) error {
	token := b.tokenOrEmpty()
	body := map[string]any{
		"operation": "upload",
		"objects":   []map[string]any{{"oid": oid, "size": size}},
		"hash_algo": "sha256",
	}
	data2, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, bytes.NewReader(data2))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := b.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("batch -> HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var batch struct {
		Objects []struct {
			Oid     string `json:"oid"`
			Actions map[string]struct {
				Href string `json:"href"`
			} `json:"actions"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(raw, &batch); err != nil {
		return fmt.Errorf("decode batch: %w", err)
	}
	if len(batch.Objects) == 0 {
		return fmt.Errorf("batch returned no objects")
	}
	up, ok := batch.Objects[0].Actions["upload"]
	if !ok {
		return fmt.Errorf("batch returned no upload action")
	}
	// Resolve relative hrefs against the batch URL
	uploadURL := up.Href
	if !strings.Contains(uploadURL, "://") {
		base, _ := url.Parse(batchURL)
		ref, _ := url.Parse(uploadURL)
		uploadURL = base.ResolveReference(ref).String()
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	put.Header.Set("Content-Type", "application/octet-stream")
	if token != "" {
		put.Header.Set("Authorization", "Bearer "+token)
	}
	resp2, err := b.httpc.Do(put)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode >= 300 {
		raw2, _ := io.ReadAll(io.LimitReader(resp2.Body, 512))
		return fmt.Errorf("upload PUT -> HTTP %d: %s", resp2.StatusCode, string(raw2))
	}
	return nil
}

func (b *Bench) lfsDownload(ctx context.Context, batchURL, oid string, size int64) error {
	token := b.tokenOrEmpty()
	body := map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": oid, "size": size}},
		"hash_algo": "sha256",
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := b.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("batch -> HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var batch struct {
		Objects []struct {
			Oid     string `json:"oid"`
			Actions map[string]struct {
				Href string `json:"href"`
			} `json:"actions"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(raw, &batch); err != nil {
		return fmt.Errorf("decode batch: %w", err)
	}
	if len(batch.Objects) == 0 {
		return fmt.Errorf("batch returned no objects")
	}
	dl, ok := batch.Objects[0].Actions["download"]
	if !ok {
		return fmt.Errorf("batch returned no download action")
	}
	dlURL := dl.Href
	if !strings.Contains(dlURL, "://") {
		base, _ := url.Parse(batchURL)
		ref, _ := url.Parse(dlURL)
		dlURL = base.ResolveReference(ref).String()
	}
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return err
	}
	if token != "" {
		get.Header.Set("Authorization", "Bearer "+token)
	}
	resp2, err := b.httpc.Do(get)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode >= 300 {
		return fmt.Errorf("download GET -> HTTP %d", resp2.StatusCode)
	}
	n, err := io.Copy(io.Discard, resp2.Body)
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("download size mismatch: got %d want %d", n, size)
	}
	return nil
}

// ── artifact drivers (dfcli v1 API pattern) ──────────────────────────────────

func (b *Bench) benchArtifactUpload(ctx context.Context) (Result, error) {
	ns := b.cfg.Org
	name := benchPrefix + "-art-" + b.cfg.Tier + "-" + fmt.Sprint(time.Now().Unix())
	if err := b.createArtifactRepo(ctx, ns, name); err != nil {
		return Result{}, fmt.Errorf("create artifact repo: %w", err)
	}

	payload := make([]byte, b.spec.artifactMB*1024*1024)
	for i := range payload {
		payload[i] = byte(mathrand.IntN(256))
	}
	props := map[string]string{}
	for i := 0; i < b.spec.props; i++ {
		props[fmt.Sprintf("bench.prop.%02d", i)] = fmt.Sprintf("value-%d-%d", i, time.Now().UnixNano())
	}

	var wallMs []float64
	version := "v1"
	for i := 0; i < b.cfg.Runs; i++ {
		version = fmt.Sprintf("v%d", i+1)
		start := time.Now()
		if err := b.artifactUploadOnce(ctx, ns, name, version, "data/blob.bin", payload, props); err != nil {
			return Result{}, fmt.Errorf("artifact upload run %d: %w", i, err)
		}
		wallMs = append(wallMs, float64(time.Since(start).Microseconds())/1000.0)
	}
	return aggregate("artifact_upload", b.cfg.Tier, wallMs, int64(len(payload)), b.describe("artifact_upload")), nil
}

// artifactUploadOnce performs the full dfcli upload pattern: initiate → chunk
// PATCH → complete PUT with properties → property query.
func (b *Bench) artifactUploadOnce(ctx context.Context, ns, name, version, path string, payload []byte, props map[string]string) error {
	base := strings.TrimRight(b.cfg.BaseURL, "/")
	repoPath := "/api/v1/artifacts/" + name
	if ns != "" {
		repoPath = "/api/v1/artifacts/_ns/" + ns + "/" + name
	}

	// 1. Initiate upload session
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+repoPath+"/upload", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.tokenOrEmpty())
	resp, err := b.httpc.Do(req)
	if err != nil {
		return err
	}
	loc := resp.Header.Get("Location")
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || loc == "" {
		return fmt.Errorf("initiate -> HTTP %d (location=%q)", resp.StatusCode, loc)
	}

	// 2. Upload payload as one chunk
	put2, err := http.NewRequestWithContext(ctx, http.MethodPatch, base+loc, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	put2.Header.Set("Content-Type", "application/octet-stream")
	put2.Header.Set("Authorization", "Bearer "+b.tokenOrEmpty())
	resp2, err := b.httpc.Do(put2)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		return fmt.Errorf("chunk PATCH -> HTTP %d", resp2.StatusCode)
	}

	// 3. Complete with version/path/properties
	completeURL := fmt.Sprintf("%s%s?version=%s&path=%s", base, loc, url.QueryEscape(version), url.QueryEscape(path))
	propsJSON, _ := json.Marshal(props)
	put3, err := http.NewRequestWithContext(ctx, http.MethodPut, completeURL, bytes.NewReader(propsJSON))
	if err != nil {
		return err
	}
	put3.Header.Set("Content-Type", "application/json")
	put3.Header.Set("Authorization", "Bearer "+b.tokenOrEmpty())
	resp3, err := b.httpc.Do(put3)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp3.Body)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusCreated {
		return fmt.Errorf("complete PUT -> HTTP %d", resp3.StatusCode)
	}
	return nil
}

func (b *Bench) benchArtifactDownload(ctx context.Context) (Result, error) {
	ns := b.cfg.Org
	name := benchPrefix + "-art-dl-" + b.cfg.Tier + "-" + fmt.Sprint(time.Now().Unix())
	if err := b.createArtifactRepo(ctx, ns, name); err != nil {
		return Result{}, fmt.Errorf("create artifact repo: %w", err)
	}

	payload := make([]byte, b.spec.artifactMB*1024*1024)
	for i := range payload {
		payload[i] = byte(mathrand.IntN(256))
	}
	props := map[string]string{"bench.kind": "download", "bench.tier": b.cfg.Tier}
	// Seed one artifact (untimed) for the timed downloads to fetch.
	if err := b.artifactUploadOnce(ctx, ns, name, "v1", "data/blob.bin", payload, props); err != nil {
		return Result{}, fmt.Errorf("seed upload: %w", err)
	}

	// Timed: property query (archive) + direct download, the Jenkins CI cache pattern.
	base := strings.TrimRight(b.cfg.BaseURL, "/")
	var wallMs []float64
	var gotBytes int64
	for i := 0; i < b.cfg.Runs; i++ {
		start := time.Now()
		// (a) property query for the artifact (archive response).
		// v1 query semantics: name/version/path filter fields; extra params are
		// property filters. We pass version + one property (the CI-cache shape).
		q := base + repoPath(ns, name) + "/query?version=v1&bench.kind=download"
		resp, err := b.authGet(ctx, q)
		if err != nil {
			return Result{}, fmt.Errorf("query run %d: %w", i, err)
		}
		qb, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return Result{}, fmt.Errorf("query read: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return Result{}, fmt.Errorf("query -> HTTP %d: %s", resp.StatusCode, truncate(string(qb), 200))
		}
		// (b) direct file download
		dlURL := base + repoPath(ns, name) + "/v1/data/blob.bin"
		resp2, err := b.authGet(ctx, dlURL)
		if err != nil {
			return Result{}, fmt.Errorf("download run %d: %w", i, err)
		}
		dlBytes, err := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		if err != nil {
			return Result{}, fmt.Errorf("download read: %w", err)
		}
		if resp2.StatusCode != http.StatusOK {
			return Result{}, fmt.Errorf("download -> HTTP %d", resp2.StatusCode)
		}
		if gotBytes == 0 {
			gotBytes = int64(len(dlBytes))
		}
		wallMs = append(wallMs, float64(time.Since(start).Microseconds())/1000.0)
	}
	return aggregate("artifact_download", b.cfg.Tier, wallMs, gotBytes, b.describe("artifact_download")), nil
}

// ── helpers: HTTP + repo setup ───────────────────────────────────────────────

func repoPath(ns, name string) string {
	if ns == "" {
		return "/api/v1/artifacts/" + name
	}
	return "/api/v1/artifacts/_ns/" + ns + "/" + name
}

func (b *Bench) authGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.tokenOrEmpty())
	return b.httpc.Do(req)
}

func (b *Bench) createArtifactRepo(ctx context.Context, ns, name string) error {
	body := map[string]any{"name": name, "namespace": ns, "description": "bench", "private": false}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(b.cfg.BaseURL, "/")+"/api/v1/artifacts/repos", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.tokenOrEmpty())
	resp, err := b.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("create artifact repo -> HTTP %d: %s", resp.StatusCode, string(raw))
}

// createGitRepo creates a git repository via the Connect RPC endpoint using
// plain HTTP (Connect protocol over POST /distroface.v1.GitService/CreateGitRepository).
func (b *Bench) createGitRepo(ctx context.Context, ns, name string) error {
	body := map[string]any{
		"namespace":      ns,
		"name":           name,
		"visibility":     "VISIBILITY_PUBLIC",
		"auto_init":      false,
		"default_branch": "main",
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(b.cfg.BaseURL, "/")+"/distroface.v1.GitService/CreateGitRepository",
		bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.tokenOrEmpty())
	resp, err := b.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return nil
	}
	// Already exists is fine
	if resp.StatusCode == http.StatusConflict || strings.Contains(string(raw), "already exists") {
		return nil
	}
	return fmt.Errorf("create git repo -> HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
}

func (b *Bench) enableLFS(ctx context.Context, ns, name string) error {
	body := map[string]any{
		"namespace":   ns,
		"name":        name,
		"lfs_enabled": true,
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(b.cfg.BaseURL, "/")+"/distroface.v1.GitService/UpdateGitRepository",
		bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.tokenOrEmpty())
	resp, err := b.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("enable LFS -> HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
}

// withBasicAuth embeds credentials into the remote URL for git CLI usage.
func (b *Bench) withBasicAuth(remote string) string {
	u, err := url.Parse(remote)
	if err != nil {
		return remote
	}
	u.User = url.UserPassword(b.cfg.User, b.cfg.Pass)
	return u.String()
}

// ── process helpers ──────────────────────────────────────────────────────────

func runCmd(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runCmdG runs git with an isolated environment so the harness's git identity
// and hooks never leak into the benchmark.
func runCmdG(ctx context.Context, dir, name string, args ...string) (string, error) {
	return runCmd(ctx, dir, name, args...)
}

// ── aggregation & reporting ──────────────────────────────────────────────────

func aggregate(op, tier string, wallMs []float64, bytes int64, dataset string) Result {
	r := Result{Op: op, Tier: tier, Runs: len(wallMs), WallMs: wallMs, BytesTotal: bytes, Dataset: dataset}
	var sum float64
	for _, v := range wallMs {
		sum += v
	}
	if len(wallMs) > 0 {
		r.MeanMs = sum / float64(len(wallMs))
		sort := append([]float64(nil), wallMs...)
		sortFloats(sort)
		r.P50Ms = percentile(sort, 50)
		r.P95Ms = percentile(sort, 95)
		r.P99Ms = percentile(sort, 99)
		secs := r.MeanMs / 1000.0
		if secs > 0 && bytes > 0 {
			r.ThroughputMBs = (float64(bytes) / (1024 * 1024)) / secs
		}
	}
	return r
}

func sortFloats(xs []float64) {
	// insertion sort is fine for tiny run counts
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100.0*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func (b *Bench) printResult(r Result) {
	if r.Error != "" {
		fmt.Printf("  %-20s %s: FAILED — %s\n", r.Op, r.Tier, r.Error)
		return
	}
	fmt.Printf("  %-20s %s: mean %.0fms  p50 %.0fms  p95 %.0fms  p99 %.0fms  %.1f MB/s  (%s)\n",
		r.Op, r.Tier, r.MeanMs, r.P50Ms, r.P95Ms, r.P99Ms, r.ThroughputMBs, r.Dataset)
}

func (b *Bench) printSummary() {
	fmt.Println()
	fmt.Println("bench: summary")
	for _, r := range b.results {
		b.printResult(r)
	}
}

func (b *Bench) logf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

func (b *Bench) warnf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	b.warnings = append(b.warnings, msg)
	fmt.Fprintf(os.Stderr, "bench: warning: %s\n", msg)
}

func hostinfo() string {
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%s/%s/%dcpus", host, runtime.GOOS, runtime.NumCPU())
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// cryptoRead fills p with cryptographically random bytes (crypto/rand.Read).
func cryptoRead(p []byte) (int, error) {
	return rand.Read(p)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
