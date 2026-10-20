package services

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.KeyServiceHandler = (*KeyService)(nil)

type KeyService struct {
	store   *stores.Store
	manager *gitrepo.Manager
	log     *logger.Logger
}

func NewKeyService(store *stores.Store, manager *gitrepo.Manager, log *logger.Logger) *KeyService {
	return &KeyService{store: store, manager: manager, log: log}
}

func (s *KeyService) user(ctx context.Context) (*auth.AuthenticatedUser, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if user.IsScoped() {
		return nil, connect.Errorf(connect.CodePermissionDenied, "scoped credentials cannot manage keys")
	}
	return user, nil
}

// Repository a deploy key belongs to, admin level required, writes pass the portal gate
func (s *KeyService) deployRepo(ctx context.Context, user *auth.AuthenticatedUser, namespace, name string, write bool) (*storage.GitRepository, error) {
	if write {
		if err := portalAllowsGitWrite(ctx); err != nil {
			return nil, err
		}
	}
	repo, err := visibleGitRepo(ctx, s.store, s.manager.Access(), user, namespace, name)
	if err != nil {
		return nil, err
	}
	lv := s.manager.Access().Levels(ctx, user, repo)
	if !lv.IsOwner && !lv.CanAdmin {
		return nil, connect.Errorf(connect.CodePermissionDenied, "repository admin required")
	}
	return repo, nil
}

func sshUsageString(u v1.SSHKeyUsage) string {
	switch u {
	case v1.SSHKeyUsage_SSH_KEY_USAGE_SIGNING:
		return "signing"
	case v1.SSHKeyUsage_SSH_KEY_USAGE_BOTH:
		return "both"
	}
	return "auth"
}

func sshUsageProto(u string) v1.SSHKeyUsage {
	switch u {
	case "signing":
		return v1.SSHKeyUsage_SSH_KEY_USAGE_SIGNING
	case "both":
		return v1.SSHKeyUsage_SSH_KEY_USAGE_BOTH
	}
	return v1.SSHKeyUsage_SSH_KEY_USAGE_AUTH
}

func (s *KeyService) sshKeyToProto(ctx context.Context, k *storage.SSHKey) *v1.SSHKey {
	out := &v1.SSHKey{
		Id: k.ID, Title: k.Title, Fingerprint: k.Fingerprint, KeyType: k.KeyType, Usage: sshUsageProto(k.Usage),
		ReadOnly: k.ReadOnly, PublicKey: k.PublicKey, CreatedAt: timestamppb.New(k.CreatedAt),
	}
	if k.OwnerType == storage.SSHKeyOwnerGitRepo {
		if repo, _ := s.store.GetGitRepositoryByID(ctx, k.OwnerID); repo != nil {
			out.RepoFullName = repo.Namespace + "/" + repo.Name
		}
	} else {
		out.ReadOnly = false
	}
	if k.LastUsedAt != nil {
		out.LastUsedAt = timestamppb.New(*k.LastUsedAt)
	}
	if k.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*k.ExpiresAt)
	}
	return out
}

func gpgKeyToProto(k *storage.GPGKey) *v1.GPGKey {
	out := &v1.GPGKey{Id: k.ID, KeyId: k.KeyID, Fingerprint: k.Fingerprint, Verified: k.Verified, CreatedAt: timestamppb.New(k.CreatedAt)}
	_ = json.Unmarshal([]byte(k.Emails), &out.Emails)
	if k.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*k.ExpiresAt)
	}
	return out
}

func (s *KeyService) audit(ctx context.Context, user *auth.AuthenticatedUser, action, detail string) {
	s.manager.Audit(ctx, audit.Event{Action: action, Resource: "git", Outcome: audit.OutcomeSuccess, Detail: detail, Actor: user.Username, ActorID: user.ID})
}

// ── SSH keys ─────────────────────────────────────────────────────────────

func (s *KeyService) AddSSHKey(ctx context.Context, req *v1.AddSSHKeyRequest) (*v1.AddSSHKeyResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(req.PublicKey)))
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "public key is not in authorized_keys format")
	}
	fp := ssh.FingerprintSHA256(pub)
	if existing, _ := s.store.GetSSHKeyByFingerprint(ctx, fp); existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "this key is already registered")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = comment
	}
	if title == "" {
		title = pub.Type()
	}
	if len(title) > 100 {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "title is too long")
	}
	key := &storage.SSHKey{
		ID: uuid.New().String(), Title: title, PublicKey: string(ssh.MarshalAuthorizedKey(pub)), Fingerprint: fp,
		KeyType: pub.Type(), Usage: "auth", CreatedBy: user.ID,
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, int(*req.ExpiresInDays))
		key.ExpiresAt = &t
	}
	detail := fp
	if req.Name != "" {
		repo, err := s.deployRepo(ctx, user, req.Namespace, req.Name, true)
		if err != nil {
			return nil, err
		}
		key.OwnerType, key.OwnerID, key.ReadOnly = storage.SSHKeyOwnerGitRepo, repo.ID, req.ReadOnly
		if req.Usage == v1.SSHKeyUsage_SSH_KEY_USAGE_UNSPECIFIED || req.Usage == v1.SSHKeyUsage_SSH_KEY_USAGE_AUTH {
			key.ReadOnly = req.ReadOnly
		}
		detail = repo.Namespace + "/" + repo.Name + " " + fp
	} else {
		key.OwnerType, key.OwnerID, key.ReadOnly = storage.SSHKeyOwnerUser, user.ID, false
		key.Usage = sshUsageString(req.Usage)
	}
	if err := s.store.CreateSSHKey(ctx, key); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	s.audit(ctx, user, "SSHKey/add", detail)
	return &v1.AddSSHKeyResponse{Key: s.sshKeyToProto(ctx, key)}, nil
}

