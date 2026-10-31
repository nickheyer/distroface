package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/distribution/distribution/v3"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/nickheyer/distroface/pkg/config"
)

var testDesc = v1.Descriptor{
	Digest:    digest.FromString("layer"),
	Size:      5,
	MediaType: "application/octet-stream",
}

func TestDescriptorCacheMissesUntilAttached(t *testing.T) {
	c := &DescriptorCache{}
	ctx := context.Background()
	if _, err := c.Stat(ctx, testDesc.Digest); !errors.Is(err, distribution.ErrBlobUnknown) {
		t.Fatalf("detached Stat error = %v", err)
	}
	if err := c.SetDescriptor(ctx, testDesc.Digest, testDesc); err != nil {
		t.Fatalf("detached Set error = %v", err)
	}
	scoped, err := c.RepositoryScoped("ns/app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.Stat(ctx, testDesc.Digest); !errors.Is(err, distribution.ErrBlobUnknown) {
		t.Fatalf("detached scoped Stat error = %v", err)
	}
	if err := c.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Kind() != "" {
		t.Errorf("detached kind %q", c.Kind())
	}
}

func TestMemoryDescriptorCacheResetDropsEverything(t *testing.T) {
	c := &DescriptorCache{}
	c.useMemory(10)
	ctx := context.Background()
	if c.Kind() != "memory" {
		t.Fatalf("kind = %q", c.Kind())
	}
	scoped, err := c.RepositoryScoped("ns/app")
	if err != nil {
		t.Fatal(err)
	}
	if err := scoped.SetDescriptor(ctx, testDesc.Digest, testDesc); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stat(ctx, testDesc.Digest); err != nil {
		t.Fatalf("global Stat after scoped Set = %v", err)
	}
	if _, err := scoped.Stat(ctx, testDesc.Digest); err != nil {
		t.Fatalf("scoped Stat = %v", err)
	}

	if err := c.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stat(ctx, testDesc.Digest); !errors.Is(err, distribution.ErrBlobUnknown) {
		t.Errorf("global Stat after reset = %v", err)
	}
	// Scoped views made before the reset follow it
	if _, err := scoped.Stat(ctx, testDesc.Digest); !errors.Is(err, distribution.ErrBlobUnknown) {
		t.Errorf("scoped Stat after reset = %v", err)
	}
	if err := c.SetDescriptor(ctx, testDesc.Digest, testDesc); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stat(ctx, testDesc.Digest); err != nil {
		t.Errorf("cache dead after reset: %v", err)
	}
}

func TestRedisDescriptorCacheResetFlushes(t *testing.T) {
	srv := miniredis.RunT(t)
	client, err := newRedisClient(config.RedisConfig{Addrs: []string{srv.Addr()}})
	if err != nil {
		t.Fatal(err)
	}
	c := &DescriptorCache{}
	c.useRedis(client)
	ctx := context.Background()
	if c.Kind() != "redis" {
		t.Fatalf("kind = %q", c.Kind())
	}
	scoped, err := c.RepositoryScoped("ns/app")
	if err != nil {
		t.Fatal(err)
	}
	if err := scoped.SetDescriptor(ctx, testDesc.Digest, testDesc); err != nil {
		t.Fatal(err)
	}
	if !srv.Exists("blobs::"+testDesc.Digest.String()) || !srv.Exists("repository::ns/app::blobs") {
		t.Fatalf("engine cache wrote unexpected keys: %v", srv.Keys())
	}
	if err := c.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if len(srv.Keys()) != 0 {
		t.Errorf("keys after reset: %v", srv.Keys())
	}
	if _, err := scoped.Stat(ctx, testDesc.Digest); !errors.Is(err, distribution.ErrBlobUnknown) {
		t.Errorf("scoped Stat after reset = %v", err)
	}
	if err := c.release(); err != nil {
		t.Fatal(err)
	}
	if c.Kind() != "" {
		t.Errorf("kind after release = %q", c.Kind())
	}
}
