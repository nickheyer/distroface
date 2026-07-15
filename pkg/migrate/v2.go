package migrate

import (
	"context"
	"fmt"
	"os"

	"github.com/nickheyer/distroface/internal/db"
)

// V1 group -> v2 role. All v1 access levels collapse cleanly: admins get the
// admin role; developers and readers both land on "user" (docker push rights in
// v2 come from namespace ownership / org membership, not the casbin role).
var groupRoleMap = map[string]string{
	"admins":     "admin",
	"developers": "user",
	"readers":    "user",
}

const roleSource = "migration"

// V2 wraps direct writes to the v2 database. The v2 server may be running
// concurrently: both sides open sqlite in WAL mode with busy timeouts.
type V2 struct {
	Store *db.Store
}

func OpenV2(path string) (*V2, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("v2 db not found (has the v2 server been started once?): %w", err)
	}
	store, err := db.NewSQLiteStore(path)
	if err != nil {
		return nil, err
	}
	return &V2{Store: store}, nil
}

func (v *V2) Close() error { return v.Store.Close() }

// RolesForGroups maps v1 group memberships to a deduplicated v2 role set.
func RolesForGroups(groups []string) (roles []string, unknown []string) {
	seen := make(map[string]bool)
	for _, g := range groups {
		role, ok := groupRoleMap[g]
		if !ok {
			unknown = append(unknown, g)
			role = "user"
		}
		if !seen[role] {
			seen[role] = true
			roles = append(roles, role)
		}
	}
	if len(roles) == 0 {
		roles = []string{"user"}
	}
	return roles, unknown
}

// ImportUser creates one v1 user in v2 with its bcrypt hash intact and assigns
// mapped roles. Returns false if the user already existed (skipped).
func (v *V2) ImportUser(ctx context.Context, u V1User) (created bool, roles []string, err error) {
	existing, err := v.Store.GetUserByUsernameAndProvider(ctx, u.Username, "local")
	if err != nil {
		return false, nil, err
	}
	roles, _ = RolesForGroups(u.Groups)
	if existing != nil {
		return false, roles, nil
	}

	user := &db.User{
		Username:     u.Username,
		PasswordHash: u.PasswordHash,
		DisplayName:  u.Username,
		AuthProvider: "local",
		IsActive:     true,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	}
	if err := v.Store.CreateUser(ctx, user); err != nil {
		return false, nil, fmt.Errorf("create user %s: %w", u.Username, err)
	}
	for _, role := range roles {
		if err := v.Store.AssignRole(ctx, user.ID, role, roleSource); err != nil {
			return true, roles, fmt.Errorf("assign role %s to %s: %w", role, u.Username, err)
		}
	}
	return true, roles, nil
}

// EnsureRolesSeeded makes sure the system roles exist (idempotent, same seed
// the v2 server runs at startup) so role assignments reference real roles.
func (v *V2) EnsureRolesSeeded() error {
	return v.Store.SeedSystemRoles()
}

// EnsureOrg creates an org if missing and adds owner as org owner.
// Fails if the name is taken by a user (v2 forbids user/org name collisions).
func (v *V2) EnsureOrg(ctx context.Context, name, ownerUsername string) (created bool, err error) {
	if err := ValidateNamespace(name); err != nil {
		return false, err
	}
	org, err := v.Store.GetOrganization(ctx, name)
	if err != nil {
		return false, err
	}
	if org != nil {
		return false, nil
	}
	if user, err := v.Store.GetUserByUsername(ctx, name); err != nil {
		return false, err
	} else if user != nil {
		return false, fmt.Errorf("org name %q collides with an existing user", name)
	}

	owner, err := v.Store.GetUserByUsername(ctx, ownerUsername)
	if err != nil {
		return false, err
	}
	if owner == nil {
		return false, fmt.Errorf("org owner %q not found in v2 (run users import first)", ownerUsername)
	}

	org = &db.Organization{
		Name:        name,
		DisplayName: name,
		Description: "Imported from distroface v1",
		CreatedBy:   owner.ID,
	}
	if err := v.Store.CreateOrganization(ctx, org); err != nil {
		return false, err
	}
	member := &db.OrgMember{OrgID: org.ID, UserID: owner.ID, Role: db.OrgRoleOwner}
	if err := v.Store.AddOrgMember(ctx, member); err != nil {
		return true, fmt.Errorf("add owner to org %s: %w", name, err)
	}
	return true, nil
}

// EnsureArtifactRepo creates a v1 artifact repo in v2 if missing, preserving
// name, description, privacy, and timestamps. The v1 owner username must
// already exist in v2 (run users import first).
func (v *V2) EnsureArtifactRepo(ctx context.Context, r V1ArtifactRepo) (repoID int64, created bool, err error) {
	existing, err := v.Store.GetArtifactRepository(ctx, r.Name)
	if err != nil {
		return 0, false, err
	}
	if existing != nil {
		return existing.ID, false, nil
	}

	owner, err := v.Store.GetUserByUsername(ctx, r.Owner)
	if err != nil {
		return 0, false, err
	}
	if owner == nil {
		return 0, false, fmt.Errorf("owner %q not found in v2 (run users import first)", r.Owner)
	}

	repo := &db.ArtifactRepository{
		Name:        r.Name,
		Description: r.Desc,
		OwnerID:     owner.ID,
		IsPrivate:   r.Private,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
	if err := v.Store.CreateArtifactRepository(ctx, repo); err != nil {
		return 0, false, err
	}
	return repo.ID, true, nil
}

// SuppressWebhooks deactivates all active webhooks and returns a restore func.
// The v2 listener dispatches per-event by querying active webhooks, so flipping
// the rows off silences dispatch during replay without touching server code.
func (v *V2) SuppressWebhooks(ctx context.Context) (restore func() error, suppressed int, err error) {
	var ids []string
	if err := v.Store.DB().WithContext(ctx).
		Model(&db.Webhook{}).Where("active = ?", true).Pluck("id", &ids).Error; err != nil {
		return nil, 0, err
	}
	if len(ids) == 0 {
		return func() error { return nil }, 0, nil
	}
	if err := v.Store.DB().WithContext(ctx).
		Model(&db.Webhook{}).Where("id IN ?", ids).Update("active", false).Error; err != nil {
		return nil, 0, err
	}
	restore = func() error {
		return v.Store.DB().
			Model(&db.Webhook{}).Where("id IN ?", ids).Update("active", true).Error
	}
	return restore, len(ids), nil
}

// SetRepoVisibility applies v1 privacy to an auto-created v2 repo row.
// Returns false if the repo row does not exist yet (image not pushed).
func (v *V2) SetRepoVisibility(ctx context.Context, namespace, name string, private bool) (bool, error) {
	repo, err := v.Store.GetRepository(ctx, namespace, name)
	if err != nil {
		return false, err
	}
	if repo == nil {
		return false, nil
	}
	if repo.IsPrivate == private {
		return true, nil
	}
	repo.IsPrivate = private
	return true, v.Store.UpdateRepository(ctx, repo)
}
