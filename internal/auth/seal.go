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

// Sealer derives a purpose bound key from the jwt secret
func (m *Manager) Sealer(purpose string) *Sealer {
	sum := sha256.Sum256(append(append([]byte{}, m.jwtSecret...), []byte(purpose)...))
	return &Sealer{key: sum[:]}
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
	out := append(nonce, gcm.Seal(nil, nonce, []byte(plaintext), nil)...)
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
