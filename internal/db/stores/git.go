package stores

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"gorm.io/gorm"
)

// Returned when a redirect resolves an old address
var ErrGitRepoMoved = errors.New("repository moved")

// ── Repositories ─────────────────────────────────────────────────────────

func (s *Store) CreateGitRepository(ctx context.Context, repo *db.GitRepository) error {
	if repo.ID == "" {
		repo.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(repo).Error
}

func (s *Store) GetGitRepository(ctx context.Context, namespace, name string) (*db.GitRepository, error) {
	var repo db.GitRepository
	err := s.db.WithContext(ctx).First(&repo, "namespace = ? AND name = ?", namespace, name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &repo, nil
}

func (s *Store) GetGitRepositoryByID(ctx context.Context, id string) (*db.GitRepository, error) {
	var repo db.GitRepository
	err := s.db.WithContext(ctx).First(&repo, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &repo, nil
}

// Follows a redirect row when the address has moved
func (s *Store) ResolveGitRepository(ctx context.Context, namespace, name string) (*db.GitRepository, bool, error) {
	repo, err := s.GetGitRepository(ctx, namespace, name)
	if err != nil || repo != nil {
		return repo, false, err
	}
	var redirect db.GitRedirect
	err = s.db.WithContext(ctx).First(&redirect, "namespace = ? AND name = ?", namespace, name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	repo, err = s.GetGitRepositoryByID(ctx, redirect.RepoID)
	return repo, repo != nil, err
}

func (s *Store) GetGitRepositoriesByIDs(ctx context.Context, ids []string) (map[string]*db.GitRepository, error) {
	out := make(map[string]*db.GitRepository, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var repos []*db.GitRepository
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&repos).Error; err != nil {
		return nil, err
	}
	for _, r := range repos {
		out[r.ID] = r
	}
	return out, nil
}

type GitRepoListOptions struct {
	Namespace       string
	ViewerID        string
	IncludePrivate  bool
	GrantedRepos    []string
	OnlyRepo        string // Credential scope, exact namespace/name
	OnlyNamespace   string // Credential scope, exact namespace
	Type            v1.GitRepoType
	Topic           string
	IncludeArchived bool
	Forks           *bool
	StarredBy       string
	Query           pages.Query
	OrderBy         string
	Limit           int
	Offset          int
}

// GitReposQuery allowlists git repository list filters
var GitReposQuery = pages.Spec{
	Fields: map[string]string{
		"name":        "git_repositories.name",
		"namespace":   "git_repositories.namespace",
		"description": "git_repositories.description",
	},
	Text: []string{"git_repositories.name", "git_repositories.namespace", "git_repositories.description"},
}

// GitRepoSortColumns allowlists order_by for repository lists
var GitRepoSortColumns = map[string]bool{
	"name": true, "updated_at": true, "created_at": true, "stars": true, "size_bytes": true,
}

// Visibility clause shared by every repository listing
func gitVisibilityScope(opts GitRepoListOptions) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		if opts.OnlyRepo != "" {
			q = q.Where("(git_repositories.namespace || '/' || git_repositories.name) = ?", opts.OnlyRepo)
		}
		if opts.OnlyNamespace != "" {
			q = q.Where("git_repositories.namespace = ?", opts.OnlyNamespace)
		}
		if opts.IncludePrivate {
			return q
		}
		if opts.ViewerID == "" {
			return q.Where("git_repositories.is_private = ?", false)
		}
		cond := "git_repositories.is_private = ? OR git_repositories.owner_id = ? " +
			"OR git_repositories.namespace IN (SELECT username FROM users WHERE id = ?) " +
			"OR git_repositories.namespace IN (SELECT o.name FROM organizations o JOIN org_members om ON o.id = om.org_id WHERE om.user_id = ?) " +
			"OR git_repositories.id IN (SELECT repo_id FROM git_collaborators WHERE user_id = ? AND accepted = ?) " +
			"OR git_repositories.id IN (SELECT tr.repo_id FROM org_team_repos tr JOIN org_team_members tm ON tm.team_id = tr.team_id WHERE tm.user_id = ?)"
		args := []any{false, opts.ViewerID, opts.ViewerID, opts.ViewerID, opts.ViewerID, true, opts.ViewerID}
		if len(opts.GrantedRepos) > 0 {
			cond += " OR (git_repositories.namespace || '/' || git_repositories.name) IN ?"
			args = append(args, opts.GrantedRepos)
		}
		return q.Where(cond, args...)
	}
}

func (s *Store) ListGitRepositories(ctx context.Context, opts GitRepoListOptions) ([]*db.GitRepository, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitRepository{})
	if opts.Namespace != "" {
		q = q.Where("git_repositories.namespace = ?", opts.Namespace)
	}
	if opts.Type != v1.GitRepoType_GIT_REPO_TYPE_UNSPECIFIED {
		q = q.Where("git_repositories.type = ?", opts.Type)
	}
	if opts.Topic != "" {
		q = q.Where("git_repositories.id IN (SELECT repo_id FROM git_topics WHERE topic = ?)", opts.Topic)
	}
	if !opts.IncludeArchived {
		q = q.Where("git_repositories.is_archived = ?", false)
	}
	if opts.Forks != nil {
		if *opts.Forks {
			q = q.Where("git_repositories.fork_of_id IS NOT NULL")
		} else {
			q = q.Where("git_repositories.fork_of_id IS NULL")
		}
	}
	if opts.StarredBy != "" {
		q = q.Joins("JOIN stars ON stars.target_id = git_repositories.id AND stars.kind = ? AND stars.user_id = ?", db.StarKindGit, opts.StarredBy)
	}
	q = q.Scopes(gitVisibilityScope(opts), GitReposQuery.Scope(opts.Query))

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	order := opts.OrderBy
	switch {
	case order == "" && opts.StarredBy != "":
		order = "stars.created_at DESC"
	case order == "":
		order = "git_repositories.updated_at DESC"
	case order == "stars ASC" || order == "stars DESC":
		order = "(SELECT COUNT(*) FROM stars gs WHERE gs.kind = '" + db.StarKindGit + "' AND gs.target_id = git_repositories.id) " + order[len("stars "):]
	default:
		order = "git_repositories." + order
	}
	q = q.Order(order)
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit).Offset(opts.Offset)
	}
	var repos []*db.GitRepository
	if err := q.Find(&repos).Error; err != nil {
		return nil, 0, err
	}
	return repos, total, nil
}

