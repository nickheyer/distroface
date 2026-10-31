package registry

import (
	"bytes"
	"context"
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

	"github.com/alicebob/miniredis/v2"
	"github.com/distribution/distribution/v3/health"
	"github.com/opencontainers/go-digest"

	"github.com/nickheyer/distroface/pkg/config"
)

// Real distribution app, no cert bundle means no auth
func startEngine(t *testing.T, reg config.RegistryConfig) (*Engine, string) {
	t.Helper()
	if reg.StoragePath == "" {
		reg.StoragePath = t.TempDir()
	}
	ctx, cancel := context.WithCancel(context.Background())
	eng, err := New(ctx, Options{RegistryConfig: reg, Addr: "127.0.0.1:0"})
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

func pushBlob(t *testing.T, base string, payload []byte) digest.Digest {
	t.Helper()
	dgst := digest.FromBytes(payload)
	resp, err := http.Post(base+"/v2/ns/app/blobs/uploads/", "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start upload status = %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatal("upload start returned no Location")
	}
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s&digest=%s", resolveLocation(t, base, location), dgst), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("finish upload status = %d", resp.StatusCode)
	}
	return dgst
}

// Tags a bare manifest so the catalog lists the repo
func pushManifest(t *testing.T, base string, cfgDgst digest.Digest, cfgSize int) {
	t.Helper()
	manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
		`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"%s","size":%d},"layers":[]}`, cfgDgst, cfgSize)
	req, err := http.NewRequest(http.MethodPut, base+"/v2/ns/app/manifests/latest", bytes.NewReader([]byte(manifest)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("manifest put = %d %s", resp.StatusCode, body)
	}
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
		"missing redis ca": {RegistryConfig: config.RegistryConfig{StoragePath: t.TempDir(), Redis: config.RedisConfig{
			Addrs: []string{"127.0.0.1:1"}, TLS: true, TLSCAFile: filepath.Join(t.TempDir(), "none.pem"),
		}}},
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
	cases := map[string]func(t *testing.T) config.RegistryConfig{
		"memory": func(*testing.T) config.RegistryConfig { return config.RegistryConfig{} },
		"redis": func(t *testing.T) config.RegistryConfig {
			return config.RegistryConfig{Redis: config.RedisConfig{Addrs: []string{miniredis.RunT(t).Addr()}}}
		},
	}
	for name, cfgFor := range cases {
		t.Run(name, func(t *testing.T) {
			reg := cfgFor(t)
			reg.StoragePath = t.TempDir()
			eng, base := startEngine(t, reg)
			if eng.cache.Kind() != name {
				t.Fatalf("cache kind = %q", eng.cache.Kind())
			}

			dgst := pushBlob(t, base, []byte("layer bytes"))
			if status := headBlob(t, base, dgst); status != http.StatusOK {
				t.Fatalf("HEAD after push = %d", status)
			}

			// The cache still answers for the swept blob
			sweepBlob(t, reg.StoragePath, dgst)
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
