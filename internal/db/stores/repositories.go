package stores

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"gorm.io/gorm"
)

// ── Repository operations ────────────────────────────────────────────────

func (s *Store) CreateRepository(ctx context.Context, repo *db.Repository) error {
	return s.db.WithContext(ctx).Create(repo).Error
}

func (s *Store) GetRepository(ctx context.Context, namespace, name string) (*db.Repository, error) {
	var repo db.Repository
	err := s.db.WithContext(ctx).First(&repo, "namespace = ? AND name = ?", namespace, name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &repo, nil
}

// ListRepositories returns repositories with visibility filtering.
//
// If canManage is true, all repositories are returned (no visibility filtering).
// Otherwise, the returned set is:
//   - All public repositories
//   - Private repos owned by userID (owner_id matches)
//   - Private repos whose namespace is a user's username (personal repos)
//   - Private repos in organizations the user is a member of
//   - Private repos explicitly granted via RBAC (grantedRepos contains "namespace/name")
//
// ReposQuery allowlists docker repository list filters
var ReposQuery = pages.Spec{
	Fields: map[string]string{
		"name":        "name",
		"namespace":   "namespace",
		"description": "description",
	},
	Text: []string{"name", "namespace", "description"},
}

// If userID is empty (anonymous), only public repos are returned.
type RepoListOptions struct {
	Namespace      string      // Optional exact namespace filter
	ViewerID       string      // Owner whose private repos are visible
	IncludePrivate bool        // True bypasses visibility filtering
	GrantedRepos   []string    // RBAC granted repos as namespace/name
	OnlyRepo       string      // Credential scope, same name or linked source
	OnlyNamespace  string      // Credential scope, exact namespace
	Query          pages.Query // Structured filter against ReposQuery
	Limit          int
	Offset         int
}

// Confines package listings to a credential scope, empty values pass
func packageScope(onlyRepo, onlyNamespace string) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		if onlyRepo != "" {
			namespace, name, _ := strings.Cut(onlyRepo, "/")
			q = q.Where("(namespace || '/' || name) = ? OR git_repo_id IN (SELECT id FROM git_repositories WHERE namespace = ? AND name = ?)", onlyRepo, namespace, name)
		}
		if onlyNamespace != "" {
			q = q.Where("namespace = ?", onlyNamespace)
		}
		return q
	}
}

func (s *Store) ListRepositories(ctx context.Context, opts RepoListOptions) ([]*db.Repository, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.Repository{})

	if opts.Namespace != "" {
		tx = tx.Where("namespace = ?", opts.Namespace)
	}
	tx = tx.Scopes(ReposQuery.Scope(opts.Query), packageScope(opts.OnlyRepo, opts.OnlyNamespace))

	if !opts.IncludePrivate {
		if opts.ViewerID != "" {
			// Authenticated user: public + owned + org membership + RBAC grants
			conditions := "is_private = ? OR owner_id = ? OR namespace IN (SELECT o.name FROM organizations o JOIN org_members om ON o.id = om.org_id WHERE om.user_id = ?)"
			args := []any{false, opts.ViewerID, opts.ViewerID}

			if len(opts.GrantedRepos) > 0 {
				conditions += " OR (namespace || '/' || name) IN ?"
				args = append(args, opts.GrantedRepos)
			}

			tx = tx.Where(conditions, args...)
		} else {
			// Anonymous: public only
			tx = tx.Where("is_private = ?", false)
		}
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var repos []*db.Repository
	err := tx.Order("updated_at DESC").Limit(opts.Limit).Offset(opts.Offset).Find(&repos).Error
	return repos, total, err
}

func (s *Store) DeleteRepository(ctx context.Context, namespace, name string) error {
	return s.db.WithContext(ctx).Delete(&db.Repository{}, "namespace = ? AND name = ?", namespace, name).Error
}

func (s *Store) UpdateRepository(ctx context.Context, repo *db.Repository) error {
	return s.db.WithContext(ctx).Save(repo).Error
}

// Targeted write so syncs never clobber concurrent edits
func (s *Store) SetRepositoryMirrorStatus(ctx context.Context, id string, at time.Time, errMsg, state string) error {
	return s.db.WithContext(ctx).Model(&db.Repository{}).Where("id = ?", id).
		Updates(map[string]any{"mirror_last_sync": at, "mirror_last_error": errMsg, "mirror_state": state}).Error
}

// Mirror repos for the sync monitor
func (s *Store) ListMirrorRepositories(ctx context.Context) ([]*db.Repository, error) {
	var repos []*db.Repository
	err := s.db.WithContext(ctx).
		Where("type = ?", v1.RepositoryType_REPOSITORY_TYPE_MIRROR).
		Order("namespace ASC, name ASC").Find(&repos).Error
	return repos, err
}

