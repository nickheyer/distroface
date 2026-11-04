package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/distribution/distribution/v3/health"
	"github.com/opencontainers/go-digest"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/cache/cachetest"
	"github.com/nickheyer/distroface/pkg/config"
)

// Real distribution app, no cert bundle means no auth
func startEngine(t *testing.T, reg config.RegistryConfig) (*Engine, string) {
	t.Helper()
	return startEngineOpts(t, Options{RegistryConfig: reg})
}

func startEngineOpts(t *testing.T, opts Options) (*Engine, string) {
	t.Helper()
	if opts.StoragePath == "" {
		opts.StoragePath = t.TempDir()
	}
	opts.Addr = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	eng, err := New(ctx, opts)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	srv := httptest.NewServer(eng.Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = eng.Close()
		cancel()
	})
	return eng, srv.URL
}

// Location headers are relative, resolve like a client
func resolveLocation(t *testing.T, base, location string) string {
	t.Helper()
	if strings.HasPrefix(location, "http") {
		t.Errorf("Location %q is absolute, relative urls were requested", location)
	}
	b, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	l, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	return b.ResolveReference(l).String()
}

// One request with an optional bearer token, body drained
func call(t *testing.T, method, url, token string, body []byte, contentType string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

func pushBlob(t *testing.T, base string, payload []byte) digest.Digest {
	t.Helper()
	return pushBlobAs(t, base, "", payload)
}

func pushBlobAs(t *testing.T, base, token string, payload []byte) digest.Digest {
	t.Helper()
	dgst := digest.FromBytes(payload)
	resp, _ := call(t, http.MethodPost, base+"/v2/ns/app/blobs/uploads/", token, nil, "application/octet-stream")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start upload status = %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatal("upload start returned no Location")
	}
	resp, _ = call(t, http.MethodPut, fmt.Sprintf("%s&digest=%s", resolveLocation(t, base, location), dgst), token, payload, "application/octet-stream")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("finish upload status = %d", resp.StatusCode)
	}
	return dgst
}

// Tags a bare manifest so the catalog lists the repo
func pushManifest(t *testing.T, base string, cfgDgst digest.Digest, cfgSize int) {
	t.Helper()
	pushManifestAs(t, base, "", cfgDgst, cfgSize)
}

func pushManifestAs(t *testing.T, base, token string, cfgDgst digest.Digest, cfgSize int) digest.Digest {
	t.Helper()
	manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
		`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"%s","size":%d},"layers":[]}`, cfgDgst, cfgSize)
	resp, body := call(t, http.MethodPut, base+"/v2/ns/app/manifests/latest", token, []byte(manifest), "application/vnd.oci.image.manifest.v1+json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("manifest put = %d %s", resp.StatusCode, body)
	}
	dgst, err := digest.Parse(resp.Header.Get("Docker-Content-Digest"))
	if err != nil {
		t.Fatalf("Docker-Content-Digest %q: %v", resp.Header.Get("Docker-Content-Digest"), err)
	}
	return dgst
}

