package mirror

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/nickheyer/distroface/internal/db"
	"golang.org/x/crypto/ssh"
)

var errNoSealer = errors.New("ssh keys need the sealing key")

// Fresh ed25519 pair, private half sealed
func (m *Monitor) mintKey(comment string) (sealed, line string, err error) {
	if m.sealer == nil {
		return "", "", errNoSealer
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	line = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
	sealed, err = m.sealer.Seal(string(pem.EncodeToMemory(block)))
	return sealed, line, err
}

func scopeComment(scope, label string) string {
	if scope == db.OutboundKeyScopeInstance {
		return "distroface-instance"
	}
	return "distroface-org-" + label
}

// Stored key of a scope, minted on first use
func (m *Monitor) scopeKey(ctx context.Context, scope, scopeID, label string) (*db.OutboundSSHKey, error) {
	key, err := m.store.GetOutboundSSHKey(ctx, scope, scopeID)
	if err != nil || key != nil {
		return key, err
	}
	sealed, line, err := m.mintKey(scopeComment(scope, label))
	if err != nil {
		return nil, err
	}
	fresh := &db.OutboundSSHKey{Scope: scope, ScopeID: scopeID, PrivateKey: sealed, PublicKey: line}
	if err := m.store.CreateOutboundSSHKeyIfAbsent(ctx, fresh); err != nil {
		return nil, err
	}
	return m.store.GetOutboundSSHKey(ctx, scope, scopeID)
}

func (m *Monitor) regenerateScopeKey(ctx context.Context, scope, scopeID, label string) (string, error) {
	sealed, line, err := m.mintKey(scopeComment(scope, label))
	if err != nil {
		return "", err
	}
	key := &db.OutboundSSHKey{Scope: scope, ScopeID: scopeID, PrivateKey: sealed, PublicKey: line}
	if err := m.store.SaveOutboundSSHKey(ctx, key); err != nil {
		return "", err
	}
	return line, nil
}

func (m *Monitor) scopePublicKey(ctx context.Context, scope, scopeID, label string) string {
	key, err := m.scopeKey(ctx, scope, scopeID, label)
	if err != nil || key == nil {
		if err != nil && !errors.Is(err, errNoSealer) {
			m.log.Error("mirror: %s ssh key: %v", scope, err)
		}
		return ""
	}
	return key.PublicKey
}

// Public half of the instance wide ssh identity
func (m *Monitor) InstancePublicKey(ctx context.Context) string {
	return m.scopePublicKey(ctx, db.OutboundKeyScopeInstance, "", "")
}

// Public half of one organization ssh identity
func (m *Monitor) OrgPublicKey(ctx context.Context, org *db.Organization) string {
	if org == nil {
		return ""
	}
	return m.scopePublicKey(ctx, db.OutboundKeyScopeOrg, org.ID, org.Name)
}

// Org key of the owning namespace, empty when personal
func (m *Monitor) RepoOrgPublicKey(ctx context.Context, repo *db.GitRepository) string {
	return m.OrgPublicKey(ctx, m.repoOrg(ctx, repo))
}

func (m *Monitor) RegenerateInstanceKey(ctx context.Context) (string, error) {
	return m.regenerateScopeKey(ctx, db.OutboundKeyScopeInstance, "", "")
}

func (m *Monitor) RegenerateOrgKey(ctx context.Context, org *db.Organization) (string, error) {
	return m.regenerateScopeKey(ctx, db.OutboundKeyScopeOrg, org.ID, org.Name)
}

func (m *Monitor) repoOrg(ctx context.Context, repo *db.GitRepository) *db.Organization {
	if repo == nil || !repo.IsOrgNamespace {
		return nil
	}
	org, err := m.store.GetOrganization(ctx, repo.Namespace)
	if err != nil {
		return nil
	}
	return org
}

// Replaces the repo ssh key, returns the public line
func (m *Monitor) GenerateGitMirrorKey(ctx context.Context, repo *db.GitRepository) (string, error) {
	sealed, line, err := m.mintKey(fmt.Sprintf("distroface-mirror-%s-%s", repo.Namespace, repo.Name))
	if err != nil {
		return "", err
	}
	state := parseGitMirrorState(repo.MirrorState)
	state.SSHPrivateKey = sealed
	state.SSHPublicKey = line
	state.HostKey = ""
	encoded := state.Encode()
	if err := m.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"mirror_state": encoded}); err != nil {
		return "", err
	}
	repo.MirrorState = encoded
	return line, nil
}

// Public half of the repo ssh key, empty when absent
func (m *Monitor) GitMirrorPublicKey(repo *db.GitRepository) string {
	if repo == nil {
		return ""
	}
	return parseGitMirrorState(repo.MirrorState).SSHPublicKey
}

// Repo public key, minted on first ask
func (m *Monitor) EnsureGitMirrorKey(ctx context.Context, repo *db.GitRepository) string {
	if line := m.GitMirrorPublicKey(repo); line != "" || repo == nil {
		return line
	}
	line, err := m.GenerateGitMirrorKey(ctx, repo)
	if err != nil {
		if !errors.Is(err, errNoSealer) {
			m.log.Error("mirror: ssh key for %s/%s: %v", repo.Namespace, repo.Name, err)
		}
		return ""
	}
	return line
}

// Keys a repo may present, most specific first
func (m *Monitor) outboundKeys(ctx context.Context, repo *db.GitRepository, repoSealed string) [][]byte {
	var out [][]byte
	add := func(sealed string) {
		if pem, err := m.openMirrorKey(sealed); err == nil && len(pem) > 0 {
			out = append(out, pem)
		}
	}
	add(repoSealed)
	if org := m.repoOrg(ctx, repo); org != nil {
		if key, err := m.scopeKey(ctx, db.OutboundKeyScopeOrg, org.ID, org.Name); err == nil && key != nil {
			add(key.PrivateKey)
		}
	}
	if key, err := m.scopeKey(ctx, db.OutboundKeyScopeInstance, "", ""); err == nil && key != nil {
		add(key.PrivateKey)
	}
	return out
}
