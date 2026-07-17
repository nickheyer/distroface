package stores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// Property update would collide with another artifact
var ErrDuplicateIdentity = errors.New("an artifact with this version, path, and property set already exists")

// Canonical hash of a property set, empty keys skipped
func PropsFingerprint(properties map[string]string) string {
	keys := make([]string, 0, len(properties))
	for k := range properties {
		if k != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%d:%s%d:%s;", len(k), k, len(properties[k]), properties[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ── Artifact repository operations ───────────────────────────────────────

func (s *Store) CreateArtifactRepository(ctx context.Context, repo *db.ArtifactRepository) error {
	return s.db.WithContext(ctx).Create(repo).Error
}

func (s *Store) GetArtifactRepository(ctx context.Context, namespace, name string) (*db.ArtifactRepository, error) {
	var repo db.ArtifactRepository
	err := s.db.WithContext(ctx).First(&repo, "namespace = ? AND name = ?", namespace, name).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &repo, nil
}

type ArtifactRepoListOptions struct {
	Namespace      string   // Optional exact namespace filter
	ViewerID       string   // Owner whose private repos are visible
	IncludePrivate bool     // True bypasses visibility filtering
	GrantedRepos   []string // RBAC granted repos as namespace/name
	Search         string   // Name substring filter
	Limit          int      // Zero means no limit
	Offset         int
}

func (s *Store) ListArtifactRepositories(ctx context.Context, opts ArtifactRepoListOptions) ([]*db.ArtifactRepository, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.ArtifactRepository{})
	if opts.Namespace != "" {
		q = q.Where("namespace = ?", opts.Namespace)
	}
	if !opts.IncludePrivate {
		if opts.ViewerID != "" {
			cond := "is_private = ? OR owner_id = ? " +
				"OR namespace IN (SELECT username FROM users WHERE id = ?) " +
				"OR namespace IN (SELECT o.name FROM organizations o JOIN org_members om ON o.id = om.org_id WHERE om.user_id = ?)"
			args := []any{false, opts.ViewerID, opts.ViewerID, opts.ViewerID}
			if len(opts.GrantedRepos) > 0 {
				cond += " OR (namespace || '/' || name) IN ?"
				args = append(args, opts.GrantedRepos)
			}
			q = q.Where(cond, args...)
		} else {
			q = q.Where("is_private = ?", false)
		}
	}
	if opts.Search != "" {
		q = q.Where("name LIKE ? OR namespace LIKE ? OR description LIKE ?", "%"+opts.Search+"%", "%"+opts.Search+"%", "%"+opts.Search+"%")
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if opts.Limit > 0 {
		q = q.Limit(opts.Limit).Offset(opts.Offset)
	}

	var repos []*db.ArtifactRepository
	if err := q.Order("name ASC").Find(&repos).Error; err != nil {
		return nil, 0, err
	}
	return repos, total, nil
}

func (s *Store) UpdateArtifactRepository(ctx context.Context, repo *db.ArtifactRepository) error {
	return s.db.WithContext(ctx).Save(repo).Error
}

// Cascade delete, returns referenced digests for blob GC
func (s *Store) DeleteArtifactRepository(ctx context.Context, id int64) ([]string, error) {
	digests, err := s.ListArtifactDigestsByRepo(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Delete(&db.ArtifactRepository{}, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return digests, nil
}

// Artifact count and total size per repo
type ArtifactRepoStats struct {
	RepoID int64
	Count  int64
	Size   int64
}

func (s *Store) GetArtifactRepoStats(ctx context.Context, repoIDs []int64) (map[int64]ArtifactRepoStats, error) {
	stats := make(map[int64]ArtifactRepoStats, len(repoIDs))
	if len(repoIDs) == 0 {
		return stats, nil
	}
	var rows []ArtifactRepoStats
	err := s.db.WithContext(ctx).Model(&db.Artifact{}).
		Select("repo_id AS repo_id, COUNT(*) AS count, COALESCE(SUM(size),0) AS size").
		Where("repo_id IN ?", repoIDs).
		Group("repo_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		stats[r.RepoID] = r
	}
	return stats, nil
}

// ── Artifact operations ──────────────────────────────────────────────────

// Inserts replacing same version path properties, returns replaced digest
func (s *Store) CreateArtifact(ctx context.Context, artifact *db.Artifact, properties map[string]string) (replacedDigest string, err error) {
	if artifact.ID == "" {
		artifact.ID = uuid.New().String()
	}
	artifact.PropsHash = PropsFingerprint(properties)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing db.Artifact
		findErr := tx.First(&existing, "repo_id = ? AND version = ? AND path = ? AND props_hash = ?",
			artifact.RepoID, artifact.Version, artifact.Path, artifact.PropsHash).Error
		if findErr == nil {
			replacedDigest = existing.Digest
			if err := tx.Delete(&db.Artifact{}, "id = ?", existing.ID).Error; err != nil {
				return err
			}
		} else if findErr != gorm.ErrRecordNotFound {
			return findErr
		}

		if err := tx.Create(artifact).Error; err != nil {
			return err
		}
		return createPropertiesTx(tx, artifact.ID, properties)
	})
	if err != nil {
		return "", err
	}
	artifact.Properties = properties
	return replacedDigest, nil
}

func createPropertiesTx(tx *gorm.DB, artifactID string, properties map[string]string) error {
	for k, v := range properties {
		if k == "" {
			continue
		}
		if err := tx.Create(&db.ArtifactProperty{ArtifactID: artifactID, Key: k, Value: v}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetArtifact(ctx context.Context, id string) (*db.Artifact, error) {
	var artifact db.Artifact
	err := s.db.WithContext(ctx).First(&artifact, "id = ?", id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadArtifactProperties(ctx, []*db.Artifact{&artifact}); err != nil {
		return nil, err
	}
	return &artifact, nil
}

// Newest match, property variants can share version and path
func (s *Store) GetArtifactByPathVersion(ctx context.Context, repoID int64, version, path string) (*db.Artifact, error) {
	var artifact db.Artifact
	err := s.db.WithContext(ctx).Order("created_at DESC, id DESC").
		First(&artifact, "repo_id = ? AND version = ? AND path = ?", repoID, version, path).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadArtifactProperties(ctx, []*db.Artifact{&artifact}); err != nil {
		return nil, err
	}
	return &artifact, nil
}

// Row matching the full four part identity
func (s *Store) GetArtifactByIdentity(ctx context.Context, repoID int64, version, path string, properties map[string]string) (*db.Artifact, error) {
	var artifact db.Artifact
	err := s.db.WithContext(ctx).First(&artifact, "repo_id = ? AND version = ? AND path = ? AND props_hash = ?",
		repoID, version, path, PropsFingerprint(properties)).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadArtifactProperties(ctx, []*db.Artifact{&artifact}); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (s *Store) ListArtifacts(ctx context.Context, repoID int64, version string, limit, offset int) ([]*db.Artifact, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.Artifact{}).Where("repo_id = ?", repoID)
	if version != "" {
		q = q.Where("version = ?", version)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}

	var artifacts []*db.Artifact
	if err := q.Order("created_at DESC").Find(&artifacts).Error; err != nil {
		return nil, 0, err
	}
	if err := s.loadArtifactProperties(ctx, artifacts); err != nil {
		return nil, 0, err
	}
	return artifacts, total, nil
}

type ArtifactSearchCriteria struct {
	RepoID     *int64
	RepoIDs    []int64 // Visibility filter, empty means unrestricted
	Name       string  // LIKE substring
	Version    string  // LIKE substring
	Path       string  // LIKE substring
	Properties map[string]string
	Sort       string // Sort column, falls back to created_at
	Order      string // ASC or DESC, defaults DESC
	Limit      int    // Zero means no limit
	Offset     int
}

var artifactSortColumns = map[string]bool{
	"name": true, "version": true, "path": true,
	"size": true, "created_at": true, "updated_at": true,
}

func (s *Store) SearchArtifacts(ctx context.Context, criteria ArtifactSearchCriteria) ([]*db.Artifact, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.Artifact{})
	if criteria.RepoID != nil {
		q = q.Where("repo_id = ?", *criteria.RepoID)
	}
	if len(criteria.RepoIDs) > 0 {
		q = q.Where("repo_id IN ?", criteria.RepoIDs)
	}
	if criteria.Name != "" {
		q = q.Where("name LIKE ?", "%"+criteria.Name+"%")
	}
	if criteria.Version != "" {
		q = q.Where("version LIKE ?", "%"+criteria.Version+"%")
	}
	if criteria.Path != "" {
		q = q.Where("path LIKE ?", "%"+criteria.Path+"%")
	}
	for k, v := range criteria.Properties {
		q = q.Where("EXISTS (SELECT 1 FROM artifact_properties p WHERE p.artifact_id = artifacts.id AND p.key = ? AND p.value = ?)", k, v)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	sort := criteria.Sort
	if !artifactSortColumns[sort] {
		sort = "created_at"
	}
	order := "DESC"
	if criteria.Order == "ASC" {
		order = "ASC"
	}
	q = q.Order(fmt.Sprintf("%s %s", sort, order))

	if criteria.Limit > 0 {
		q = q.Limit(criteria.Limit).Offset(criteria.Offset)
	}

	var artifacts []*db.Artifact
	if err := q.Find(&artifacts).Error; err != nil {
		return nil, 0, err
	}
	if err := s.loadArtifactProperties(ctx, artifacts); err != nil {
		return nil, 0, err
	}
	return artifacts, total, nil
}

func (s *Store) UpdateArtifact(ctx context.Context, artifact *db.Artifact) error {
	return s.db.WithContext(ctx).Save(artifact).Error
}

// Replaces the full property set, identity hash follows
func (s *Store) SetArtifactProperties(ctx context.Context, artifactID string, properties map[string]string) error {
	hash := PropsFingerprint(properties)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var artifact db.Artifact
		if err := tx.First(&artifact, "id = ?", artifactID).Error; err != nil {
			return err
		}
		if artifact.PropsHash != hash {
			var occupied int64
			if err := tx.Model(&db.Artifact{}).Where("repo_id = ? AND version = ? AND path = ? AND props_hash = ?",
				artifact.RepoID, artifact.Version, artifact.Path, hash).Count(&occupied).Error; err != nil {
				return err
			}
			if occupied > 0 {
				return ErrDuplicateIdentity
			}
			if err := tx.Model(&db.Artifact{}).Where("id = ?", artifactID).Update("props_hash", hash).Error; err != nil {
				return err
			}
		}
		if err := tx.Delete(&db.ArtifactProperty{}, "artifact_id = ?", artifactID).Error; err != nil {
			return err
		}
		return createPropertiesTx(tx, artifactID, properties)
	})
}

// Backfills identity hashes for rows predating props_hash
func (s *Store) backfillArtifactPropsHash() error {
	var rows []*db.Artifact
	if err := s.db.Where("props_hash = ''").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if err := s.loadArtifactProperties(context.Background(), rows); err != nil {
		return err
	}
	for _, a := range rows {
		if err := s.db.Model(&db.Artifact{}).Where("id = ?", a.ID).
			Update("props_hash", PropsFingerprint(a.Properties)).Error; err != nil {
			return err
		}
	}
	return nil
}

// Backfills namespace for repos predating org scoping
func (s *Store) backfillArtifactRepoNamespace() error {
	var repos []*db.ArtifactRepository
	if err := s.db.Where("namespace = '' OR namespace IS NULL").Find(&repos).Error; err != nil {
		return err
	}
	ctx := context.Background()
	hasCasbin := s.db.Migrator().HasTable("casbin_rule")
	for _, r := range repos {
		ns := ""
		if r.OwnerID != "" {
			if u, err := s.GetUserByID(ctx, r.OwnerID); err == nil && u != nil {
				ns = u.Username
			} else if org, err := s.GetOrganizationByID(ctx, r.OwnerID); err == nil && org != nil {
				ns = org.Name
			}
		}
		if ns == "" {
			ns = r.Name // Names were globally unique pre migration so this stays unique
		}
		if err := s.db.Model(&db.ArtifactRepository{}).Where("id = ?", r.ID).Update("namespace", ns).Error; err != nil {
			return err
		}
		// Scoped grants match on the object string, follow the rename
		if hasCasbin {
			if err := s.db.Exec("UPDATE casbin_rule SET v3 = ? WHERE ptype = 'p' AND v1 = 'artifacts' AND v3 = ?", ns+"/"+r.Name, r.Name).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// Properties cascade with the row
func (s *Store) DeleteArtifact(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&db.Artifact{}, "id = ?", id).Error
}

// ── Blob reference counting ──────────────────────────────────────────────

// Sums one size per distinct digest matching disk usage
func (s *Store) ArtifactUniqueBlobBytes(ctx context.Context) (int64, error) {
	var total int64
	err := s.db.WithContext(ctx).
		Raw(`SELECT COALESCE(SUM(size),0) FROM (SELECT MAX(size) AS size FROM artifacts GROUP BY digest)`).
		Scan(&total).Error
	return total, err
}

func (s *Store) CountArtifactsByDigest(ctx context.Context, digest string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&db.Artifact{}).Where("digest = ?", digest).Count(&count).Error
	return count, err
}

func (s *Store) ListArtifactDigestsByRepo(ctx context.Context, repoID int64) ([]string, error) {
	var digests []string
	err := s.db.WithContext(ctx).Model(&db.Artifact{}).
		Distinct("digest").Where("repo_id = ?", repoID).Pluck("digest", &digests).Error
	return digests, err
}

// ── Helpers ──────────────────────────────────────────────────────────────

func (s *Store) loadArtifactProperties(ctx context.Context, artifacts []*db.Artifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	ids := make([]string, len(artifacts))
	byID := make(map[string]*db.Artifact, len(artifacts))
	for i, a := range artifacts {
		ids[i] = a.ID
		byID[a.ID] = a
		a.Properties = map[string]string{}
	}

	var props []db.ArtifactProperty
	if err := s.db.WithContext(ctx).Where("artifact_id IN ?", ids).Find(&props).Error; err != nil {
		return err
	}
	for _, p := range props {
		if a := byID[p.ArtifactID]; a != nil {
			a.Properties[p.Key] = p.Value
		}
	}
	return nil
}
