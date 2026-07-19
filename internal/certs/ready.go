package certs

import (
	"context"
	"sync"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Bounds per request status checks on cleartext redirects
const readyTTL = 5 * time.Second

type readyEntry struct {
	ready   bool
	expires time.Time
}

type readyCache struct {
	mu      sync.Mutex
	entries map[string]readyEntry
}

func (c *readyCache) get(key string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expires) {
		return false, false
	}
	return entry.ready, true
}

func (c *readyCache) put(key string, ready bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]readyEntry{}
	}
	c.entries[key] = readyEntry{ready: ready, expires: time.Now().Add(readyTTL)}
}

func (c *readyCache) clear() {
	c.mu.Lock()
	c.entries = map[string]readyEntry{}
	c.mu.Unlock()
}

// AppCertReady reports whether the primary host would serve tls,
// https only enforcement stays off until this turns true
func (e *Engine) AppCertReady(ctx context.Context) bool {
	if ready, ok := e.ready.get("app"); ok {
		return ready
	}
	ready := e.AppStatus(ctx).GetState() == v1.CertState_CERT_STATE_READY
	e.ready.put("app", ready)
	return ready
}

// PortalCertReady reports whether a portal host would serve tls
func (e *Engine) PortalCertReady(ctx context.Context, source v1.CertSource, orgID, portalID, host string) bool {
	if ready, ok := e.ready.get("portal|" + portalID); ok {
		return ready
	}
	st := e.PortalStatus(ctx, &storage.RegistryPortal{
		ID:         portalID,
		OrgID:      orgID,
		CertSource: source,
		Hostname:   bareHost(host),
	})
	ready := st.GetState() == v1.CertState_CERT_STATE_READY
	e.ready.put("portal|"+portalID, ready)
	return ready
}
