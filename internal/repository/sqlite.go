package repository

import (
	"encoding/json"
	"fmt"
	"time"

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

func (r *GormRepository) GetDB() *gorm.DB {
	return r.db
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
	result := r.db.Model(&models.User{}).Where("username = ?", username).Update("groups", []string(groups))
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

	return r.db.Model(&models.User{}).Where("username = ?", username).Update("groups", []string(newGroups)).Error
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

func (r *GormRepository) DeleteImageMetadata(id string) error {
	var count int64
	if err := r.db.Model(&models.UserImage{}).Where("image_id = ?", id).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check references to image metadata: %v", err)
	}

	if count > 0 {
		return fmt.Errorf("cannot delete image metadata while %d user images reference it", count)
	}

	result := r.db.Delete(&models.ImageMetadata{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete image metadata: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("image metadata not found: %s", id)
	}
	return nil
}

func (r *GormRepository) ListUserImages(username string) ([]*models.UserImage, error) {
	var userImages []*models.UserImage
	if err := r.db.Where("username = ?", username).Find(&userImages).Error; err != nil {
		return nil, fmt.Errorf("query failed: %v", err)
	}
	return userImages, nil
}

func (r *GormRepository) GetUserImage(username string, name string, tag string) (*models.UserImage, error) {
	var userImage models.UserImage
	if err := r.db.Where("username = ? AND name = ? AND tag = ?", username, name, tag).First(&userImage).Error; err != nil {
		return nil, err
	}
	return &userImage, nil
}

func (r *GormRepository) CreateUserImage(userImage *models.UserImage) error {
	var count int64
	if err := r.db.Model(&models.ImageMetadata{}).Where("id = ?", userImage.ImageID).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check if image metadata exists: %v", err)
	}

	if count == 0 {
		return fmt.Errorf("referenced image metadata with id %s does not exist", userImage.ImageID)
	}

	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "username"}, {Name: "name"}, {Name: "tag"}},
		DoUpdates: clause.Assignments(map[string]any{
			"image_id":   userImage.ImageID,
			"private":    userImage.Private,
			"updated_at": time.Now(),
		}),
	}).Create(userImage).Error
}

func (r *GormRepository) UpdateUserImage(userImage *models.UserImage) error {
	result := r.db.Model(&models.UserImage{}).Where("username = ? AND name = ? AND tag = ?",
		userImage.Username, userImage.Name, userImage.Tag).Updates(map[string]any{
		"image_id": userImage.ImageID,
		"private":  userImage.Private,
	})

	if result.Error != nil {
		return fmt.Errorf("failed to update user image: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user image not found for %s/%s:%s", userImage.Username, userImage.Name, userImage.Tag)
	}
	return nil
}

func (r *GormRepository) DeleteUserImage(username string, name string, tag string) error {
	result := r.db.Where("username = ? AND name = ? AND tag = ?", username, name, tag).Delete(&models.UserImage{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete user image: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user image not found for %s/%s:%s", username, name, tag)
	}

	// CHECK FOR METADATA WE NEED TO RM
	var userImage models.UserImage
	if err := r.db.Unscoped().Where("username = ? AND name = ? AND tag = ?", username, name, tag).First(&userImage).Error; err != nil {
		// THIS SHOULDNT HAPPEN
		return nil
	}

	// CHECK FOR OTHER USER REFS
	var count int64
	if err := r.db.Model(&models.UserImage{}).Where("image_id = ?", userImage.ImageID).Count(&count).Error; err != nil {
		return nil
	}

	// IF NO REFS, DELETE METADATA TOO
	if count == 0 {
		r.db.Delete(&models.ImageMetadata{}, "id = ?", userImage.ImageID)
	}

	return nil
}

func (r *GormRepository) ListPublicUserImages() ([]*models.UserImage, error) {
	var userImages []*models.UserImage
	if err := r.db.Where("private IS NULL OR private = ?", false).
		Order("created_at DESC").
		Find(&userImages).Error; err != nil {
		return nil, fmt.Errorf("failed to query public user images: %v", err)
	}
	return userImages, nil
}

func (r *GormRepository) UpdateUserImageVisibility(id int, private bool) error {
	result := r.db.Model(&models.UserImage{}).Where("id = ?", id).Update("private", private)
	if result.Error != nil {
		return fmt.Errorf("failed to update visibility: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user image not found with id %d", id)
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
	if err := r.db.Where("owner = ? OR (private IS NULL OR private = ?)", username, false).Find(&repos).Error; err != nil {
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
	if err := r.db.Where("repo_id = ?", repoID).
		Preload("Properties").
		Find(&artifacts).Error; err != nil {
		return nil, err
	}

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

	// LOAD ARTIFACT WITH PROPS PRIOR TO DELETE
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
	// WE SHOULDNT HIT BECAUSE WE SET ANONYMOUS IN HANDLER
	if criteria.Username == "" {
		return nil, fmt.Errorf("no username provided to search")
	}

	// BASE QUERY
	query := r.db.Model(&models.Artifact{}).
		Distinct().
		Joins("JOIN artifact_repositories ON artifacts.repo_id = artifact_repositories.id").
		Where("(artifact_repositories.owner = ? OR artifact_repositories.private IS NULL OR artifact_repositories.private = ?)", criteria.Username, false)

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

	// PROP FILTER SUBQUERIES
	if len(criteria.Properties) > 0 {
		for key, value := range criteria.Properties {
			subQuery := r.db.Model(&models.ArtifactProperty{}).
				Select("1").
				Where("artifact_properties.artifact_id = artifacts.id").
				Where("artifact_properties.key = ? AND artifact_properties.value = ?", key, value)

			query = query.Where("EXISTS (?)", subQuery)
		}
	}

	// APPLY SORT
	if criteria.Sort != "" {
		// VALIDATE SORT FIELDS
		validSortFields := map[string]string{
			"name":       "artifacts.name",
			"version":    "artifacts.version",
			"path":       "artifacts.path",
			"size":       "artifacts.size",
			"created_at": "artifacts.created_at",
			"updated_at": "artifacts.updated_at",
		}

		if sortField, valid := validSortFields[criteria.Sort]; valid {
			order := "DESC"
			if criteria.Order == "asc" || criteria.Order == "ASC" {
				order = "ASC"
			}

			query = query.Order(fmt.Sprintf("%s %s", sortField, order))
		} else {
			return nil, fmt.Errorf("invalid sort field: %s", criteria.Sort)
		}
	} else {
		query = query.Order("artifacts.created_at DESC")
	}

	// PAGINATION
	if criteria.Limit > 0 {
		query = query.Limit(criteria.Limit)
	}

	if criteria.Offset > 0 {
		query = query.Offset(criteria.Offset)
	}

	query = query.Preload("Properties")

	// RUN QUERY
	var artifacts []models.Artifact
	if err := query.Find(&artifacts).Error; err != nil {
		return nil, fmt.Errorf("failed to execute search query: %w", err)
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
