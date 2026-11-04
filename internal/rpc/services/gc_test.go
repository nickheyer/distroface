package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/db/stores/storetest"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

func writeBlob(t *testing.T, root, hex string, size int) {
	t.Helper()
	dir := filepath.Join(root, "docker", "registry", "v2", "blobs", "sha256", hex[:2], hex)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func linkBlob(t *testing.T, root, ns, repo, kind, hex string) {
	t.Helper()
	repoDir := filepath.Join(root, "docker", "registry", "v2", "repositories", ns, repo)
	for _, dir := range []string{filepath.Join(repoDir, "_manifests"), filepath.Join(repoDir, kind, "sha256", hex)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repoDir, kind, "sha256", hex, "link"), []byte("sha256:"+hex), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryUsageAttributesBlobsPerNamespace(t *testing.T) {
	root := t.TempDir()
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	writeBlob(t, root, a, 10)
	writeBlob(t, root, b, 20)
	linkBlob(t, root, "ns1", "app", "_layers", a)
	linkBlob(t, root, "ns1", "app", filepath.Join("_manifests", "revisions"), b)
	linkBlob(t, root, "ns2", "other", "_layers", a)
	// A directory without _manifests is not a repository
	if err := os.MkdirAll(filepath.Join(root, "docker", "registry", "v2", "repositories", "ns3", "junk"), 0o755); err != nil {
		t.Fatal(err)
	}

	total, entries, err := registryUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if total != 30 {
		t.Errorf("total = %d, want 30", total)
	}
	if len(entries) != 2 || entries[0].Name != "ns1" || entries[0].Bytes != 30 || entries[0].Count != 1 ||
		entries[1].Name != "ns2" || entries[1].Bytes != 10 || entries[1].Count != 1 {
		t.Errorf("entries = %v", entries)
	}

	total, entries, err = registryUsage(context.Background(), t.TempDir())
	if err != nil || total != 0 || len(entries) != 0 {
		t.Errorf("empty layout = %d %v %v", total, entries, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := registryUsage(cancelled, root); err == nil {
		t.Error("cancelled walk returned no error")
	}
}

func TestStorageUsageServesStaleAndRefreshesOnce(t *testing.T) {
	store := storetest.New(t)
	s := NewGCService(nil, store, t.TempDir(), nil, logger.New())
	var calls atomic.Int32
	block := make(chan struct{})
	s.usageWalk = func(ctx context.Context, root string) (int64, []*v1.StorageUsageEntry, error) {
		n := calls.Add(1)
		if n > 1 {
			<-block
		}
		return int64(n), nil, nil
	}
	ctx := context.Background()
	get := func() int64 {
		t.Helper()
		resp, err := s.GetStorageUsage(ctx, &v1.GetStorageUsageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return resp.RegistryBytes
	}

	// A cold service walks on the calling goroutine once
	if got := get(); got != 1 {
		t.Fatalf("first usage = %d", got)
	}
	if got := get(); got != 1 || calls.Load() != 1 {
		t.Fatalf("fresh usage = %d after %d walks", got, calls.Load())
	}

	// Stale numbers come back at once while exactly one refresh runs
	s.usageMu.Lock()
	s.usageAt = time.Now().Add(-time.Hour)
	s.usageMu.Unlock()
	for range 3 {
		done := make(chan int64, 1)
		go func() { done <- get() }()
		select {
		case got := <-done:
			if got != 1 {
				t.Fatalf("stale usage = %d", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("stale read waited on the walk")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("%d walks started for three stale reads", calls.Load())
	}

	close(block)
	deadline = time.Now().Add(2 * time.Second)
	for get() != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := get(); got != 2 {
		t.Errorf("usage after refresh = %d", got)
	}

	// Warm walks once more only when nothing is running
	s.usageMu.Lock()
	s.usageAt = time.Now().Add(-time.Hour)
	s.usageMu.Unlock()
	s.Warm()
	deadline = time.Now().Add(2 * time.Second)
	for get() != 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := get(); got != 3 || calls.Load() != 3 {
		t.Errorf("usage after warm = %d after %d walks", got, calls.Load())
	}
}
