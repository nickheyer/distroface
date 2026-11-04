package registry

import (
	"context"
	"sync"

	"github.com/distribution/distribution/v3"
	registrymiddleware "github.com/distribution/distribution/v3/registry/middleware/registry"
	"github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/cache"
	memorycache "github.com/distribution/distribution/v3/registry/storage/cache/memory"
	rediscache "github.com/distribution/distribution/v3/registry/storage/cache/redis"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"

	dfcache "github.com/nickheyer/distroface/internal/cache"
)

// Keys the distribution redis descriptor cache writes, no app prefix
var redisCachePatterns = []string{"blobs::*", "repository::*"}

// Engine side descriptor cache a sweep can drop whole
type DescriptorCache struct {
	mu     sync.RWMutex
	inner  cache.BlobDescriptorCacheProvider
	kind   string
	size   int
	shared *dfcache.Client
}

// Captured by the engine once, engines attach backends
var engineCache = &DescriptorCache{}

func init() {
	if err := registrymiddleware.RegisterOptions(storage.BlobDescriptorCacheProvider(engineCache)); err != nil {
		panic(err)
	}
}

func (c *DescriptorCache) useMemory(size int) {
	if size <= 0 {
		size = memorycache.DefaultSize
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kind, c.size, c.shared = "memory", size, nil
	c.inner = memorycache.NewInMemoryBlobDescriptorCacheProvider(size)
}

// The pool belongs to the shared client, the engine only borrows it
func (c *DescriptorCache) useRedis(client *dfcache.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kind, c.size, c.shared = "redis", 0, client
	c.inner = rediscache.NewRedisBlobDescriptorCacheProvider(client.Redis())
}

// Detaches the backend, lookups miss until the next attach
func (c *DescriptorCache) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inner, c.kind, c.size, c.shared = nil, "", 0, nil
}

func (c *DescriptorCache) current() cache.BlobDescriptorCacheProvider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inner
}

// Backend name for logs, empty before an engine attaches
func (c *DescriptorCache) Kind() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.kind
}

// Forgets every descriptor, run once blobs left disk.
//
// GC ordering contract: the collector calls Reset strictly after Thaw,
// and Reset is atomic with respect to concurrent readers — the backend
// swap happens under the write lock, so a reader either sees the old or
// the new provider, never a torn state, and no stale descriptor can
// outlive the reset. Do not weaken the locking here; the freeze →
// drain → sweep → thaw → reset sequence in internal/admin/gc.go
// depends on this being complete before the next write lands.
func (c *DescriptorCache) Reset(ctx context.Context) error {
	c.mu.Lock()
	shared := c.shared
	if shared == nil && c.inner != nil {
		c.inner = memorycache.NewInMemoryBlobDescriptorCacheProvider(c.size)
	}
	c.mu.Unlock()
	if shared == nil {
		return nil
	}
	return shared.DeleteMatching(ctx, redisCachePatterns...)
}

// Misses until attached, lookups fall through to disk
func (c *DescriptorCache) Stat(ctx context.Context, dgst digest.Digest) (v1.Descriptor, error) {
	if p := c.current(); p != nil {
		return p.Stat(ctx, dgst)
	}
	return v1.Descriptor{}, distribution.ErrBlobUnknown
}

func (c *DescriptorCache) SetDescriptor(ctx context.Context, dgst digest.Digest, desc v1.Descriptor) error {
	if p := c.current(); p != nil {
		return p.SetDescriptor(ctx, dgst, desc)
	}
	return nil
}

func (c *DescriptorCache) Clear(ctx context.Context, dgst digest.Digest) error {
	if p := c.current(); p != nil {
		return p.Clear(ctx, dgst)
	}
	return nil
}

// Names arrive parsed, the backend rechecks them per call
func (c *DescriptorCache) RepositoryScoped(repo string) (distribution.BlobDescriptorService, error) {
	return &scopedDescriptorCache{repo: repo, parent: c}, nil
}

// Resolves the live backend on every call
type scopedDescriptorCache struct {
	repo   string
	parent *DescriptorCache
}

func (s *scopedDescriptorCache) scoped() (distribution.BlobDescriptorService, error) {
	p := s.parent.current()
	if p == nil {
		return nil, nil
	}
	return p.RepositoryScoped(s.repo)
}

func (s *scopedDescriptorCache) Stat(ctx context.Context, dgst digest.Digest) (v1.Descriptor, error) {
	svc, err := s.scoped()
	if err != nil {
		return v1.Descriptor{}, err
	}
	if svc == nil {
		return v1.Descriptor{}, distribution.ErrBlobUnknown
	}
	return svc.Stat(ctx, dgst)
}

func (s *scopedDescriptorCache) SetDescriptor(ctx context.Context, dgst digest.Digest, desc v1.Descriptor) error {
	svc, err := s.scoped()
	if err != nil || svc == nil {
		return err
	}
	return svc.SetDescriptor(ctx, dgst, desc)
}

func (s *scopedDescriptorCache) Clear(ctx context.Context, dgst digest.Digest) error {
	svc, err := s.scoped()
	if err != nil || svc == nil {
		return err
	}
	return svc.Clear(ctx, dgst)
}
