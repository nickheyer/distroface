package gitrepo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"golang.org/x/crypto/ssh"
)

// Outcome of a signature check
type Verification struct {
	State  v1.GitVerificationState
	Reason string
	Signer string
	KeyID  string
}

// Signing holds the instance key and verifies commit signatures
type Signing struct {
	store    *stores.Store
	keysDir  string
	mu       sync.Mutex
	instance *openpgp.Entity
	armored  string
}

func NewSigning(store *stores.Store, keysDir string) *Signing {
	return &Signing{store: store, keysDir: keysDir}
}

// Loads or generates the instance OpenPGP key
func (s *Signing) Instance() (*openpgp.Entity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.instance != nil {
		return s.instance, nil
	}
	path := filepath.Join(s.keysDir, "signing.asc")
	if data, err := os.ReadFile(path); err == nil {
		ring, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
		if err != nil || len(ring) == 0 {
			return nil, fmt.Errorf("reading signing key: %w", err)
		}
		s.instance = ring[0]
		s.armored = s.publicArmor()
		return s.instance, nil
	}
	cfg := &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, Time: time.Now}
	entity, err := openpgp.NewEntity("Distroface", "instance signing key", "noreply@distroface.local", cfg)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return nil, err
	}
	if err := entity.SerializePrivate(w, nil); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return nil, err
	}
	s.instance = entity
	s.armored = s.publicArmor()
	return entity, nil
}

func (s *Signing) publicArmor() string {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return ""
	}
	if err := s.instance.Serialize(w); err != nil {
		return ""
	}
	w.Close()
	return buf.String()
}

// Armored public half of the instance key
func (s *Signing) InstancePublicKey() string {
	if _, err := s.Instance(); err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.armored
}

// Signer that signs with the instance key
func (s *Signing) InstanceSigner() Signer {
	return instanceSigner{s: s}
}

type instanceSigner struct{ s *Signing }

func (i instanceSigner) Sign(_ context.Context, message io.Reader) ([]byte, error) {
	entity, err := i.s.Instance()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, entity, message, nil); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Verifies a commit signature against stored keys and the instance key
func (s *Signing) VerifyCommit(ctx context.Context, c *object.Commit) Verification {
	if c.Signature == "" {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNSIGNED}
	}
	payload := &bytes.Buffer{}
	encoded := &memObject{}
	if err := c.EncodeWithoutSignature(encoded); err != nil {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "could not encode commit"}
	}
	payload.Write(encoded.buf.Bytes())
	return s.verify(ctx, payload.Bytes(), c.Signature, c.Committer.Email)
}

// Verifies a tag signature
func (s *Signing) VerifyTag(ctx context.Context, t *object.Tag) Verification {
	if t.Signature == "" {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNSIGNED}
	}
	encoded := &memObject{}
	if err := t.EncodeWithoutSignature(encoded); err != nil {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "could not encode tag"}
	}
	return s.verify(ctx, encoded.buf.Bytes(), t.Signature, t.Tagger.Email)
}

func (s *Signing) verify(ctx context.Context, payload []byte, signature, email string) Verification {
	if strings.Contains(signature, "SSH SIGNATURE") {
		return s.verifySSH(ctx, payload, signature, email)
	}
	if entity, err := s.Instance(); err == nil {
		if _, err := openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{entity}, bytes.NewReader(payload), strings.NewReader(signature), nil); err == nil {
			return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_INSTANCE, Signer: "instance", KeyID: fmt.Sprintf("%X", entity.PrimaryKey.KeyId)}
		}
	}
	keys, err := s.store.ListAllGPGKeys(ctx)
	if err != nil {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "key lookup failed"}
	}
	for _, k := range keys {
		ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(k.Armored))
		if err != nil {
			continue
		}
		signer, err := openpgp.CheckArmoredDetachedSignature(ring, bytes.NewReader(payload), strings.NewReader(signature), nil)
		if err != nil || signer == nil {
			continue
		}
		if !k.Verified {
			return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "signing key not verified by its owner", KeyID: k.KeyID}
		}
		if !emailMatches(k.Emails, email) {
			return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "committer email does not match the key", KeyID: k.KeyID}
		}
		user, _ := s.store.GetUserByID(ctx, k.UserID)
		username := ""
		if user != nil {
			username = user.Username
		}
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_VERIFIED, Signer: username, KeyID: k.KeyID}
	}
	return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "no known key signed this object"}
}

func emailMatches(stored string, email string) bool {
	var emails []string
	_ = json.Unmarshal([]byte(stored), &emails)
	for _, e := range emails {
		if strings.EqualFold(e, email) {
			return true
		}
	}
	return false
}

