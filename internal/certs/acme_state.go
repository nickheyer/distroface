package certs

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/cache"
)

// Short lived protocol state behind the built in acme server
type acmeState interface {
	addNonce(ctx context.Context, nonce string)
	takeNonce(ctx context.Context, nonce string) bool
	order(ctx context.Context, id string) *acmeOrder
	saveOrder(ctx context.Context, o *acmeOrder)
	authz(ctx context.Context, id string) *acmeAuthz
	saveAuthz(ctx context.Context, a *acmeAuthz)
	// Flips one pending authorization to processing, false once claimed
	claimValidation(ctx context.Context, id string) bool
}

// Single process state, copies in and out so handlers never share pointers
type memoryACMEState struct {
	mu     sync.Mutex
	nonces map[string]time.Time
	orders map[string]acmeOrder
	authzs map[string]acmeAuthz
}

func newMemoryACMEState() *memoryACMEState {
	return &memoryACMEState{nonces: map[string]time.Time{}, orders: map[string]acmeOrder{}, authzs: map[string]acmeAuthz{}}
}

func (m *memoryACMEState) addNonce(_ context.Context, nonce string) {
	m.mu.Lock()
	m.gcLocked()
	m.nonces[nonce] = time.Now().Add(acmeNonceTTL)
	m.mu.Unlock()
}

func (m *memoryACMEState) takeNonce(_ context.Context, nonce string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.nonces[nonce]
	delete(m.nonces, nonce)
	return ok && time.Now().Before(exp)
}

func (m *memoryACMEState) order(_ context.Context, id string) *acmeOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok || time.Now().After(o.Expires) {
		return nil
	}
	o.Identifiers = slices.Clone(o.Identifiers)
	o.AuthzIDs = slices.Clone(o.AuthzIDs)
	return &o
}

func (m *memoryACMEState) saveOrder(_ context.Context, o *acmeOrder) {
	cp := *o
	cp.Identifiers = slices.Clone(o.Identifiers)
	cp.AuthzIDs = slices.Clone(o.AuthzIDs)
	m.mu.Lock()
	m.orders[o.ID] = cp
	m.mu.Unlock()
}

func (m *memoryACMEState) authz(_ context.Context, id string) *acmeAuthz {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.authzs[id]
	if !ok || time.Now().After(a.Expires) {
		return nil
	}
	return &a
}

func (m *memoryACMEState) saveAuthz(_ context.Context, a *acmeAuthz) {
	m.mu.Lock()
	m.authzs[a.ID] = *a
	m.mu.Unlock()
}

func (m *memoryACMEState) claimValidation(_ context.Context, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.authzs[id]
	if !ok || a.Status != "pending" {
		return false
	}
	a.Status = "processing"
	m.authzs[id] = a
	return true
}

// Drops expired nonces, orders, and authorizations
func (m *memoryACMEState) gcLocked() {
	now := time.Now()
	for k, exp := range m.nonces {
		if now.After(exp) {
			delete(m.nonces, k)
		}
	}
	for k, o := range m.orders {
		if now.After(o.Expires) {
			delete(m.orders, k)
		}
	}
	for k, a := range m.authzs {
		if now.After(a.Expires) {
			delete(m.authzs, k)
		}
	}
}

// Redis backed state so any instance can answer any step of an order
type sharedACMEState struct {
	c *cache.Client
}

const (
	acmeNoncePrefix = cache.Prefix + "acme:nonce:"
	acmeOrderPrefix = cache.Prefix + "acme:order:"
	acmeAuthzPrefix = cache.Prefix + "acme:authz:"
	acmeClaimPrefix = cache.Prefix + "acme:validating:"
)

func (s sharedACMEState) addNonce(ctx context.Context, nonce string) {
	_ = s.c.SetString(ctx, acmeNoncePrefix+nonce, "1", acmeNonceTTL)
}

// Replay protection fails closed, a lost redis means a fresh nonce
func (s sharedACMEState) takeNonce(ctx context.Context, nonce string) bool {
	_, ok, err := s.c.Take(ctx, acmeNoncePrefix+nonce)
	return err == nil && ok
}

func (s sharedACMEState) order(ctx context.Context, id string) *acmeOrder {
	var o acmeOrder
	if hit, err := s.c.Get(ctx, acmeOrderPrefix+id, &o); err != nil || !hit || time.Now().After(o.Expires) {
		return nil
	}
	return &o
}

func (s sharedACMEState) saveOrder(ctx context.Context, o *acmeOrder) {
	_ = s.c.Set(ctx, acmeOrderPrefix+o.ID, o, ttlUntil(o.Expires))
}

func (s sharedACMEState) authz(ctx context.Context, id string) *acmeAuthz {
	var a acmeAuthz
	if hit, err := s.c.Get(ctx, acmeAuthzPrefix+id, &a); err != nil || !hit || time.Now().After(a.Expires) {
		return nil
	}
	return &a
}

func (s sharedACMEState) saveAuthz(ctx context.Context, a *acmeAuthz) {
	_ = s.c.Set(ctx, acmeAuthzPrefix+a.ID, a, ttlUntil(a.Expires))
}

// One validator per authorization across instances
func (s sharedACMEState) claimValidation(ctx context.Context, id string) bool {
	a := s.authz(ctx, id)
	if a == nil || a.Status != "pending" {
		return false
	}
	won, err := s.c.SetNX(ctx, acmeClaimPrefix+id, "1", acmeOrderTTL)
	if err != nil || !won {
		return false
	}
	a.Status = "processing"
	s.saveAuthz(ctx, a)
	return true
}

// Keys die with the order, never before
func ttlUntil(t time.Time) time.Duration {
	return max(time.Until(t), time.Second)
}
