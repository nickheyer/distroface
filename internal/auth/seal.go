package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// Sealer encrypts small secrets with a purpose bound key
type Sealer struct {
	key []byte
}

// Sealer derives a purpose bound key from the sealing key
func (m *Manager) Sealer(purpose string) *Sealer {
	return &Sealer{key: m.DerivedKey(purpose)}
}

// Sealer under the jwt secret, how values were sealed before the sealing key existed
func (m *Manager) LegacySealer(purpose string) *Sealer {
	return &Sealer{key: m.LegacyDerivedKey(purpose)}
}

// Purpose bound key material from the sealing key
func (m *Manager) DerivedKey(purpose string) []byte {
	return deriveKey(m.sealingKey(), purpose)
}

// Purpose bound key material from the jwt secret, for values sealed before
func (m *Manager) LegacyDerivedKey(purpose string) []byte {
	return deriveKey(m.jwtSecret, purpose)
}

// The jwt secret stands in only when no sealing key was loaded
func (m *Manager) sealingKey() []byte {
	if len(m.sealKey) > 0 {
		return m.sealKey
	}
	return m.jwtSecret
}

func deriveKey(secret []byte, purpose string) []byte {
	sum := sha256.Sum256(append(append([]byte{}, secret...), []byte(purpose)...))
	return sum[:]
}

// Seal returns base64 of nonce plus ciphertext
func (s *Sealer) Seal(plaintext string) (string, error) {
	if s == nil {
		return "", errors.New("sealer unavailable")
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal
func (s *Sealer) Open(encoded string) (string, error) {
	if s == nil {
		return "", errors.New("sealer unavailable")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("sealed value too short")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