// Engine behind token auth, tokens come straight from the signer
func startTokenEngine(t *testing.T) (*auth.TokenService, string) {
	t.Helper()
	ts, err := auth.NewTokenService(t.TempDir(), "distroface", "distroface-registry", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, base := startEngineOpts(t, Options{CertPath: ts.CertPath()})
	return ts, base
}

func mintToken(t *testing.T, ts *auth.TokenService, access ...*auth.ResourceActions) string {
	t.Helper()
	tok, err := ts.SignTokenAudience("distroface-registry", time.Hour, "admin", access)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func repoActions(name string, actions ...string) *auth.ResourceActions {
	return &auth.ResourceActions{Type: "repository", Name: name, Actions: actions}
}

func headBlob(t *testing.T, base string, dgst digest.Digest) int {
	t.Helper()
	resp, err := http.Head(base + "/v2/ns/app/blobs/" + dgst.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Removes a blob and repo link like the vacuum
func sweepBlob(t *testing.T, root string, dgst digest.Digest) {
	t.Helper()
	algo, hex := dgst.Algorithm().String(), dgst.Encoded()
	for _, dir := range []string{
		filepath.Join(root, "docker", "registry", "v2", "blobs", algo, hex[:2], hex),
		filepath.Join(root, "docker", "registry", "v2", "repositories", "ns", "app", "_layers", algo, hex),
	} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEngineRefusesBrokenConfig(t *testing.T) {
	ctx := context.Background()
	cases := map[string]Options{
		"missing cert bundle": {RegistryConfig: config.RegistryConfig{StoragePath: t.TempDir()}, CertPath: filepath.Join(t.TempDir(), "none.pem"), Addr: "127.0.0.1:0"},
		"bad manifest regex":  {RegistryConfig: config.RegistryConfig{StoragePath: t.TempDir(), ManifestURLsAllow: []string{"["}}},
	}
	for name, opts := range cases {
		eng, err := New(ctx, opts)
		if err == nil {
			_ = eng.Close()
			t.Errorf("%s: engine started", name)
			continue
		}
		if strings.Contains(err.Error(), "panic") {
			t.Errorf("%s: raw panic text in error %q", name, err)
		}
	}
	if engineCache.Kind() != "" {
		t.Errorf("failed start left the cache attached as %q", engineCache.Kind())
	}
}

func TestCacheResetAfterSweep(t *testing.T) {
	cases := map[string]func(t *testing.T) Options{
		"memory": func(*testing.T) Options { return Options{} },
		"redis": func(t *testing.T) Options {
			client, _ := cachetest.New(t)
			return Options{Cache: client}
		},
	}
	for name, optsFor := range cases {
		t.Run(name, func(t *testing.T) {
			opts := optsFor(t)
			opts.StoragePath = t.TempDir()
			eng, base := startEngineOpts(t, opts)
			if eng.cache.Kind() != name {
				t.Fatalf("cache kind = %q", eng.cache.Kind())
			}

			dgst := pushBlob(t, base, []byte("layer bytes"))
			if status := headBlob(t, base, dgst); status != http.StatusOK {
				t.Fatalf("HEAD after push = %d", status)
			}

			// The cache still answers for the swept blob
			sweepBlob(t, opts.StoragePath, dgst)
			if status := headBlob(t, base, dgst); status != http.StatusOK {
				t.Fatalf("HEAD after sweep = %d, the cache no longer masks swept blobs", status)
			}
			if err := eng.ResetCache(context.Background()); err != nil {
				t.Fatal(err)
			}
			if status := headBlob(t, base, dgst); status != http.StatusNotFound {
				t.Fatalf("HEAD after reset = %d", status)
			}

			// The cache is alive again and refills from disk
			dgst = pushBlob(t, base, []byte("second layer"))
			if status := headBlob(t, base, dgst); status != http.StatusOK {
				t.Fatalf("HEAD after second push = %d", status)
			}
		})
	}
}

func TestGateFreezesUploadsNotReads(t *testing.T) {
	eng, base := startEngine(t, config.RegistryConfig{})

	dgst := pushBlob(t, base, []byte("layer bytes"))
	if err := eng.Gate.Freeze(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer eng.Gate.Thaw()

	resp, err := http.Post(base+"/v2/ns/app/blobs/uploads/", "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(`"UNAVAILABLE"`)) || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("upload during freeze = %d %s %v", resp.StatusCode, body, resp.Header)
	}
	if status := headBlob(t, base, dgst); status != http.StatusOK {
		t.Fatalf("HEAD during freeze = %d", status)
	}
}

func TestCatalogListsRepositories(t *testing.T) {
	_, base := startEngine(t, config.RegistryConfig{})

	cfgBytes := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	pushManifest(t, base, pushBlob(t, base, cfgBytes), len(cfgBytes))

	// A zero cap used to make this list empty
	resp, err := http.Get(base + "/v2/_catalog?n=50")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ns/app"`)) {
		t.Fatalf("catalog = %d %s", resp.StatusCode, body)
	}
}

func TestHealthGateBouncesFailingStorage(t *testing.T) {
	eng, base := startEngine(t, config.RegistryConfig{})
	ctx := context.Background()
	if failing := eng.Health(ctx); len(failing) != 0 {
		t.Fatalf("fresh engine reports %v", failing)
	}
	resp, err := http.Get(base + "/v2/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthy base route = %d", resp.StatusCode)
	}

	eng.checks.Register("test_disk", health.CheckFunc(func(context.Context) error { return errors.New("disk gone") }))
	if failing := eng.Health(ctx); failing["test_disk"] != "disk gone" {
		t.Fatalf("Health() = %v", failing)
	}
	resp, err = http.Get(base + "/v2/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(`"UNAVAILABLE"`)) {
		t.Fatalf("unhealthy base route = %d %s", resp.StatusCode, body)
	}
}

// Distribution honors the catalog and delete claims the token endpoint mints
func TestTokenAuthCatalogAndDelete(t *testing.T) {
	ts, base := startTokenEngine(t)
	writer := mintToken(t, ts, repoActions("ns/app", "pull", "push"))
	cfgBytes := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	manifestDgst := pushManifestAs(t, base, writer, pushBlobAs(t, base, writer, cfgBytes), len(cfgBytes))

	// The catalog challenges for its own scope and opens to a token carrying it
	resp, _ := call(t, http.MethodGet, base+"/v2/_catalog", "", nil, "")
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "registry:catalog:*") {
		t.Fatalf("anonymous catalog = %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if resp, _ = call(t, http.MethodGet, base+"/v2/_catalog", writer, nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("repository token listed the catalog: %d", resp.StatusCode)
	}
	catalog := mintToken(t, ts, &auth.ResourceActions{Type: "registry", Name: "catalog", Actions: []string{"*"}})
	resp, body := call(t, http.MethodGet, base+"/v2/_catalog", catalog, nil, "")
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"ns/app"`)) {
		t.Fatalf("catalog = %d %s", resp.StatusCode, body)
	}

	// Deleting needs the delete action and then works by digest
	target := base + "/v2/ns/app/manifests/" + manifestDgst.String()
	resp, _ = call(t, http.MethodDelete, target, writer, nil, "")
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "repository:ns/app:delete") {
		t.Fatalf("delete without the action = %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	deleter := mintToken(t, ts, repoActions("ns/app", "pull", "delete"))
	if resp, body = call(t, http.MethodDelete, target, deleter, nil, ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("delete = %d %s", resp.StatusCode, body)
	}
	if resp, _ = call(t, http.MethodGet, target, writer, nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("manifest after delete = %d", resp.StatusCode)
	}
	if resp, _ = call(t, http.MethodGet, base+"/v2/ns/app/manifests/latest", writer, nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("tag after delete = %d", resp.StatusCode)
	}
}

// Catalog pages resume past dotted and dashed neighbors
func TestCatalogPagesPastDottedNeighbors(t *testing.T) {
	_, base := startEngine(t, config.RegistryConfig{})
	cfgBytes := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	cfgDgst := digest.FromBytes(cfgBytes)
	repos := []string{"ns/app", "ns/app-v2", "ns/app.old", "ns-old/app", "nsx/app"}
	for _, repo := range repos {
		resp, _ := call(t, http.MethodPost, base+"/v2/"+repo+"/blobs/uploads/", "", nil, "application/octet-stream")
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s start upload = %d", repo, resp.StatusCode)
		}
		target := fmt.Sprintf("%s&digest=%s", resolveLocation(t, base, resp.Header.Get("Location")), cfgDgst)
		if resp, _ = call(t, http.MethodPut, target, "", cfgBytes, "application/octet-stream"); resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s finish upload = %d", repo, resp.StatusCode)
		}
		manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
			`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"%s","size":%d},"layers":[]}`, cfgDgst, len(cfgBytes))
		resp, body := call(t, http.MethodPut, base+"/v2/"+repo+"/manifests/latest", "", []byte(manifest), "application/vnd.oci.image.manifest.v1+json")
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s manifest put = %d %s", repo, resp.StatusCode, body)
		}
	}

	var got []string
	next := base + "/v2/_catalog?n=1"
	for next != "" {
		resp, body := call(t, http.MethodGet, next, "", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("catalog page = %d %s", resp.StatusCode, body)
		}
		var page struct {
			Repositories []string `json:"repositories"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("catalog page %s: %v", body, err)
		}
		got = append(got, page.Repositories...)
		if len(got) > len(repos) {
			t.Fatalf("catalog never ends: %v", got)
		}
		next = ""
		if link := resp.Header.Get("Link"); link != "" {
			target, _, _ := strings.Cut(link, ";")
			next = resolveLocation(t, base, strings.Trim(strings.TrimSpace(target), "<>"))
		}
	}
	if strings.Join(got, " ") != strings.Join(repos, " ") {
		t.Errorf("paged catalog = %v, want %v", got, repos)
	}
}
