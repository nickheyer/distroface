package stores_test

import (
	"context"
	"testing"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores/storetest"
)

func TestOutboundSSHKeyFirstWriterWins(t *testing.T) {
	store := storetest.New(t)
	ctx := context.Background()

	if key, err := store.GetOutboundSSHKey(ctx, db.OutboundKeyScopeInstance, ""); err != nil || key != nil {
		t.Fatalf("empty store returned %v, %v", key, err)
	}
	for _, pub := range []string{"first", "second"} {
		row := &db.OutboundSSHKey{Scope: db.OutboundKeyScopeInstance, PrivateKey: "sealed-" + pub, PublicKey: pub}
		if err := store.CreateOutboundSSHKeyIfAbsent(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	key, err := store.GetOutboundSSHKey(ctx, db.OutboundKeyScopeInstance, "")
	if err != nil || key == nil || key.PublicKey != "first" {
		t.Fatalf("racing create replaced the key, got %+v, %v", key, err)
	}

	if err := store.SaveOutboundSSHKey(ctx, &db.OutboundSSHKey{Scope: db.OutboundKeyScopeInstance, PrivateKey: "sealed-third", PublicKey: "third"}); err != nil {
		t.Fatal(err)
	}
	if key, _ = store.GetOutboundSSHKey(ctx, db.OutboundKeyScopeInstance, ""); key == nil || key.PublicKey != "third" {
		t.Fatalf("regenerate did not replace the key, got %+v", key)
	}
	if other, _ := store.GetOutboundSSHKey(ctx, db.OutboundKeyScopeOrg, "org-1"); other != nil {
		t.Fatalf("scopes leaked into each other, got %+v", other)
	}
}
