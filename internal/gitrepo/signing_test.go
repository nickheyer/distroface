package gitrepo

import (
	"bytes"
	"slices"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

func TestSignatureIssuerNamesTheSigningSubkey(t *testing.T) {
	cfg := &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA}
	entity, err := openpgp.NewEntity("t", "", "t@example.com", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.AddSigningSubkey(cfg); err != nil {
		t.Fatal(err)
	}
	payload := []byte("tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n")
	var sig bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&sig, entity, bytes.NewReader(payload), nil); err != nil {
		t.Fatal(err)
	}
	issuer, ok := signatureIssuer(sig.String())
	if !ok {
		t.Fatal("signature carries no issuer")
	}
	ids := entityKeyIDs(entity)
	if !slices.Contains(ids, issuer) {
		t.Fatalf("issuer %X not among entity ids %X", issuer, ids)
	}
	if issuer == entity.PrimaryKey.KeyId {
		t.Fatalf("expected the signing subkey, got the primary key")
	}
	if _, err := openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{entity}, bytes.NewReader(payload), bytes.NewReader(sig.Bytes()), nil); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	if _, ok := signatureIssuer("not armored"); ok {
		t.Fatal("garbage reported an issuer")
	}
}
