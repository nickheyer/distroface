package certs

import (
	"context"
	"testing"
	"time"

	"github.com/nickheyer/distroface/internal/cache/cachetest"
)

func TestSharedACMEStateReachesEveryInstance(t *testing.T) {
	client, srv := cachetest.New(t)
	st := sharedACMEState{c: client}
	peer := sharedACMEState{c: cachetest.Attach(t, srv)}
	ctx := context.Background()

	st.addNonce(ctx, "n1")
	if !peer.takeNonce(ctx, "n1") {
		t.Fatal("peer refused a fresh nonce")
	}
	if st.takeNonce(ctx, "n1") {
		t.Fatal("nonce replayed")
	}

	exp := time.Now().Add(time.Hour)
	st.saveAuthz(ctx, &acmeAuthz{ID: "a1", Domain: "x.example", Status: "pending", Token: "t", Account: "acct", OrderID: "o1", Expires: exp})
	st.saveOrder(ctx, &acmeOrder{ID: "o1", Account: "acct", Identifiers: []string{"x.example"}, AuthzIDs: []string{"a1"}, Status: "pending", Expires: exp})

	if !peer.claimValidation(ctx, "a1") {
		t.Fatal("first claim lost")
	}
	if st.claimValidation(ctx, "a1") {
		t.Fatal("second claim won")
	}
	if a := st.authz(ctx, "a1"); a == nil || a.Status != "processing" || a.Token != "t" {
		t.Fatalf("authz after claim = %+v", a)
	}
	o := peer.order(ctx, "o1")
	if o == nil || len(o.AuthzIDs) != 1 || o.AuthzIDs[0] != "a1" || o.Identifiers[0] != "x.example" {
		t.Fatalf("order on the peer = %+v", o)
	}
	o.Status = "ready"
	peer.saveOrder(ctx, o)
	if got := st.order(ctx, "o1"); got == nil || got.Status != "ready" {
		t.Fatalf("order after peer write = %+v", got)
	}

	srv.FastForward(2 * time.Hour)
	if st.order(ctx, "o1") != nil || st.authz(ctx, "a1") != nil {
		t.Error("expired state still served")
	}
}

func TestMemoryACMEStateHandsOutCopies(t *testing.T) {
	st := newMemoryACMEState()
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	st.saveOrder(ctx, &acmeOrder{ID: "o1", Status: "pending", AuthzIDs: []string{"a1"}, Expires: exp})
	first := st.order(ctx, "o1")
	first.Status = "valid"
	first.AuthzIDs[0] = "changed"
	if again := st.order(ctx, "o1"); again.Status != "pending" || again.AuthzIDs[0] != "a1" {
		t.Errorf("stored order mutated through a copy: %+v", again)
	}
	st.saveAuthz(ctx, &acmeAuthz{ID: "a1", Status: "pending", Expires: exp})
	if !st.claimValidation(ctx, "a1") || st.claimValidation(ctx, "a1") {
		t.Error("claim is not once")
	}
	st.addNonce(ctx, "n")
	if !st.takeNonce(ctx, "n") || st.takeNonce(ctx, "n") {
		t.Error("nonce is not once")
	}
	st.saveOrder(ctx, &acmeOrder{ID: "old", Expires: time.Now().Add(-time.Minute)})
	if st.order(ctx, "old") != nil {
		t.Error("expired order served")
	}
}