// Parses an armored SSH signature and checks it against stored signing keys
func (s *Signing) verifySSH(ctx context.Context, payload []byte, signature, email string) Verification {
	sig, err := parseSSHSig(signature)
	if err != nil {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: err.Error()}
	}
	if sig.namespace != "git" {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "signature namespace is not git"}
	}
	pub, err := ssh.ParsePublicKey(sig.publicKey)
	if err != nil {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "unparsable signing key"}
	}
	hashed, err := sshSigHash(sig.hashAlg, payload)
	if err != nil {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: err.Error()}
	}
	signed := sshSigSignedData(sig.namespace, sig.hashAlg, hashed)
	if err := pub.Verify(signed, sig.signature); err != nil {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "signature does not verify"}
	}
	fp := ssh.FingerprintSHA256(pub)
	key, err := s.store.GetSSHKeyByFingerprint(ctx, fp)
	if err != nil || key == nil || key.OwnerType != db.SSHKeyOwnerUser || (key.Usage != "signing" && key.Usage != "both") {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "signing key is not registered for signing", KeyID: fp}
	}
	user, _ := s.store.GetUserByID(ctx, key.OwnerID)
	if user == nil {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "key owner not found", KeyID: fp}
	}
	if user.Email == nil || !strings.EqualFold(*user.Email, email) {
		return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_UNVERIFIED, Reason: "committer email does not match the key owner", KeyID: fp}
	}
	return Verification{State: v1.GitVerificationState_GIT_VERIFICATION_STATE_VERIFIED, Signer: user.Username, KeyID: fp}
}

type sshSig struct {
	publicKey []byte
	namespace string
	hashAlg   string
	signature *ssh.Signature
}

// Decodes the armored SSHSIG blob
func parseSSHSig(armored string) (*sshSig, error) {
	var b64 strings.Builder
	for _, line := range strings.Split(armored, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		b64.WriteString(line)
	}
	raw, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		return nil, fmt.Errorf("unarmored ssh signature")
	}
	if len(raw) < 10 || string(raw[:6]) != "SSHSIG" {
		return nil, fmt.Errorf("not an ssh signature")
	}
	rest := raw[6:]
	version := binary.BigEndian.Uint32(rest[:4])
	if version != 1 {
		return nil, fmt.Errorf("unsupported ssh signature version %d", version)
	}
	rest = rest[4:]
	next := func() ([]byte, error) {
		if len(rest) < 4 {
			return nil, errors.New("truncated ssh signature")
		}
		n := binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
		if uint32(len(rest)) < n {
			return nil, errors.New("truncated ssh signature")
		}
		v := rest[:n]
		rest = rest[n:]
		return v, nil
	}
	pub, err := next()
	if err != nil {
		return nil, err
	}
	ns, err := next()
	if err != nil {
		return nil, err
	}
	if _, err := next(); err != nil {
		return nil, err
	}
	hashAlg, err := next()
	if err != nil {
		return nil, err
	}
	sigBlob, err := next()
	if err != nil {
		return nil, err
	}
	sig := &ssh.Signature{}
	if err := ssh.Unmarshal(sigBlob, sig); err != nil {
		return nil, fmt.Errorf("unparsable ssh signature blob")
	}
	return &sshSig{publicKey: pub, namespace: string(ns), hashAlg: string(hashAlg), signature: sig}, nil
}

func sshSigHash(alg string, payload []byte) ([]byte, error) {
	switch alg {
	case "sha256":
		sum := sha256Sum(payload)
		return sum, nil
	case "sha512":
		sum := sha512Sum(payload)
		return sum, nil
	}
	return nil, fmt.Errorf("unsupported ssh signature hash %q", alg)
}

// Builds the blob the ssh signature actually covers
func sshSigSignedData(namespace, hashAlg string, hashed []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("SSHSIG")
	writeString := func(s []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		buf.Write(n[:])
		buf.Write(s)
	}
	writeString([]byte(namespace))
	writeString(nil)
	writeString([]byte(hashAlg))
	writeString(hashed)
	return buf.Bytes()
}

// Random challenge a user signs to prove key ownership
func NewChallenge() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "distroface-gpg-verify-" + base64.RawURLEncoding.EncodeToString(b)
}

// Parsed metadata of an armored public key
type GPGKeyInfo struct {
	KeyID       string
	Fingerprint string
	Emails      []string
	ExpiresAt   *time.Time
}

// Reads an armored OpenPGP public key
func ParseGPGKey(armored string) (*GPGKeyInfo, error) {
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil || len(ring) == 0 {
		return nil, fmt.Errorf("invalid armored public key")
	}
	e := ring[0]
	if e.PrivateKey != nil {
		return nil, fmt.Errorf("private keys are not accepted")
	}
	info := &GPGKeyInfo{
		KeyID:       fmt.Sprintf("%016X", e.PrimaryKey.KeyId),
		Fingerprint: fmt.Sprintf("%X", e.PrimaryKey.Fingerprint),
	}
	for _, id := range e.Identities {
		if id.UserId != nil && id.UserId.Email != "" {
			info.Emails = append(info.Emails, id.UserId.Email)
		}
	}
	for _, id := range e.Identities {
		if id.SelfSignature != nil && id.SelfSignature.KeyLifetimeSecs != nil {
			exp := e.PrimaryKey.CreationTime.Add(time.Duration(*id.SelfSignature.KeyLifetimeSecs) * time.Second)
			info.ExpiresAt = &exp
			break
		}
	}
	return info, nil
}

// Checks a detached armored signature over the challenge
func VerifyGPGChallenge(armoredKey, challenge, armoredSig string) error {
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armoredKey))
	if err != nil {
		return fmt.Errorf("invalid key")
	}
	_, err = openpgp.CheckArmoredDetachedSignature(ring, strings.NewReader(challenge), strings.NewReader(armoredSig), nil)
	if err != nil {
		return fmt.Errorf("signature does not verify the challenge")
	}
	return nil
}

// Proto conversion of a verification
func (v Verification) Proto() *v1.GitVerification {
	return &v1.GitVerification{State: v.State, Reason: v.Reason, Signer: v.Signer, KeyId: v.KeyID}
}