func (s *Store) UpdateGitRepository(ctx context.Context, repo *db.GitRepository) error {
	return s.db.WithContext(ctx).Save(repo).Error
}

// Targeted column writes so concurrent edits never clobber each other
func (s *Store) UpdateGitRepositoryColumns(ctx context.Context, id string, cols map[string]any) error {
	return s.db.WithContext(ctx).Model(&db.GitRepository{}).Where("id = ?", id).Updates(cols).Error
}

func (s *Store) SetGitRepoMirrorStatus(ctx context.Context, id string, at time.Time, errMsg, state string) error {
	return s.UpdateGitRepositoryColumns(ctx, id, map[string]any{"mirror_last_sync": at, "mirror_last_error": errMsg, "mirror_state": state})
}

// Drops the upstream and every trace of it, the repository becomes standard
func (s *Store) DetachGitRepoMirror(ctx context.Context, id string) error {
	return s.UpdateGitRepositoryColumns(ctx, id, map[string]any{
		"type":              v1.GitRepoType_GIT_REPO_TYPE_STANDARD,
		"mirror_config":     "",
		"mirror_state":      "",
		"mirror_last_sync":  nil,
		"mirror_last_error": "",
		"updated_at":        time.Now().UTC(),
	})
}

func (s *Store) ListMirrorGitRepositories(ctx context.Context) ([]*db.GitRepository, error) {
	var repos []*db.GitRepository
	err := s.db.WithContext(ctx).Where("type = ?", v1.GitRepoType_GIT_REPO_TYPE_MIRROR).Order("namespace ASC, name ASC").Find(&repos).Error
	return repos, err
}

func (s *Store) ListAllGitRepositories(ctx context.Context) ([]*db.GitRepository, error) {
	var repos []*db.GitRepository
	err := s.db.WithContext(ctx).Order("namespace ASC, name ASC").Find(&repos).Error
	return repos, err
}

func (s *Store) ListGitRepositoriesByNamespace(ctx context.Context, namespace string) ([]*db.GitRepository, error) {
	var repos []*db.GitRepository
	err := s.db.WithContext(ctx).Where("namespace = ?", namespace).Find(&repos).Error
	return repos, err
}

