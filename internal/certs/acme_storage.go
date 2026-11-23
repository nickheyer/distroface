package certs

import (
	"context"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/nickheyer/distroface/internal/cache"
	"github.com/nickheyer/distroface/internal/db/stores"
)

// Bounds a crashed holder across instances
const storageLeaseTTL = 2 * time.Minute

// Acme client state kept in the database
type certStorage struct {
	store  *stores.Store
	shared func() *cache.Client

	mu   sync.Mutex
	held map[string]*heldLock
}

type heldLock struct {
	done  chan struct{}
	lease *cache.Lock
	stop  func()
}

var _ certmagic.Storage = (*certStorage)(nil)

func newCertStorage(store *stores.Store, shared func() *cache.Client) *certStorage {
	return &certStorage{store: store, shared: shared, held: map[string]*heldLock{}}
}

func (s *certStorage) Store(ctx context.Context, key string, value []byte) error {
	return s.store.PutACMECacheEntry(ctx, key, value)
}

func (s *certStorage) Load(ctx context.Context, key string) ([]byte, error) {
	data, err := s.store.GetACMECacheEntry(ctx, key)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fs.ErrNotExist
	}
	return data, nil
}

// Removes the key and everything nested under it
func (s *certStorage) Delete(ctx context.Context, key string) error {
	if err := s.store.DeleteACMECacheEntry(ctx, key); err != nil {
		return err
	}
	return s.store.DeleteACMECachePrefix(ctx, key+"/")
}

func (s *certStorage) Exists(ctx context.Context, key string) bool {
	_, _, found, err := s.store.StatACMECacheEntry(ctx, key)
	return err == nil && found
}

func (s *certStorage) List(ctx context.Context, path string, recursive bool) ([]string, error) {
	prefix := strings.TrimSuffix(path, "/") + "/"
	keys, err := s.store.ListACMECacheKeys(ctx, prefix)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fs.ErrNotExist
	}
	if recursive {
		return keys, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		child, _, _ := strings.Cut(strings.TrimPrefix(k, prefix), "/")
		if !seen[child] {
			seen[child] = true
			out = append(out, prefix+child)
		}
	}
	return out, nil
}

func (s *certStorage) Stat(ctx context.Context, key string) (certmagic.KeyInfo, error) {
	size, modified, found, err := s.store.StatACMECacheEntry(ctx, key)
	if err != nil {
		return certmagic.KeyInfo{}, err
	}
	if found {
		return certmagic.KeyInfo{Key: key, Modified: modified, Size: size, IsTerminal: true}, nil
	}
	children, err := s.store.ListACMECacheKeys(ctx, key+"/")
	if err != nil {
		return certmagic.KeyInfo{}, err
	}
	if len(children) == 0 {
		return certmagic.KeyInfo{}, fs.ErrNotExist
	}
	return certmagic.KeyInfo{Key: key}, nil
}

// One holder per name in process and across instances
func (s *certStorage) Lock(ctx context.Context, name string) error {
	held := &heldLock{done: make(chan struct{})}
	for {
		s.mu.Lock()
		other, busy := s.held[name]
		if !busy {
			s.held[name] = held
		}
		s.mu.Unlock()
		if !busy {
			break
		}
		select {
		case <-other.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		lease, ok, err := s.shared().TryLock(ctx, "acme:"+name, storageLeaseTTL)
		if err == nil && ok {
			held.lease = lease
			held.stop = lease.KeepAlive(context.WithoutCancel(ctx))
			return nil
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			s.release(name)
			return ctx.Err()
		}
	}
}

func (s *certStorage) Unlock(ctx context.Context, name string) error {
	held := s.release(name)
	if held == nil || held.lease == nil {
		return nil
	}
	held.stop()
	return held.lease.Unlock(ctx)
}

func (s *certStorage) release(name string) *heldLock {
	s.mu.Lock()
	held := s.held[name]
	delete(s.held, name)
	s.mu.Unlock()
	if held != nil {
		close(held.done)
	}
	return held
}
