package repository

import (
	"encoding/json"
	"fmt"

	"slices"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) Repository {
	return &GormRepository{db: db}
}

// USER OPERATIONS
func (r *GormRepository) GetUser(username string) (*models.User, error) {
	var user models.User
	result := r.db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		return nil, result.Error
	}
	return &user, nil
}

func (r *GormRepository) CreateUser(user *models.User) error {
	return r.db.Create(user).Error
}

func (r *GormRepository) UpdateUser(user *models.User) error {
	result := r.db.Model(&models.User{}).Where("username = ?", user.Username).Updates(map[string]any{
		"password": user.Password,
		"groups":   user.Groups,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found: %s", user.Username)
	}
	return nil
}

func (r *GormRepository) UpdateUserGroups(username string, groups []string) error {
	result := r.db.Model(&models.User{}).Where("username = ?", username).Update("groups", models.StringArray(groups))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found: %s", username)
	}
	return nil
}

func (r *GormRepository) DeleteUser(username string) error {
	result := r.db.Where("username = ?", username).Delete(&models.User{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found: %s", username)
	}
	return nil
}

func (r *GormRepository) ListUsers() ([]*models.User, error) {
	var users []*models.User
	if err := r.db.Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// GROUP OPERATIONS
func (r *GormRepository) GetGroup(name string) (*models.Group, error) {
	var group models.Group
	result := r.db.Where("name = ?", name).First(&group)
	if result.Error != nil {
		return nil, result.Error
	}
	return &group, nil
}

func (r *GormRepository) CreateGroup(group *models.Group) error {
	// Set default scope if not provided
	if group.Scope == "" {
		group.Scope = "system:default"
	}
	return r.db.Create(group).Error
}

func (r *GormRepository) UpdateGroup(group *models.Group) error {
	result := r.db.Model(&models.Group{}).Where("name = ?", group.Name).Updates(map[string]any{
		"description": group.Description,
		"roles":       group.Roles,
		"scope":       group.Scope,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("group not found: %s", group.Name)
	}
	return nil
}

func (r *GormRepository) DeleteGroup(name string) error {
	result := r.db.Where("name = ?", name).Delete(&models.Group{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("group not found: %s", name)
	}
	return nil
}

func (r *GormRepository) ListGroups() ([]*models.Group, error) {
	var groups []*models.Group
	if err := r.db.Find(&groups).Error; err != nil {
		return nil, err
	}
	return groups, nil
}

func (r *GormRepository) AddUserToGroup(username string, groupName string) error {
	user, err := r.GetUser(username)
	if err != nil {
		return err
	}

	if slices.Contains(user.Groups, groupName) {
		return nil
	}

	user.Groups = append(user.Groups, groupName)

	return r.db.Model(&models.User{}).Where("username = ?", username).Update("groups", user.Groups).Error
}

func (r *GormRepository) RemoveUserFromGroup(username string, groupName string) error {
	user, err := r.GetUser(username)
	if err != nil {
		return err
	}

	var newGroups []string
	for _, g := range user.Groups {
		if g != groupName {
			newGroups = append(newGroups, g)
		}
	}

	return r.db.Model(&models.User{}).Where("username = ?", username).Update("groups", models.StringArray(newGroups)).Error
}

func (r *GormRepository) GetUserGroups(username string) ([]string, error) {
	var user models.User
	if err := r.db.Select("groups").Where("username = ?", username).First(&user).Error; err != nil {
		return nil, err
	}
	return user.Groups, nil
}

// ROLE OPERATIONS
func (r *GormRepository) ListRoles() ([]*models.Role, error) {
	var roles []*models.Role
	if err := r.db.Find(&roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}

func (r *GormRepository) GetRole(name string) (*models.Role, error) {
	var role models.Role
	if err := r.db.Where("name = ?", name).First(&role).Error; err != nil {
		return nil, err
	}
	return &role, nil
}

func (r *GormRepository) CreateRole(role *models.Role) error {
	return r.db.Create(role).Error
}

func (r *GormRepository) UpdateRole(role *models.Role) error {
	result := r.db.Model(&models.Role{}).Where("name = ?", role.Name).Updates(map[string]any{
		"description": role.Description,
		"permissions": role.Permissions,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("role not found: %s", role.Name)
	}
	return nil
}

func (r *GormRepository) DeleteRole(name string) error {
	result := r.db.Where("name = ?", name).Delete(&models.Role{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("role not found: %s", name)
	}
	return nil
}

// IMAGE OPERATIONS
func (r *GormRepository) ListImageMetadata(owner string) ([]*models.ImageMetadata, error) {
	var metadata []*models.ImageMetadata
	if err := r.db.Where("owner = ?", owner).Find(&metadata).Error; err != nil {
		return nil, fmt.Errorf("query failed: %v", err)
	}
	return metadata, nil
}

func (r *GormRepository) GetImageMetadata(id string) (*models.ImageMetadata, error) {
	var metadata models.ImageMetadata
	if err := r.db.Where("id = ?", id).First(&metadata).Error; err != nil {
		return nil, err
	}
	return &metadata, nil
}

func (r *GormRepository) CreateImageMetadata(metadata *models.ImageMetadata) error {
	return r.db.Create(metadata).Error
}

func (r *GormRepository) UpdateImageMetadata(metadata *models.ImageMetadata) error {
	result := r.db.Model(&models.ImageMetadata{}).Where("id = ? AND owner = ?", metadata.ID, metadata.Owner).Updates(map[string]any{
		"name":   metadata.Name,
		"tags":   metadata.Tags,
		"size":   metadata.Size,
		"labels": metadata.Labels,
	})
	if result.Error != nil {
		return fmt.Errorf("failed to update image metadata: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("image not found: %s", metadata.ID)
	}
	return nil
}

func (r *GormRepository) DeleteImageMetadata(id string) error {
	result := r.db.Delete(&models.ImageMetadata{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete image metadata: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("image metadata not found: %s", id)
	}
	return nil
}

func (r *GormRepository) DeleteImageTag(repository string, tag string, owner string) error {
	var metadata models.ImageMetadata
	if err := r.db.Where("name = ? AND owner = ?", repository, owner).First(&metadata).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("repository not found: %s", repository)
		}
		return fmt.Errorf("failed to fetch repository: %v", err)
	}

	// RM TAG FROM TAGS
	var newTags []string
	tagFound := false
	for _, t := range metadata.Tags {
		if t != tag {
			newTags = append(newTags, t)
		} else {
			tagFound = true
		}
	}

	if !tagFound {
		return fmt.Errorf("tag not found: %s", tag)
	}

	// UPDATE TAGS
	result := r.db.Model(&metadata).Update("tags", models.StringArray(newTags))
	if result.Error != nil {
		return fmt.Errorf("failed to update tags: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("failed to update repository: %s", repository)
	}

	return nil
}

func (r *GormRepository) ListPublicImageMetadata() ([]*models.ImageMetadata, error) {
	var metadata []*models.ImageMetadata
	if err := r.db.Where("private = ? OR private IS NULL", false).
		Order("created_at DESC").
		Find(&metadata).Error; err != nil {
		return nil, fmt.Errorf("failed to query public images: %v", err)
	}
	return metadata, nil
}

func (r *GormRepository) UpdateImageVisibility(id string, private bool) error {
	result := r.db.Model(&models.ImageMetadata{}).Where("id = ?", id).Update("private", private)
	if result.Error != nil {
		return fmt.Errorf("failed to update visibility: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("image not found: %s", id)
	}
	return nil
}

// ARTIFACT REPOSITORY OPERATIONS
func (r *GormRepository) CreateArtifactRepository(repo *models.ArtifactRepository) error {
	return r.db.Create(repo).Error
}

func (r *GormRepository) GetArtifactRepository(name string) (*models.ArtifactRepository, error) {
	var repo models.ArtifactRepository
	if err := r.db.Where("name = ?", name).First(&repo).Error; err != nil {
		return nil, err
	}
	return &repo, nil
}

func (r *GormRepository) GetArtifactRepositoryByID(repoID string) (*models.ArtifactRepository, error) {
	var repo models.ArtifactRepository
	if err := r.db.Where("id = ?", repoID).First(&repo).Error; err != nil {
		return nil, err
	}
	return &repo, nil
}

func (r *GormRepository) ListArtifactRepositories(username string) ([]models.ArtifactRepository, error) {
	var repos []models.ArtifactRepository
	if err := r.db.Where("owner = ? OR (private = ?)", username, false).Find(&repos).Error; err != nil {
		return nil, err
	}
	return repos, nil
}

func (r *GormRepository) DeleteArtifactRepository(name string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// GET REPO ID
		var repo models.ArtifactRepository
		if err := tx.Where("name = ?", name).First(&repo).Error; err != nil {
			return err
		}

		if err := tx.Where("repo_id = ?", repo.ID).Delete(&models.Artifact{}).Error; err != nil {
			return err
		}

		// DELETE REPO
		return tx.Delete(&repo).Error
	})
}

// ARTIFACT OPERATIONS
func (r *GormRepository) CreateArtifact(artifact *models.Artifact) error {
	if artifact.ID == "" {
		artifact.ID = uuid.New().String()
	}
	return r.db.Create(artifact).Error
}

func (r *GormRepository) ListArtifacts(repoID int) ([]models.Artifact, error) {
	var artifacts []models.Artifact

	// Find artifacts with preloaded properties
	if err := r.db.Where("repo_id = ?", repoID).
		Preload("Properties").
		Find(&artifacts).Error; err != nil {
		return nil, err
	}

	// Ensure all artifacts have an ID (legacy support)
	for i := range artifacts {
		if artifacts[i].ID == "" {
			artifacts[i].ID = uuid.New().String()
			if err := r.db.Model(&artifacts[i]).Update("id", artifacts[i].ID).Error; err != nil {
				return nil, fmt.Errorf("failed to update artifact ID: %v", err)
			}
		}
	}

	return artifacts, nil
}

func (r *GormRepository) UpdateArtifactMetadata(id string, metadata string) error {
	result := r.db.Model(&models.Artifact{}).Where("id = ?", id).Update("metadata", metadata)
	return result.Error
}

func (r *GormRepository) DeleteArtifact(repoID int, version string, id string) (models.Artifact, error) {
	var artifact models.Artifact

	// Load artifact with properties before deletion
	if err := r.db.Where("repo_id = ? AND version = ? AND id = ?", repoID, version, id).
		Preload("Properties").
		First(&artifact).Error; err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}

	// DELETE ARTIFACT (WILL CASCADE DELETE PROPS)
	if err := r.db.Delete(&artifact).Error; err != nil {
		return models.Artifact{}, err
	}

	return artifact, nil
}

func (r *GormRepository) DeleteArtifactByPath(repoID int, version string, path string) (models.Artifact, error) {
	var artifact models.Artifact

	if err := r.db.Where("repo_id = ? AND version = ? AND path = ?", repoID, version, path).
		Preload("Properties").
		First(&artifact).Error; err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}

	if err := r.db.Delete(&artifact).Error; err != nil {
		return models.Artifact{}, err
	}

	return artifact, nil
}

func (r *GormRepository) DeleteArtifactByUploadID(repoID int, uploadID string) (models.Artifact, error) {
	var artifact models.Artifact

	if err := r.db.Where("repo_id = ? AND upload_id = ?", repoID, uploadID).
		Preload("Properties").
		First(&artifact).Error; err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}

	if err := r.db.Delete(&artifact).Error; err != nil {
		return models.Artifact{}, err
	}

	return artifact, nil
}

func (r *GormRepository) SearchArtifacts(criteria models.ArtifactSearchCriteria) ([]models.Artifact, error) {
	query := r.db.Model(&models.Artifact{}).
		Joins("JOIN artifact_repositories ON artifacts.repo_id = artifact_repositories.id").
		Where("(artifact_repositories.owner = ? OR artifact_repositories.private = ?)", criteria.Username, false)

	// ADD FILTERS
	if criteria.RepoID != nil {
		query = query.Where("artifacts.repo_id = ?", *criteria.RepoID)
	}

	if criteria.Name != nil {
		query = query.Where("artifacts.name LIKE ?", *criteria.Name)
	}

	if criteria.Version != nil {
		query = query.Where("artifacts.version LIKE ?", *criteria.Version)
	}

	if criteria.Path != nil {
		query = query.Where("artifacts.path LIKE ?", *criteria.Path)
	}

	query = query.Preload("Properties")

	var artifacts []models.Artifact
	if err := query.Find(&artifacts).Error; err != nil {
		return nil, fmt.Errorf("failed to execute search query: %w", err)
	}

	if len(criteria.Properties) > 0 {
		var filteredArtifacts []models.Artifact
		for _, artifact := range artifacts {
			matches := true
			props := artifact.PropertiesMap()

			for key, value := range criteria.Properties {
				if propValue, exists := props[key]; !exists || propValue != value {
					matches = false
					break
				}
			}

			if matches {
				filteredArtifacts = append(filteredArtifacts, artifact)
			}
		}
		artifacts = filteredArtifacts
	}

	// Apply sorting (client-side since we already have the data)
	// TODO: implement proper server-side sorting for large result sets

	// Apply pagination if needed
	if criteria.Limit > 0 {
		endIdx := min(criteria.Offset+criteria.Limit, len(artifacts))

		if criteria.Offset < len(artifacts) {
			artifacts = artifacts[criteria.Offset:endIdx]
		} else {
			artifacts = []models.Artifact{}
		}
	}

	return artifacts, nil
}

func (r *GormRepository) GetArtifact(artifactID string) (models.Artifact, error) {
	var artifact models.Artifact
	if err := r.db.Where("id = ?", artifactID).
		Preload("Properties").
		First(&artifact).Error; err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}
	return artifact, nil
}

func (r *GormRepository) GetArtifactByPath(repoID int, path string) (models.Artifact, error) {
	var artifact models.Artifact
	if err := r.db.Where("repo_id = ? AND path = ?", repoID, path).
		Preload("Properties").
		First(&artifact).Error; err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}
	return artifact, nil
}

func (r *GormRepository) UpdateArtifactPath(id string, name string, path string, version string) error {
	return r.db.Model(&models.Artifact{}).Where("id = ?", id).Updates(map[string]any{
		"name":    name,
		"path":    path,
		"version": version,
	}).Error
}

// ARTIFACT PROPERTY OPERATIONS
func (r *GormRepository) SetArtifactProperties(artifactID string, properties map[string]string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Delete existing properties
		if err := tx.Where("artifact_id = ?", artifactID).Delete(&models.ArtifactProperty{}).Error; err != nil {
			return err
		}

		// Create new properties
		for key, value := range properties {
			prop := models.ArtifactProperty{
				ArtifactID: artifactID,
				Key:        key,
				Value:      value,
			}
			if err := tx.Create(&prop).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *GormRepository) GetArtifactProperties(artifactID string) (map[string]string, error) {
	var properties []models.ArtifactProperty
	if err := r.db.Where("artifact_id = ?", artifactID).Find(&properties).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, prop := range properties {
		result[prop.Key] = prop.Value
	}

	return result, nil
}

// SETTINGS OPERATIONS
func (r *GormRepository) GetAllSettings() (map[string]json.RawMessage, error) {
	var settings []models.Setting
	if err := r.db.Find(&settings).Error; err != nil {
		return nil, err
	}

	result := make(map[string]json.RawMessage)
	for _, setting := range settings {
		result[setting.Key] = json.RawMessage(setting.Value)
	}

	return result, nil
}

func (r *GormRepository) GetSettingsSection(section string) (json.RawMessage, error) {
	var setting models.Setting
	result := r.db.Where("key = ?", section).First(&setting)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return json.RawMessage("{}"), nil
		}
		return nil, result.Error
	}
	return json.RawMessage(setting.Value), nil
}

func (r *GormRepository) UpdateSettingsSection(section string, settings json.RawMessage) error {
	var js json.RawMessage
	if err := json.Unmarshal(settings, &js); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}

	// UPSERT
	setting := models.Setting{
		Key:   section,
		Value: string(settings),
	}

	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&setting).Error
}

func (r *GormRepository) ResetSettingsSection(section string) error {
	settings, err := models.GetSettingsWithDefaults(section)
	if err != nil {
		return fmt.Errorf("unknown settings section: %s", section)
	}

	defaultValue, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal default settings: %v", err)
	}

	setting := models.Setting{
		Key:   section,
		Value: string(defaultValue),
	}

	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&setting).Error
}