func (s *Store) ListGitForks(ctx context.Context, parentID string, limit, offset int) ([]*db.GitRepository, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitRepository{}).Where("fork_of_id = ?", parentID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var repos []*db.GitRepository
	q = q.Order("created_at DESC")
	// Zero means every row, gorm would emit LIMIT 0
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Find(&repos).Error
	return repos, total, err
}

func (s *Store) CountGitForks(ctx context.Context, parentID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.GitRepository{}).Where("fork_of_id = ?", parentID).Count(&n).Error
	return n, err
}

// Removes the row and every repo scoped record no foreign key covers
func (s *Store) DeleteGitRepository(ctx context.Context, repo *db.GitRepository) error {
	full := repo.Namespace + "/" + repo.Name
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("scope_type = ? AND scope_id = ?", int32(v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO), full).Delete(&db.APIToken{}).Error; err != nil {
			return err
		}
		if err := tx.Where("owner_type = ? AND owner_id = ?", db.SSHKeyOwnerGitRepo, repo.ID).Delete(&db.SSHKey{}).Error; err != nil {
			return err
		}
		for _, model := range []any{&db.ActionsSecret{}, &db.ActionsVariable{}, &db.ActionsRunner{}, &db.ActionsRunnerToken{}} {
			if err := tx.Where("scope_kind = ? AND scope_id = ?", db.ActionsScopeRepo, repo.ID).Delete(model).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("scope_type = ? AND scope_id = ?", int32(v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO), repo.ID).Delete(&db.SettingsRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("kind = ? AND target_id = ?", db.StarKindGit, repo.ID).Delete(&db.Star{}).Error; err != nil {
			return err
		}
		return tx.Delete(&db.GitRepository{}, "id = ?", repo.ID).Error
	})
}

func (s *Store) IncrementGitPullCount(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Model(&db.GitRepository{}).Where("id = ?", id).
		UpdateColumn("pull_count", gorm.Expr("pull_count + 1")).Error
}

func (s *Store) RecordGitPush(ctx context.Context, id string, size int64, branches, tags int32) error {
	return s.db.WithContext(ctx).Model(&db.GitRepository{}).Where("id = ?", id).Updates(map[string]any{
		"push_count":   gorm.Expr("push_count + 1"),
		"last_push":    time.Now().UTC(),
		"size_bytes":   size,
		"branch_count": branches,
		"tag_count":    tags,
		"updated_at":   time.Now().UTC(),
	}).Error
}

// Claims the next issue, pull, run, or session number atomically
func (s *Store) NextGitCounter(ctx context.Context, repoID, column string) (int64, error) {
	var next int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&db.GitRepository{}).Where("id = ?", repoID).
			UpdateColumn(column, gorm.Expr(column+" + 1")).Error; err != nil {
			return err
		}
		return tx.Model(&db.GitRepository{}).Where("id = ?", repoID).Pluck(column, &next).Error
	})
	return next, err
}

// Moves a repository to a new address inside one transaction
func (s *Store) MoveGitRepository(ctx context.Context, repo *db.GitRepository, newNamespace, newName, ownerID string, isOrg bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		redirect := &db.GitRedirect{ID: uuid.New().String(), Namespace: repo.Namespace, Name: repo.Name, RepoID: repo.ID}
		if err := tx.Where("namespace = ? AND name = ?", newNamespace, newName).Delete(&db.GitRedirect{}).Error; err != nil {
			return err
		}
		if err := tx.Create(redirect).Error; err != nil {
			return err
		}
		// Teams of the old org lose the repo, the new org grants through its own teams
		teamRepos := tx.Where("repo_id = ?", repo.ID)
		if isOrg {
			teamRepos = teamRepos.Where("team_id NOT IN (SELECT id FROM org_teams WHERE org_id = ?)", ownerID)
		}
		if err := teamRepos.Delete(&db.OrgTeamRepo{}).Error; err != nil {
			return err
		}
		return tx.Model(&db.GitRepository{}).Where("id = ?", repo.ID).Updates(map[string]any{
			"namespace": newNamespace, "name": newName, "owner_id": ownerID, "is_org_namespace": isOrg,
		}).Error
	})
}

// ── Transfers ────────────────────────────────────────────────────────────

