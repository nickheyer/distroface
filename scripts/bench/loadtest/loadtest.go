// Command loadtest generates concurrent docker-registry push/pull load
// against a running distroface. Scratch discovery driver for the registry
// hardening ticket: not a product artifact, no test coverage required.
//
// Build and run:
//
//	go run ./scripts/bench/loadtest -addr 127.0.0.1:8080 -repo loadtest/img -workers 8 -ops 50
//
// It drives the real registry HTTP protocol the way the docker client does
// (POST upload session, PUT monolithic blob, PUT manifest) and reports wall
// time, throughput and per-op latency percentiles. An optional -token-cmd
// lets a script mint bearer tokens when the registry requires auth.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	addr      = flag.String("addr", "127.0.0.1:8080", "registry host:port")
	user      = flag.String("user", "", "username for token auth (empty = anonymous)")
	pass      = flag.String("pass", "", "password for token auth")
	repo      = flag.String("repo", "loadtest/img", "repository to push/pull")
	workers   = flag.Int("workers", 8, "concurrent workers")
	ops       = flag.Int("ops", 50, "operations per worker")
	layerKB   = flag.Int("layer-kb", 256, "layer size in KiB")
	manifest_ = flag.String("manifest", "schema2", "manifest media type: schema2|oci")
	layers    = flag.Int("layers", 1, "layers per image")
	tagSuffix = flag.Bool("tag-suffix", true, "append worker id to tags to avoid tag contention")
)

// ---- payload construction -------------------------------------------------

type descriptor struct {
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	Digest    string `json:"digest"`
}

// Manifest shape shared by docker schema2 and OCI image manifests
type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + fmt.Sprintf("%x", s)
}

// randomLayer returns size bytes of incompressible data plus its digest
func randomLayer(size int) ([]byte, string) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b, digestOf(b)
}

// configBlob builds a minimal image config with the given labels
func configBlob(labels map[string]string) ([]byte, string) {
	c := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"rootfs":       map[string]any{"type": "layers", "diff_ids": []string{}},
		"config":       labels,
	}
	b, _ := json.Marshal(c)
	return b, digestOf(b)
}

// ---- http client -----------------------------------------------------------

type client struct {
	http  *http.Client
	addr  string
	repo  string
	token func() string
}

func newClient(addr, repo string, token func() string) *client {
	return &client{
		http:  &http.Client{Timeout: 120 * time.Second},
		addr:  addr,
		repo:  repo,
		token: token,
	}
}

// mintToken performs the docker auth dance: GET /auth/token?service=&scope=
// with basic auth, returning the bearer token.
func mintToken(addr, user, pass, repo string) (string, error) {
	u := "http://" + addr + "/auth/token?service=distroface-registry&scope=repository:" + repo + ":pull,push"
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("token: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var tr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.Token == "" {
		return "", fmt.Errorf("token: bad response")
	}
	return tr.Token, nil
}

func (c *client) do(method, path string, body []byte, headers map[string]string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://"+c.addr+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "docker/24.0.7 go/go1.21")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.token != nil {
		req.Header.Set("Authorization", "Bearer "+c.token())
	}
	return c.http.Do(req)
}

