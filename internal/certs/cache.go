package certs

import (
	"context"

	"github.com/nickheyer/distroface/internal/db/stores"
	"golang.org/x/crypto/acme/autocert"
)

// Autocert cache persisted in sqlite so certs survive rebuilds
type dbCache struct {
	store *stores.Store
}

var _ autocert.Cache = dbCache{}

func (c dbCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := c.store.GetACMECacheEntry(ctx, key)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, autocert.ErrCacheMiss
	}
	return data, nil
}

func (c dbCache) Put(ctx context.Context, key string, data []byte) error {
	return c.store.PutACMECacheEntry(ctx, key, data)
}

func (c dbCache) Delete(ctx context.Context, key string) error {
	return c.store.DeleteACMECacheEntry(ctx, key)
}