func (s *Store) CreateGitTransfer(ctx context.Context, t *db.GitTransfer) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if err := s.db.WithContext(ctx).Where("repo_id = ?", t.RepoID).Delete(&db.GitTransfer{}).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *Store) GetGitTransfer(ctx context.Context, id string) (*db.GitTransfer, error) {
	var t db.GitTransfer
	err := s.db.WithContext(ctx).First(&t, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

func (s *Store) GetGitTransferByRepo(ctx context.Context, repoID string) (*db.GitTransfer, error) {
	var t db.GitTransfer
	err := s.db.WithContext(ctx).First(&t, "repo_id = ?", repoID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

func (s *Store) ListGitTransfersTo(ctx context.Context, namespaces []string, limit, offset int) ([]*db.GitTransfer, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitTransfer{}).Where("to_namespace IN ? AND expires_at > ?", namespaces, time.Now().UTC())
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitTransfer
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

func (s *Store) DeleteGitTransfer(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.GitTransfer{}, "id = ?", id).Error
}

// ── Topics ───────────────────────────────────────────────────────────────

func (s *Store) SetGitTopics(ctx context.Context, repoID string, topics []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("repo_id = ?", repoID).Delete(&db.GitTopic{}).Error; err != nil {
			return err
		}
		for _, t := range topics {
			if err := tx.Create(&db.GitTopic{RepoID: repoID, Topic: t}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetGitTopics(ctx context.Context, repoIDs []string) (map[string][]string, error) {
	out := make(map[string][]string)
	if len(repoIDs) == 0 {
		return out, nil
	}
	var rows []db.GitTopic
	if err := s.db.WithContext(ctx).Where("repo_id IN ?", repoIDs).Order("topic ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.RepoID] = append(out[r.RepoID], r.Topic)
	}
	return out, nil
}

// ── Watches ──────────────────────────────────────────────────────────────

func (s *Store) SetGitWatch(ctx context.Context, userID, repoID, mode string) error {
	var existing db.GitWatch
	err := s.db.WithContext(ctx).First(&existing, "user_id = ? AND repo_id = ?", userID, repoID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.WithContext(ctx).Create(&db.GitWatch{ID: uuid.New().String(), UserID: userID, RepoID: repoID, Mode: mode}).Error
	}
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&existing).Update("mode", mode).Error
}

func (s *Store) GetGitWatch(ctx context.Context, userID, repoID string) (string, error) {
	var w db.GitWatch
	err := s.db.WithContext(ctx).First(&w, "user_id = ? AND repo_id = ?", userID, repoID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return w.Mode, err
}

// Users watching every event on the repo
func (s *Store) ListGitWatchers(ctx context.Context, repoID, mode string) ([]string, error) {
	var ids []string
	err := s.db.WithContext(ctx).Model(&db.GitWatch{}).Where("repo_id = ? AND mode = ?", repoID, mode).Pluck("user_id", &ids).Error
	return ids, err
}

// ── Collaborators ────────────────────────────────────────────────────────

func (s *Store) UpsertGitCollaborator(ctx context.Context, c *db.GitCollaborator) error {
	var existing db.GitCollaborator
	err := s.db.WithContext(ctx).First(&existing, "repo_id = ? AND user_id = ?", c.RepoID, c.UserID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if c.ID == "" {
			c.ID = uuid.New().String()
		}
		return s.db.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}
	c.ID = existing.ID
	return s.db.WithContext(ctx).Model(&existing).Updates(map[string]any{"permission": c.Permission, "accepted": c.Accepted}).Error
}

func (s *Store) GetGitCollaborator(ctx context.Context, repoID, userID string) (*db.GitCollaborator, error) {
	var c db.GitCollaborator
	err := s.db.WithContext(ctx).First(&c, "repo_id = ? AND user_id = ?", repoID, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

// Accepted collaborators among the users, keyed by user id
func (s *Store) AcceptedGitCollaborators(ctx context.Context, repoID string, userIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []db.GitCollaborator
	if err := s.db.WithContext(ctx).Where("repo_id = ? AND accepted = ? AND user_id IN ?", repoID, true, userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, c := range rows {
		out[c.UserID] = c.Permission
	}
	return out, nil
}

func (s *Store) ListGitCollaborators(ctx context.Context, repoID string, limit, offset int) ([]*db.GitCollaborator, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitCollaborator{}).Where("repo_id = ?", repoID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitCollaborator
	err := q.Preload("User").Order("created_at ASC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

func (s *Store) DeleteGitCollaborator(ctx context.Context, repoID, userID string) error {
	return s.db.WithContext(ctx).Where("repo_id = ? AND user_id = ?", repoID, userID).Delete(&db.GitCollaborator{}).Error
}

// ── Protections ──────────────────────────────────────────────────────────

func (s *Store) SaveGitBranchProtection(ctx context.Context, p *db.GitBranchProtection) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
		return s.db.WithContext(ctx).Create(p).Error
	}
	return s.db.WithContext(ctx).Save(p).Error
}

func (s *Store) ListGitBranchProtections(ctx context.Context, repoID string) ([]*db.GitBranchProtection, error) {
	var out []*db.GitBranchProtection
	err := s.db.WithContext(ctx).Where("repo_id = ?", repoID).Order("priority ASC, created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) GetGitBranchProtection(ctx context.Context, repoID, id string) (*db.GitBranchProtection, error) {
	var p db.GitBranchProtection
	err := s.db.WithContext(ctx).First(&p, "repo_id = ? AND id = ?", repoID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}

func (s *Store) DeleteGitBranchProtection(ctx context.Context, repoID, id string) error {
	return s.db.WithContext(ctx).Where("repo_id = ? AND id = ?", repoID, id).Delete(&db.GitBranchProtection{}).Error
}

func (s *Store) GetGitTagProtection(ctx context.Context, repoID, id string) (*db.GitTagProtection, error) {
	var p db.GitTagProtection
	err := s.db.WithContext(ctx).First(&p, "repo_id = ? AND id = ?", repoID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}

func (s *Store) SaveGitTagProtection(ctx context.Context, p *db.GitTagProtection) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
		return s.db.WithContext(ctx).Create(p).Error
	}
	return s.db.WithContext(ctx).Save(p).Error
}

func (s *Store) ListGitTagProtections(ctx context.Context, repoID string) ([]*db.GitTagProtection, error) {
	var out []*db.GitTagProtection
	err := s.db.WithContext(ctx).Where("repo_id = ?", repoID).Order("created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) DeleteGitTagProtection(ctx context.Context, repoID, id string) error {
	return s.db.WithContext(ctx).Where("repo_id = ? AND id = ?", repoID, id).Delete(&db.GitTagProtection{}).Error
}

// ── Commit statuses ──────────────────────────────────────────────────────

func (s *Store) CreateGitCommitStatus(ctx context.Context, st *db.GitCommitStatus) error {
	if st.ID == "" {
		st.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(st).Error
}

func (s *Store) ListGitCommitStatuses(ctx context.Context, repoID, sha string, limit, offset int) ([]*db.GitCommitStatus, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.GitCommitStatus{}).Where("repo_id = ? AND sha = ?", repoID, sha)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []*db.GitCommitStatus
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	err := q.Order("created_at DESC").Find(&out).Error
	return out, total, err
}

// Newest status per context
func (s *Store) LatestGitCommitStatuses(ctx context.Context, repoID, sha string) ([]*db.GitCommitStatus, error) {
	var all []*db.GitCommitStatus
	err := s.db.WithContext(ctx).
		Where("repo_id = ? AND sha = ?", repoID, sha).
		Where("created_at = (SELECT MAX(s2.created_at) FROM git_commit_statuses s2 WHERE s2.repo_id = git_commit_statuses.repo_id AND s2.sha = git_commit_statuses.sha AND s2.context = git_commit_statuses.context)").
		Order("created_at DESC").Find(&all).Error
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []*db.GitCommitStatus
	for _, st := range all {
		if seen[st.Context] {
			continue
		}
		seen[st.Context] = true
		out = append(out, st)
	}
	return out, nil
}

// Newest status per context for many commits in one query
func (s *Store) LatestGitCommitStatusesBulk(ctx context.Context, repoID string, shas []string) (map[string][]*db.GitCommitStatus, error) {
	out := make(map[string][]*db.GitCommitStatus)
	if len(shas) == 0 {
		return out, nil
	}
	var all []*db.GitCommitStatus
	err := s.db.WithContext(ctx).
		Where("repo_id = ? AND sha IN ?", repoID, shas).
		Where("created_at = (SELECT MAX(s2.created_at) FROM git_commit_statuses s2 WHERE s2.repo_id = git_commit_statuses.repo_id AND s2.sha = git_commit_statuses.sha AND s2.context = git_commit_statuses.context)").
		Order("created_at DESC").Find(&all).Error
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, st := range all {
		key := st.SHA + "\x00" + st.Context
		if seen[key] {
			continue
		}
		seen[key] = true
		out[st.SHA] = append(out[st.SHA], st)
	}
	return out, nil
}

// ── LFS ──────────────────────────────────────────────────────────────────

func (s *Store) UpsertLFSObject(ctx context.Context, repoID, oid string, size int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var obj db.LFSObject
		err := tx.First(&obj, "oid = ?", oid).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&db.LFSObject{OID: oid, Size: size}).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var link db.GitLFSLink
		err = tx.First(&link, "repo_id = ? AND oid = ?", repoID, oid).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&db.GitLFSLink{RepoID: repoID, OID: oid}).Error
		}
		return err
	})
}

// Repositories linking one object, capped
func (s *Store) ListLFSLinkRepoIDs(ctx context.Context, oid string, limit int) ([]string, error) {
	var ids []string
	err := s.db.WithContext(ctx).Model(&db.GitLFSLink{}).Where("oid = ?", oid).Limit(limit).Pluck("repo_id", &ids).Error
	return ids, err
}

// Linked objects among the given oids, keyed by oid
func (s *Store) GetLFSObjects(ctx context.Context, repoID string, oids []string) (map[string]*db.LFSObject, error) {
	out := make(map[string]*db.LFSObject, len(oids))
	if len(oids) == 0 {
		return out, nil
	}
	var rows []*db.LFSObject
	err := s.db.WithContext(ctx).Joins("JOIN git_lfs_links ON git_lfs_links.oid = lfs_objects.oid AND git_lfs_links.repo_id = ?", repoID).
		Where("lfs_objects.oid IN ?", oids).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.OID] = r
	}
	return out, nil
}

func (s *Store) GetLFSObject(ctx context.Context, repoID, oid string) (*db.LFSObject, error) {
	var obj db.LFSObject
	err := s.db.WithContext(ctx).Joins("JOIN git_lfs_links ON git_lfs_links.oid = lfs_objects.oid AND git_lfs_links.repo_id = ?", repoID).
		First(&obj, "lfs_objects.oid = ?", oid).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &obj, err
}

// Repository count with object and lfs bytes summed over every repository
func (s *Store) GitStorageTotals(ctx context.Context) (count, bytes, lfsBytes int64, err error) {
	var row struct {
		Count int64
		Bytes int64
		LFS   int64
	}
	err = s.db.WithContext(ctx).Model(&db.GitRepository{}).
		Select("COUNT(*) AS count, COALESCE(SUM(size_bytes), 0) AS bytes, COALESCE(SUM(lfs_size_bytes), 0) AS lfs").
		Scan(&row).Error
	return row.Count, row.Bytes, row.LFS, err
}

func (s *Store) LFSRepoSize(ctx context.Context, repoID string) (int64, error) {
	var total int64
	err := s.db.WithContext(ctx).Raw(`SELECT COALESCE(SUM(o.size),0) FROM lfs_objects o JOIN git_lfs_links l ON l.oid = o.oid WHERE l.repo_id = ?`, repoID).Scan(&total).Error
	return total, err
}

// Drops the repo's links and returns oids nothing references anymore
func (s *Store) DeleteLFSLinks(ctx context.Context, repoID string) ([]string, error) {
	var oids []string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&db.GitLFSLink{}).Where("repo_id = ?", repoID).Pluck("oid", &oids).Error; err != nil {
			return err
		}
		if err := tx.Where("repo_id = ?", repoID).Delete(&db.GitLFSLink{}).Error; err != nil {
			return err
		}
		kept := oids[:0]
		for _, oid := range oids {
			var n int64
			if err := tx.Model(&db.GitLFSLink{}).Where("oid = ?", oid).Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				if err := tx.Delete(&db.LFSObject{}, "oid = ?", oid).Error; err != nil {
					return err
				}
				kept = append(kept, oid)
			}
		}
		oids = kept
		return nil
	})
	return oids, err
}