// drain closes a response after reading (and discarding) its body
func drain(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// ---- push / pull -----------------------------------------------------------

// pushOne performs the full docker push handshake for one image: blobs then
// manifest. Returns the number of payload bytes sent.
func (c *client) pushOne(ctx context.Context, tag string, layerSize, nLayers int, mtype string) (int, error) {
	sent := 0
	layer, ldgst := randomLayer(layerSize * 1024)
	cfg, cdgst := configBlob(map[string]string{
		"org.opencontainers.image.source": "git@github.com:example/repo",
	})

	var layerDescs []descriptor
	for i := 0; i < nLayers; i++ {
		resp, err := c.do("POST", "/v2/"+c.repo+"/blobs/uploads/", nil, nil)
		if err != nil {
			return sent, err
		}
		if resp.StatusCode != http.StatusAccepted {
			drain(resp)
			return sent, fmt.Errorf("init blob upload: %s", resp.Status)
		}
		loc := resp.Header.Get("Location")
		drain(resp)

		sep := "&"
		if !strings.Contains(loc, "?") {
			sep = "?"
		}
		resp, err = c.do("PUT", loc+sep+"digest="+ldgst, layer, map[string]string{
			"Content-Type":   "application/octet-stream",
			"Content-Length": fmt.Sprintf("%d", len(layer)),
		})
		if err != nil {
			return sent, err
		}
		if resp.StatusCode/100 != 2 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			drain(resp)
			return sent, fmt.Errorf("put blob: %s %s", resp.Status, strings.TrimSpace(string(b)))
		}
		drain(resp)
		sent += len(layer)
		layerDescs = append(layerDescs, descriptor{
			MediaType: "application/vnd.docker.image.rootfs.diff.tar.gzip",
			Size:      int64(len(layer)),
			Digest:    ldgst,
		})
	}

	// config blob
	resp, err := c.do("POST", "/v2/"+c.repo+"/blobs/uploads/", nil, nil)
	if err != nil {
		return sent, err
	}
	if resp.StatusCode != http.StatusAccepted {
		drain(resp)
		return sent, fmt.Errorf("init config upload: %s", resp.Status)
	}
	loc := resp.Header.Get("Location")
	drain(resp)

	sep := "&"
	if !strings.Contains(loc, "?") {
		sep = "?"
	}
	resp, err = c.do("PUT", loc+sep+"digest="+cdgst, cfg, map[string]string{
		"Content-Type":   "application/octet-stream",
		"Content-Length": fmt.Sprintf("%d", len(cfg)),
	})
	if err != nil {
		return sent, err
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		drain(resp)
		return sent, fmt.Errorf("put config: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	drain(resp)
	sent += len(cfg)

	mt := "application/vnd.docker.distribution.manifest.v2+json"
	if mtype == "oci" {
		mt = "application/vnd.oci.image.manifest.v1+json"
	}
	m := manifest{
		SchemaVersion: 2,
		MediaType:     mt,
		Config: descriptor{
			MediaType: "application/vnd.docker.container.image.v1+json",
			Size:      int64(len(cfg)),
			Digest:    cdgst,
		},
		Layers: layerDescs,
	}
	mb, _ := json.Marshal(m)

	resp, err = c.do("PUT", "/v2/"+c.repo+"/manifests/"+tag, mb, map[string]string{"Content-Type": mt})
	if err != nil {
		return sent, err
	}
	drain(resp)
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		drain(resp)
		return sent, fmt.Errorf("put manifest: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return sent + len(mb), nil
}

// pullOne fetches the manifest by tag then every blob it references, the way
// a docker pull runs. Returns the number of payload bytes received.
func (c *client) pullOne(ctx context.Context, tag string, mtype string) (int, error) {
	got := 0
	mt := "application/vnd.docker.distribution.manifest.v2+json"
	if mtype == "oci" {
		mt = "application/vnd.oci.image.manifest.v1+json"
	}
	resp, err := c.do("GET", "/v2/"+c.repo+"/manifests/"+tag, nil, map[string]string{"Accept": mt})
	if err != nil {
		return got, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		drain(resp)
		return got, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return got, fmt.Errorf("get manifest: %s", resp.Status)
	}
	got += len(body)

	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return got, fmt.Errorf("parse manifest: %w", err)
	}
	for _, d := range append(m.Layers, m.Config) {
		resp, err := c.do("GET", "/v2/"+c.repo+"/blobs/"+d.Digest, nil, nil)
		if err != nil {
			return got, err
		}
		n, err := io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return got, err
		}
		if resp.StatusCode/100 != 2 {
			return got, fmt.Errorf("get blob %s: %s", d.Digest, resp.Status)
		}
		got += int(n)
	}
	return got, nil
}

// ---- measurement -----------------------------------------------------------

type opResult struct {
	latency time.Duration
	bytes   int
	err     error
}

func percentiles(ds []time.Duration) (p50, p90, p99 time.Duration) {
	if len(ds) == 0 {
		return 0, 0, 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	pick := func(f float64) time.Duration {
		i := int(float64(len(ds)-1) * f)
		return ds[i]
	}
	return pick(0.50), pick(0.90), pick(0.99)
}

// runLoad drives nWorkers goroutines, each doing nOps push-then-pull cycles,
// collecting per-op latencies. Push and pull are timed separately.
func runLoad(ctx context.Context, c *client, nWorkers, nOps, layerSize, nLayers int, mtype string, tagPerWorker bool) (pushes, pulls []opResult, errs []string) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		pushRes  []opResult
		pullRes  []opResult
		errList  []string
	)
	for w := 0; w < nWorkers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tag := "latest"
			if tagPerWorker {
				tag = fmt.Sprintf("w%d", id)
			}
			for i := 0; i < nOps; i++ {
				start := time.Now()
				n, err := c.pushOne(ctx, tag, layerSize, nLayers, mtype)
				el := time.Since(start)
				mu.Lock()
				pushRes = append(pushRes, opResult{el, n, err})
				if err != nil {
					errList = append(errList, fmt.Sprintf("push w%d op%d: %v", id, i, err))
				}
				mu.Unlock()
				if err != nil {
					continue
				}

				start = time.Now()
				n, err = c.pullOne(ctx, tag, mtype)
				el = time.Since(start)
				mu.Lock()
				pullRes = append(pullRes, opResult{el, n, err})
				if err != nil {
					errList = append(errList, fmt.Sprintf("pull w%d op%d: %v", id, i, err))
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	return pushRes, pullRes, errList
}

func summarize(name string, res []opResult, wall time.Duration) {
	var (
		totalBytes int64
		fails      int
		lats       []time.Duration
	)
	for _, r := range res {
		if r.err != nil {
			fails++
			continue
		}
		totalBytes += int64(r.bytes)
		lats = append(lats, r.latency)
	}
	p50, p90, p99 := percentiles(lats)
	secs := wall.Seconds()
	mbps := float64(totalBytes) / secs / (1 << 20)
	fmt.Printf("%-6s ops=%4d ok=%4d fail=%3d  wall=%6.2fs  throughput=%7.1f ops/s  %8.2f MiB/s  p50=%7.1fms p90=%7.1fms p99=%7.1fms\n",
		name, len(res), len(res)-fails, fails, secs, float64(len(res))/secs, mbps,
		float64(p50.Microseconds())/1000, float64(p90.Microseconds())/1000, float64(p99.Microseconds())/1000)
}

// ---- main ------------------------------------------------------------------

func main() {
	flag.Parse()

	var token func() string
	if *user != "" {
		t, err := mintToken(*addr, *user, *pass, *repo)
		if err != nil {
			fmt.Fprintln(os.Stderr, "token mint failed:", err)
			os.Exit(1)
		}
		token = func() string { return t }
	}

	c := newClient(*addr, *repo, token)
	ctx := context.Background()

	// reachability probe
	resp, err := c.do("GET", "/v2/", nil, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "registry unreachable:", err)
		os.Exit(1)
	}
	drain(resp)
	fmt.Printf("registry %s reachable (status %d)\n", *addr, resp.StatusCode)

	// warmup: push one image so the pull phase has something to read
	if _, err := c.pushOne(ctx, "warm", *layerKB, *layers, *manifest_); err != nil {
		fmt.Fprintln(os.Stderr, "warmup push failed:", err)
		os.Exit(1)
	}

	fmt.Printf("load: %d workers x %d ops, layer=%dKiB x%d, manifest=%s, repo=%s\n",
		*workers, *ops, *layerKB, *layers, *manifest_, *repo)

	start := time.Now()
	pushes, pulls, errs := runLoad(ctx, c, *workers, *ops, *layerKB, *layers, *manifest_, *tagSuffix)
	wall := time.Since(start)

	summarize("push", pushes, wall)
	summarize("pull", pulls, wall)
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "\nerrors (first 10):")
		for i, e := range errs {
			if i >= 10 {
				break
			}
			fmt.Fprintln(os.Stderr, " ", e)
		}
		if len(errs) > 10 {
			fmt.Fprintf(os.Stderr, "  ... and %d more\n", len(errs)-10)
		}
		os.Exit(2)
	}
}