func (s *Store) IncrementPullCount(ctx context.Context, namespace, name string) error {
	return s.db.WithContext(ctx).Model(&db.Repository{}).
		Where("namespace = ? AND name = ?", namespace, name).
		UpdateColumn("pull_count", gorm.Expr("pull_count + 1")).Error
}

func (s *Store) IncrementPushCount(ctx context.Context, namespace, name string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&db.Repository{}).
		Where("namespace = ? AND name = ?", namespace, name).
		Updates(map[string]interface{}{
			"push_count": gorm.Expr("push_count + 1"),
			"last_push":  now,
		}).Error
}

// ── Star operations ──────────────────────────────────────────────────────

func (s *Store) StarRepository(ctx context.Context, userID, repoID string) error {
	var existing db.Star
	err := s.db.WithContext(ctx).First(&existing, "user_id = ? AND repo_id = ?", userID, repoID).Error
	if err == nil {
		return nil // Already starred
	}
	star := &db.Star{ID: uuid.New().String(), UserID: userID, RepoID: repoID}
	return s.db.WithContext(ctx).Create(star).Error
}

func (s *Store) UnstarRepository(ctx context.Context, userID, repoID string) error {
	return s.db.WithContext(ctx).Where("user_id = ? AND repo_id = ?", userID, repoID).Delete(&db.Star{}).Error
}

func (s *Store) CountStars(ctx context.Context, repoID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&db.Star{}).Where("repo_id = ?", repoID).Count(&count).Error
	return count, err
}

// Star counts for many repos in one query
func (s *Store) GetStarCounts(ctx context.Context, repoIDs []string) (map[string]int64, error) {
	counts := make(map[string]int64)
	if len(repoIDs) == 0 {
		return counts, nil
	}
	var rows []struct {
		RepoID string
		Count  int64
	}
	err := s.db.WithContext(ctx).Model(&db.Star{}).
		Select("repo_id, COUNT(*) as count").
		Where("repo_id IN ?", repoIDs).
		Group("repo_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.RepoID] = r.Count
	}
	return counts, nil
}

// Which of these repos the user starred
func (s *Store) GetStarredSet(ctx context.Context, userID string, repoIDs []string) (map[string]bool, error) {
	starred := make(map[string]bool)
	if userID == "" || len(repoIDs) == 0 {
		return starred, nil
	}
	var ids []string
	err := s.db.WithContext(ctx).Model(&db.Star{}).
		Where("user_id = ? AND repo_id IN ?", userID, repoIDs).
		Pluck("repo_id", &ids).Error
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		starred[id] = true
	}
	return starred, nil
}

// Newest starred first
func (s *Store) ListStarredRepositories(ctx context.Context, userID string, limit, offset int) ([]*db.Repository, int64, error) {
	tx := s.db.WithContext(ctx).Model(&db.Repository{}).
		Joins("JOIN stars ON stars.repo_id = repositories.id").
		Where("stars.user_id = ?", userID)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var repos []*db.Repository
	err := tx.Order("stars.created_at DESC").Limit(limit).Offset(offset).Find(&repos).Error
	return repos, total, err
}

// ── Source repository links and moves ────────────────────────────────────

func (s *Store) GetRepositoryByID(ctx context.Context, id string) (*db.Repository, error) {
	var repo db.Repository
	err := s.db.WithContext(ctx).First(&repo, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &repo, err
}

func (s *Store) SetRepositoryGitRepo(ctx context.Context, id string, gitRepoID *string) error {
	return s.db.WithContext(ctx).Model(&db.Repository{}).Where("id = ?", id).Update("git_repo_id", gitRepoID).Error
}

func (s *Store) ListRepositoriesByGitRepo(ctx context.Context, gitRepoID string) ([]*db.Repository, error) {
	var repos []*db.Repository
	err := s.db.WithContext(ctx).Where("git_repo_id = ?", gitRepoID).Order("name ASC").Find(&repos).Error
	return repos, err
}

// Image repos named ns/name or ns/name/anything that carry no link yet
func (s *Store) ListUnlinkedRepositoriesForGit(ctx context.Context, namespace, name string) ([]*db.Repository, error) {
	var repos []*db.Repository
	err := s.db.WithContext(ctx).Where("namespace = ? AND git_repo_id IS NULL AND (name = ? OR name LIKE ? ESCAPE '\\')", namespace, name, pages.LikePrefix(name+"/")).Find(&repos).Error
	return repos, err
}

// Moves a repository row to a new address
func (s *Store) MoveRepository(ctx context.Context, id, newNamespace, newName, ownerID string, isOrg bool) error {
	return s.db.WithContext(ctx).Model(&db.Repository{}).Where("id = ?", id).Updates(map[string]any{
		"namespace": newNamespace, "name": newName, "owner_id": ownerID, "is_org_namespace": isOrg,
	}).Error
}