func (s *Store) CountLFSLinksByOID(ctx context.Context, oid string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&db.GitLFSLink{}).Where("oid = ?", oid).Count(&n).Error
	return n, err
}

func (s *Store) CreateLFSLock(ctx context.Context, l *db.LFSLock) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(l).Error
}

func (s *Store) GetLFSLock(ctx context.Context, repoID, id string) (*db.LFSLock, error) {
	var l db.LFSLock
	err := s.db.WithContext(ctx).First(&l, "repo_id = ? AND id = ?", repoID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &l, err
}

func (s *Store) ListLFSLocks(ctx context.Context, repoID, path, id string, limit int, cursor string) ([]*db.LFSLock, string, error) {
	q := s.db.WithContext(ctx).Where("repo_id = ?", repoID)
	if path != "" {
		q = q.Where("path = ?", path)
	}
	if id != "" {
		q = q.Where("id = ?", id)
	}
	if cursor != "" {
		q = q.Where("id > ?", cursor)
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var out []*db.LFSLock
	if err := q.Order("id ASC").Limit(limit + 1).Find(&out).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		next = out[limit-1].ID
		out = out[:limit]
	}
	return out, next, nil
}

func (s *Store) DeleteLFSLock(ctx context.Context, repoID, id string) error {
	return s.db.WithContext(ctx).Where("repo_id = ? AND id = ?", repoID, id).Delete(&db.LFSLock{}).Error
}

// ── Push mirrors and imports ─────────────────────────────────────────────

func (s *Store) CreateGitPushMirror(ctx context.Context, m *db.GitPushMirror) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return s.db.WithContext(ctx).Create(m).Error
}

