package actions

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"

	"github.com/nickheyer/distroface/internal/db"
)

var secretNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,199}$`)

// ValidSecretName accepts env style identifiers only
func ValidSecretName(name string) bool {
	return secretNamePattern.MatchString(name)
}

func (m *Manager) sealKey() []byte {
	return m.auth.DerivedKey("actions")
}

// Seal encrypts a secret value with AES-GCM, nonce first
func (m *Manager) Seal(plaintext string) ([]byte, error) {
	block, err := aes.NewCipher(m.sealKey())
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open decrypts a sealed secret value, accepting the jwt derived key it may predate
func (m *Manager) Open(sealed []byte) (string, error) {
	out, err := openWith(m.sealKey(), sealed)
	if err != nil {
		if legacy, lerr := openWith(m.auth.LegacyDerivedKey("actions"), sealed); lerr == nil {
			return legacy, nil
		}
	}
	return out, err
}

// ResealLegacy rewraps secrets sealed under the jwt secret with the sealing key
func (m *Manager) ResealLegacy(ctx context.Context) error {
	rows, err := m.store.ListAllActionsSecrets(ctx)
	if err != nil {
		return err
	}
	n := 0
	for _, row := range rows {
		if _, err := openWith(m.sealKey(), row.Sealed); err == nil {
			continue
		}
		plain, err := openWith(m.auth.LegacyDerivedKey("actions"), row.Sealed)
		if err != nil {
			m.log.Warn("actions: secret %s at %s scope cannot be opened with any key, re-enter it", row.Name, row.ScopeKind)
			continue
		}
		sealed, err := m.Seal(plain)
		if err != nil {
			return err
		}
		if err := m.store.SetActionsSecretSealed(ctx, row.ID, sealed); err != nil {
			return err
		}
		n++
	}
	if n > 0 {
		m.log.Info("actions: resealed %d secrets under the sealing key", n)
	}
	return nil
}

func openWith(key, sealed []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("sealed value too short")
	}
	out, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Scope precedence when merging, later wins
var scopeRank = map[string]int{db.ActionsScopeSystem: 0, db.ActionsScopeOrg: 1, db.ActionsScopeRepo: 2}

// Decrypted secrets visible to a repository, repo over org over system
func (m *Manager) repoSecrets(ctx context.Context, repo *db.GitRepository, orgID string) (map[string]string, error) {
	rows, err := m.store.ListActionsSecretsForRepo(ctx, repo.ID, orgID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	rank := map[string]int{}
	for _, row := range rows {
		if r, seen := rank[row.Name]; seen && r > scopeRank[row.ScopeKind] {
			continue
		}
		val, err := m.Open(row.Sealed)
		if err != nil {
			// A job silently missing a secret fails somewhere far away, say why here
			return nil, fmt.Errorf("secret %s at %s scope cannot be decrypted, re-enter it", row.Name, row.ScopeKind)
		}
		out[row.Name] = val
		rank[row.Name] = scopeRank[row.ScopeKind]
	}
	return out, nil
}

// Variables visible to a repository, repo over org over system
func (m *Manager) repoVariables(ctx context.Context, repo *db.GitRepository, orgID string) (map[string]string, error) {
	rows, err := m.store.ListActionsVariablesForRepo(ctx, repo.ID, orgID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	rank := map[string]int{}
	for _, row := range rows {
		if r, seen := rank[row.Name]; seen && r > scopeRank[row.ScopeKind] {
			continue
		}
		out[row.Name] = row.Value
		rank[row.Name] = scopeRank[row.ScopeKind]
	}
	return out, nil
}

// Organization id of an org owned repository, empty otherwise
func (m *Manager) repoOrgID(ctx context.Context, repo *db.GitRepository) string {
	if repo == nil || !repo.IsOrgNamespace {
		return ""
	}
	org, err := m.store.GetOrganization(ctx, repo.Namespace)
	if err != nil || org == nil {
		return ""
	}
	return org.ID
}