func (s *KeyService) ListSSHKeys(ctx context.Context, req *v1.ListSSHKeysRequest) (*v1.ListSSHKeysResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	ownerType, ownerID := storage.SSHKeyOwnerUser, user.ID
	if req.Name != "" {
		repo, err := s.deployRepo(ctx, user, req.Namespace, req.Name, false)
		if err != nil {
			return nil, err
		}
		ownerType, ownerID = storage.SSHKeyOwnerGitRepo, repo.ID
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListSSHKeys(ctx, ownerType, ownerID, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.ListSSHKeysResponse{Page: pages.Info(offset, limit, total)}
	for _, k := range rows {
		out.Keys = append(out.Keys, s.sshKeyToProto(ctx, k))
	}
	return out, nil
}

func (s *KeyService) DeleteSSHKey(ctx context.Context, req *v1.DeleteSSHKeyRequest) (*v1.DeleteSSHKeyResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	key, err := s.store.GetSSHKey(ctx, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if key == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "key not found")
	}
	switch key.OwnerType {
	case storage.SSHKeyOwnerUser:
		if key.OwnerID != user.ID {
			return nil, connect.Errorf(connect.CodeNotFound, "key not found")
		}
	case storage.SSHKeyOwnerGitRepo:
		repo, err := s.store.GetGitRepositoryByID(ctx, key.OwnerID)
		if err != nil || repo == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "key not found")
		}
		if _, err := s.deployRepo(ctx, user, repo.Namespace, repo.Name, true); err != nil {
			return nil, err
		}
	}
	if err := s.store.DeleteSSHKey(ctx, key.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	s.audit(ctx, user, "SSHKey/delete", key.Fingerprint)
	return &v1.DeleteSSHKeyResponse{}, nil
}

// ── GPG keys ─────────────────────────────────────────────────────────────

func (s *KeyService) AddGPGKey(ctx context.Context, req *v1.AddGPGKeyRequest) (*v1.AddGPGKeyResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	info, err := gitrepo.ParseGPGKey(req.ArmoredPublicKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if existing, _ := s.store.GetGPGKeyByKeyID(ctx, info.KeyID); existing != nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "this key is already registered")
	}
	emails, _ := json.Marshal(info.Emails)
	key := &storage.GPGKey{
		ID: uuid.New().String(), UserID: user.ID, KeyID: info.KeyID, Fingerprint: info.Fingerprint,
		Armored: strings.TrimSpace(req.ArmoredPublicKey), Emails: string(emails), Challenge: gitrepo.NewChallenge(), ExpiresAt: info.ExpiresAt,
	}
	if err := s.store.CreateGPGKey(ctx, key); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	s.manager.Signing().InvalidateKeys()
	s.audit(ctx, user, "GPGKey/add", key.KeyID)
	return &v1.AddGPGKeyResponse{Key: gpgKeyToProto(key), Challenge: key.Challenge}, nil
}

func (s *KeyService) ListGPGKeys(ctx context.Context, req *v1.ListGPGKeysRequest) (*v1.ListGPGKeysResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	rows, total, err := s.store.ListGPGKeys(ctx, user.ID, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.ListGPGKeysResponse{Page: pages.Info(offset, limit, total)}
	for _, k := range rows {
		out.Keys = append(out.Keys, gpgKeyToProto(k))
	}
	return out, nil
}

func (s *KeyService) DeleteGPGKey(ctx context.Context, req *v1.DeleteGPGKeyRequest) (*v1.DeleteGPGKeyResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	key, err := s.store.GetGPGKey(ctx, user.ID, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if key == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "key not found")
	}
	if err := s.store.DeleteGPGKey(ctx, user.ID, key.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	s.manager.Signing().InvalidateKeys()
	s.audit(ctx, user, "GPGKey/delete", key.KeyID)
	return &v1.DeleteGPGKeyResponse{}, nil
}

func (s *KeyService) VerifyGPGKey(ctx context.Context, req *v1.VerifyGPGKeyRequest) (*v1.VerifyGPGKeyResponse, error) {
	user, err := s.user(ctx)
	if err != nil {
		return nil, err
	}
	key, err := s.store.GetGPGKey(ctx, user.ID, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if key == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "key not found")
	}
	if key.Verified {
		return &v1.VerifyGPGKeyResponse{Key: gpgKeyToProto(key)}, nil
	}
	if key.Challenge == "" {
		key.Challenge = gitrepo.NewChallenge()
		_ = s.store.UpdateGPGKey(ctx, key)
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "sign this challenge and submit it again: %s", key.Challenge)
	}
	if err := gitrepo.VerifyGPGChallenge(key.Armored, key.Challenge, req.ArmoredSignature); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	key.Verified = true
	key.Challenge = ""
	if err := s.store.UpdateGPGKey(ctx, key); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	s.manager.Signing().InvalidateKeys()
	return &v1.VerifyGPGKeyResponse{Key: gpgKeyToProto(key)}, nil
}