func (s *Store) GetGitPushMirror(ctx context.Context, repoID, id string) (*db.GitPushMirror, error) {
	var m db.GitPushMirror
	err := s.db.WithContext(ctx).First(&m, "repo_id = ? AND id = ?", repoID, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

func (s *Store) ListGitPushMirrors(ctx context.Context, repoID string) ([]*db.GitPushMirror, error) {
	var out []*db.GitPushMirror
	err := s.db.WithContext(ctx).Where("repo_id = ?", repoID).Order("created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) ListAllGitPushMirrors(ctx context.Context) ([]*db.GitPushMirror, error) {
	var out []*db.GitPushMirror
	err := s.db.WithContext(ctx).Order("created_at ASC").Find(&out).Error
	return out, err
}

func (s *Store) UpdateGitPushMirror(ctx context.Context, m *db.GitPushMirror) error {
	return s.db.WithContext(ctx).Save(m).Error
}

func (s *Store) SetGitPushMirrorStatus(ctx context.Context, id string, at time.Time, errMsg, state string) error {
	return s.db.WithContext(ctx).Model(&db.GitPushMirror{}).Where("id = ?", id).
		Updates(map[string]any{"last_sync": at, "last_error": errMsg, "state": state}).Error
}

func (s *Store) DeleteGitPushMirror(ctx context.Context, repoID, id string) error {
	return s.db.WithContext(ctx).Where("repo_id = ? AND id = ?", repoID, id).Delete(&db.GitPushMirror{}).Error
}

func (s *Store) SaveGitImport(ctx context.Context, imp *db.GitImport) error {
	return s.db.WithContext(ctx).Save(imp).Error
}

func (s *Store) GetGitImport(ctx context.Context, repoID string) (*db.GitImport, error) {
	var imp db.GitImport
	err := s.db.WithContext(ctx).First(&imp, "repo_id = ?", repoID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &imp, err
}

// Import rows of several repositories keyed by repo id
func (s *Store) GetGitImportsByRepoIDs(ctx context.Context, ids []string) (map[string]*db.GitImport, error) {
	out := map[string]*db.GitImport{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []*db.GitImport
	if err := s.db.WithContext(ctx).Where("repo_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.RepoID] = r
	}
	return out, nil
}

func (s *Store) ListPendingGitImports(ctx context.Context) ([]*db.GitImport, error) {
	var out []*db.GitImport
	err := s.db.WithContext(ctx).Where("phase <> ?", "done").Find(&out).Error
	return out, err
}

func (s *Store) DeleteGitImport(ctx context.Context, repoID string) error {
	return s.db.WithContext(ctx).Delete(&db.GitImport{}, "repo_id = ?", repoID).Error
}
