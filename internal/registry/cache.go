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
	"github.com/redis/go-redis/v9"
)

// Engine side descriptor cache a sweep can drop whole
type DescriptorCache struct {
	mu     sync.RWMutex
	inner  cache.BlobDescriptorCacheProvider
	kind   string
	size   int
	client redis.UniversalClient
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
	c.kind, c.size, c.client = "memory", size, nil
	c.inner = memorycache.NewInMemoryBlobDescriptorCacheProvider(size)
}

func (c *DescriptorCache) useRedis(client redis.UniversalClient) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kind, c.size, c.client = "redis", 0, client
	c.inner = rediscache.NewRedisBlobDescriptorCacheProvider(client)
}

// Detaches the backend and closes the redis pool
func (c *DescriptorCache) release() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if c.client != nil {
		err = c.client.Close()
	}
	c.inner, c.kind, c.size, c.client = nil, "", 0, nil
	return err
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

// Forgets every descriptor, run once blobs left disk
func (c *DescriptorCache) Reset(ctx context.Context) error {
	c.mu.Lock()
	client := c.client
	if client == nil && c.inner != nil {
		c.inner = memorycache.NewInMemoryBlobDescriptorCacheProvider(c.size)
	}
	c.mu.Unlock()
	if client == nil {
		return nil
	}
	return flushRedisCache(ctx, client)
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
