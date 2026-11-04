package auth

import (
	"context"
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/cache/cachetest"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

func TestIdentityCacheRoundTripAndInvalidation(t *testing.T) {
	client, srv := cachetest.New(t)
	m := &Manager{cache: client}
	peer := &Manager{cache: cachetest.Attach(t, srv)}
	ctx := context.Background()
	user := &AuthenticatedUser{
		ID: "u1", Username: "ann", Email: "ann@example.com", Roles: []string{"user"}, Provider: "local",
		Scope: &CredentialScope{Type: v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_ORG, ID: "acme", ReadOnly: true},
	}

	if _, ok := m.lookupIdentity(ctx, "tok", "u1"); ok {
		t.Fatal("empty cache answered")
	}
	m.rememberIdentity(ctx, "tok", user, nil)
	for _, known := range []string{"u1", ""} {
		got, ok := peer.lookupIdentity(ctx, "tok", known)
		if !ok || got.Username != "ann" || got.Scope == nil || got.Scope.ID != "acme" || !got.Scope.ReadOnly {
			t.Fatalf("lookup with user %q = %+v, %v", known, got, ok)
		}
	}
	if _, ok := m.lookupIdentity(ctx, "tok", "someone-else"); ok {
		t.Error("entry served for the wrong user id")
	}
	for _, key := range srv.Keys() {
		if key == "tok" || len(key) > 0 && key[len(key)-3:] == "tok" {
			t.Errorf("raw token landed in redis as %q", key)
		}
	}

	// A user edit anywhere drops every token of that user
	peer.InvalidateIdentities(ctx, []string{"u1"})
	if _, ok := m.lookupIdentity(ctx, "tok", "u1"); ok {
		t.Fatal("stale identity survived invalidation")
	}
	m.rememberIdentity(ctx, "tok", user, nil)
	if _, ok := m.lookupIdentity(ctx, "tok", "u1"); !ok {
		t.Fatal("refill failed")
	}

	// Logout drops one token only
	m.rememberIdentity(ctx, "tok2", user, nil)
	m.forgetIdentity(ctx, "tok")
	if _, ok := m.lookupIdentity(ctx, "tok", "u1"); ok {
		t.Error("logged out token still cached")
	}
	if _, ok := m.lookupIdentity(ctx, "tok2", "u1"); !ok {
		t.Error("sibling token dropped by logout")
	}

	// Everyone goes on a global change
	m.InvalidateIdentities(ctx, nil)
	if _, ok := m.lookupIdentity(ctx, "tok2", "u1"); ok {
		t.Error("global invalidation missed an entry")
	}
}

func TestIdentityCacheHonoursExpiryAndTTL(t *testing.T) {
	client, srv := cachetest.New(t)
	m := &Manager{cache: client}
	ctx := context.Background()
	user := &AuthenticatedUser{ID: "u2", Username: "bob", Provider: "local"}
	past := time.Now().Add(-time.Minute)
	m.rememberIdentity(ctx, "expired", user, &past)
	if _, ok := m.lookupIdentity(ctx, "expired", ""); ok {
		t.Error("expired api token served")
	}
	m.rememberIdentity(ctx, "fresh", user, nil)
	srv.FastForward(2 * identityTTL)
	if _, ok := m.lookupIdentity(ctx, "fresh", "u2"); ok {
		t.Error("entry outlived its ttl")
	}
}

func TestIdentityCacheOffIsANoop(t *testing.T) {
	m := &Manager{}
	ctx := context.Background()
	m.rememberIdentity(ctx, "tok", &AuthenticatedUser{ID: "u"}, nil)
	if _, ok := m.lookupIdentity(ctx, "tok", "u"); ok {
		t.Error("nil cache answered")
	}
	m.InvalidateIdentities(ctx, nil)
	m.forgetIdentity(ctx, "tok")
}
