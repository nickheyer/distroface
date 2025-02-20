package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/models"
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

// USER OPERATIONS
func (r *SQLiteRepository) GetUser(username string) (*models.User, error) {
	var user models.User
	var groupsJSON string
	var createdAt string

	err := r.db.QueryRow(
		"SELECT id, username, password, groups, created_at FROM users WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &user.Password, &groupsJSON, &createdAt)

	if err != nil {
		return nil, err
	}

	// JSON TO GROUPS
	if err := json.Unmarshal([]byte(groupsJSON), &user.Groups); err != nil {
		return nil, fmt.Errorf("failed to parse groups: %v", err)
	}

	user.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &user, nil
}

func (r *SQLiteRepository) CreateUser(user *models.User) error {
	// GROUPS TO JSON
	groupsJSON, err := json.Marshal(user.Groups)
	if err != nil {
		return fmt.Errorf("failed to marshal groups: %v", err)
	}

	_, err = r.db.Exec(
		`INSERT INTO users (username, password, groups, created_at) 
         VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		user.Username, user.Password, string(groupsJSON),
	)
	return err
}

func (r *SQLiteRepository) UpdateUser(user *models.User) error {
	groupsJSON, err := json.Marshal(user.Groups)
	if err != nil {
		return fmt.Errorf("failed to marshal groups: %v", err)
	}

	result, err := r.db.Exec(
		`UPDATE users SET password = ?, groups = ? WHERE username = ?`,
		user.Password, string(groupsJSON), user.Username,
	)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("user not found: %s", user.Username)
	}
	return nil
}

func (r *SQLiteRepository) UpdateUserGroups(username string, groups []string) error {
	groupsJSON, err := json.Marshal(groups)
	if err != nil {
		return fmt.Errorf("failed to marshal groups: %v", err)
	}

	result, err := r.db.Exec(
		"UPDATE users SET groups = ? WHERE username = ?",
		groupsJSON, username,
	)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("user not found: %s", username)
	}
	return nil
}

func (r *SQLiteRepository) DeleteUser(username string) error {
	result, err := r.db.Exec("DELETE FROM users WHERE username = ?", username)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("user not found: %s", username)
	}
	return nil
}

func (r *SQLiteRepository) ListUsers() ([]*models.User, error) {
	rows, err := r.db.Query("SELECT id, username, groups FROM users")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		var user models.User
		var groupsJSON string

		if err := rows.Scan(&user.ID, &user.Username, &groupsJSON); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(groupsJSON), &user.Groups); err != nil {
			return nil, fmt.Errorf("failed to parse groups: %v", err)
		}

		users = append(users, &user)
	}

	return users, nil
}

// GROUP OPERATIONS
func (r *SQLiteRepository) GetGroup(name string) (*models.Group, error) {
	var group models.Group
	var rolesJSON string
	var createdAt string

	err := r.db.QueryRow(
		"SELECT id, name, description, roles, scope, created_at FROM groups WHERE name = ?",
		name,
	).Scan(&group.ID, &group.Name, &group.Description, &rolesJSON, &group.Scope, &createdAt)

	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(rolesJSON), &group.Roles); err != nil {
		return nil, fmt.Errorf("failed to parse roles: %v", err)
	}

	group.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &group, nil
}

func (r *SQLiteRepository) CreateGroup(group *models.Group) error {
	rolesJSON, err := json.Marshal(group.Roles)
	if err != nil {
		return fmt.Errorf("failed to marshal roles: %v", err)
	}

	if group.Scope == "" {
		group.Scope = "system:default"
	}

	_, err = r.db.Exec(
		`INSERT INTO groups (name, description, roles, scope, created_at) 
         VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		group.Name, group.Description, string(rolesJSON), group.Scope,
	)
	return err
}

func (r *SQLiteRepository) UpdateGroup(group *models.Group) error {
	rolesJSON, err := json.Marshal(group.Roles)
	if err != nil {
		return fmt.Errorf("failed to marshal roles: %v", err)
	}

	result, err := r.db.Exec(
		`UPDATE groups SET description = ?, roles = ?, scope = ? WHERE name = ?`,
		group.Description, string(rolesJSON), group.Scope, group.Name,
	)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("group not found: %s", group.Name)
	}
	return nil
}

func (r *SQLiteRepository) DeleteGroup(name string) error {
	result, err := r.db.Exec("DELETE FROM groups WHERE name = ?", name)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("group not found: %s", name)
	}
	return nil
}

func (r *SQLiteRepository) ListGroups() ([]*models.Group, error) {
	rows, err := r.db.Query("SELECT id, name, description, roles, scope, created_at FROM groups")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*models.Group
	for rows.Next() {
		var group models.Group
		var rolesJSON string
		var createdAt string

		if err := rows.Scan(&group.ID, &group.Name, &group.Description, &rolesJSON, &group.Scope, &createdAt); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(rolesJSON), &group.Roles); err != nil {
			return nil, fmt.Errorf("failed to parse roles: %v", err)
		}

		group.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		groups = append(groups, &group)
	}

	return groups, nil
}

func (r *SQLiteRepository) AddUserToGroup(username string, groupName string) error {
	user, err := r.GetUser(username)
	if err != nil {
		return err
	}

	for _, g := range user.Groups {
		if g == groupName {
			return nil // ALREADY IN GROUP
		}
	}

	user.Groups = append(user.Groups, groupName)
	groupsJSON, err := json.Marshal(user.Groups)
	if err != nil {
		return err
	}

	_, err = r.db.Exec(
		"UPDATE users SET groups = ? WHERE username = ?",
		string(groupsJSON), username,
	)
	return err
}

func (r *SQLiteRepository) RemoveUserFromGroup(username string, groupName string) error {
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

	groupsJSON, err := json.Marshal(newGroups)
	if err != nil {
		return err
	}

	_, err = r.db.Exec(
		"UPDATE users SET groups = ? WHERE username = ?",
		string(groupsJSON), username,
	)
	return err
}

func (r *SQLiteRepository) GetUserGroups(username string) ([]string, error) {
	var groupsJSON string
	err := r.db.QueryRow(
		"SELECT groups FROM users WHERE username = ?",
		username,
	).Scan(&groupsJSON)

	if err != nil {
		return nil, err
	}

	var groups []string
	if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
		return nil, err
	}

	return groups, nil
}

// ROLE OPERATIONS
func (r *SQLiteRepository) ListRoles() ([]*models.Role, error) {
	rows, err := r.db.Query("SELECT id, name, description, permissions, created_at FROM roles")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []*models.Role
	for rows.Next() {
		var role models.Role
		var permissionsJSON string
		var createdAt string

		if err := rows.Scan(&role.ID, &role.Name, &role.Description, &permissionsJSON, &createdAt); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(permissionsJSON), &role.Permissions); err != nil {
			log.Printf("Raw permissions JSON: %s", permissionsJSON)
			return nil, fmt.Errorf("failed to parse permissions: %v", err)
		}

		role.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		roles = append(roles, &role)
	}

	return roles, nil
}

func (r *SQLiteRepository) GetRole(name string) (*models.Role, error) {
	var role models.Role
	var permissionsJSON string
	var createdAt string

	err := r.db.QueryRow(
		"SELECT id, name, description, permissions, created_at FROM roles WHERE name = ?",
		name,
	).Scan(&role.ID, &role.Name, &role.Description, &permissionsJSON, &createdAt)

	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(permissionsJSON), &role.Permissions); err != nil {
		log.Printf("Raw permissions JSON: %s", permissionsJSON)
		return nil, fmt.Errorf("failed to parse permissions: %v", err)
	}

	role.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &role, nil
}

func (r *SQLiteRepository) CreateRole(role *models.Role) error {
	permissionsJSON, err := json.Marshal(role.Permissions)
	if err != nil {
		return fmt.Errorf("failed to marshal permissions: %v", err)
	}

	_, err = r.db.Exec(
		`INSERT INTO roles (name, description, permissions, created_at) 
         VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
		role.Name, role.Description, string(permissionsJSON),
	)
	return err
}

func (r *SQLiteRepository) UpdateRole(role *models.Role) error {
	permissionsJSON, err := json.Marshal(role.Permissions)
	if err != nil {
		return fmt.Errorf("failed to marshal permissions: %v", err)
	}

	result, err := r.db.Exec(
		`UPDATE roles SET description = ?, permissions = ? WHERE name = ?`,
		role.Description, string(permissionsJSON), role.Name,
	)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("role not found: %s", role.Name)
	}
	return nil
}

func (r *SQLiteRepository) DeleteRole(name string) error {
	result, err := r.db.Exec("DELETE FROM roles WHERE name = ?", name)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("role not found: %s", name)
	}
	return nil
}

// IMAGE OPERATIONS
func (r *SQLiteRepository) ListImageMetadata(owner string) ([]*models.ImageMetadata, error) {
	log.Printf("Fetching image metadata for owner: %s", owner)

	rows, err := r.db.Query(
		`SELECT id, name, tags, size, owner, labels, CASE private 
            WHEN 1 THEN true 
            ELSE false 
         END as private, created_at, updated_at 
         FROM image_metadata WHERE owner = ?`,
		owner,
	)
	if err != nil {
		return nil, fmt.Errorf("query failed: %v", err)
	}
	defer rows.Close()

	var metadata []*models.ImageMetadata
	for rows.Next() {
		var img models.ImageMetadata
		var tagsJSON, labelsJSON string

		err := rows.Scan(
			&img.ID,
			&img.Name,
			&tagsJSON,
			&img.Size,
			&img.Owner,
			&labelsJSON,
			&img.Private,
			&img.CreatedAt,
			&img.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("row scan failed: %v", err)
		}

		if err := json.Unmarshal([]byte(tagsJSON), &img.Tags); err != nil {
			return nil, fmt.Errorf("failed to parse tags: %v", err)
		}
		if err := json.Unmarshal([]byte(labelsJSON), &img.Labels); err != nil {
			return nil, fmt.Errorf("failed to parse labels: %v", err)
		}

		metadata = append(metadata, &img)
	}

	return metadata, nil
}

func (r *SQLiteRepository) GetImageMetadata(id string) (*models.ImageMetadata, error) {
	var metadata models.ImageMetadata
	var tagsJSON, labelsJSON string
	var createdAt, updatedAt string

	err := r.db.QueryRow(
		`SELECT id, name, tags, size, owner, labels, created_at, updated_at 
         FROM image_metadata WHERE id = ?`,
		id,
	).Scan(&metadata.ID, &metadata.Name, &tagsJSON, &metadata.Size,
		&metadata.Owner, &labelsJSON, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(tagsJSON), &metadata.Tags); err != nil {
		return nil, fmt.Errorf("failed to parse tags: %v", err)
	}

	if err := json.Unmarshal([]byte(labelsJSON), &metadata.Labels); err != nil {
		return nil, fmt.Errorf("failed to parse labels: %v", err)
	}

	metadata.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	metadata.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &metadata, nil
}

func (r *SQLiteRepository) CreateImageMetadata(metadata *models.ImageMetadata) error {
	tagsJSON, err := json.Marshal(metadata.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %v", err)
	}

	labelsJSON, err := json.Marshal(metadata.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %v", err)
	}

	_, err = r.db.Exec(
		`INSERT INTO image_metadata 
         (id, name, tags, size, owner, labels, created_at, updated_at) 
         VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		metadata.ID, metadata.Name, string(tagsJSON), metadata.Size,
		metadata.Owner, string(labelsJSON),
	)
	if err != nil {
		return fmt.Errorf("failed to insert image metadata: %v", err)
	}

	return nil
}

func (r *SQLiteRepository) UpdateImageMetadata(metadata *models.ImageMetadata) error {
	tagsJSON, err := json.Marshal(metadata.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %v", err)
	}

	labelsJSON, err := json.Marshal(metadata.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %v", err)
	}

	result, err := r.db.Exec(
		`UPDATE image_metadata SET 
         name = ?, tags = ?, size = ?, labels = ?, updated_at = CURRENT_TIMESTAMP 
         WHERE id = ? AND owner = ?`,
		metadata.Name, string(tagsJSON), metadata.Size, string(labelsJSON),
		metadata.ID, metadata.Owner,
	)
	if err != nil {
		return fmt.Errorf("failed to update image metadata: %v", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("image not found: %s", metadata.ID)
	}

	return nil
}

func (r *SQLiteRepository) DeleteImageMetadata(id string) error {
	result, err := r.db.Exec("DELETE FROM image_metadata WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete image metadata: %v", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("image metadata not found: %s", id)
	}
	return nil
}

func (r *SQLiteRepository) DeleteImageTag(repository string, tag string, owner string) error {
	var metadata models.ImageMetadata
	var tagsJSON string

	err := r.db.QueryRow(
		`SELECT id, tags FROM image_metadata 
         WHERE name = ? AND owner = ?`,
		repository, owner,
	).Scan(&metadata.ID, &tagsJSON)

	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("repository not found: %s", repository)
		}
		return fmt.Errorf("failed to fetch repository: %v", err)
	}

	// UNMARSHALL CURRENT TAGS
	var currentTags []string
	if err := json.Unmarshal([]byte(tagsJSON), &currentTags); err != nil {
		return fmt.Errorf("failed to parse tags: %v", err)
	}

	// RM SPECIFIC TAG
	newTags := make([]string, 0)
	tagFound := false
	for _, t := range currentTags {
		if t != tag {
			newTags = append(newTags, t)
		} else {
			tagFound = true
		}
	}

	if !tagFound {
		return fmt.Errorf("tag not found: %s", tag)
	}

	// MARSHALL NEW TAGS
	newTagsJSON, err := json.Marshal(newTags)
	if err != nil {
		return fmt.Errorf("failed to marshal new tags: %v", err)
	}

	// UPDATE METADATA
	result, err := r.db.Exec(
		`UPDATE image_metadata 
         SET tags = ?, updated_at = CURRENT_TIMESTAMP 
         WHERE id = ? AND owner = ?`,
		string(newTagsJSON), metadata.ID, owner,
	)
	if err != nil {
		return fmt.Errorf("failed to update tags: %v", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("failed to update repository: %s", repository)
	}

	return nil
}

func (r *SQLiteRepository) ListPublicImageMetadata() ([]*models.ImageMetadata, error) {
	query := `
					SELECT id, name, tags, size, owner, labels, private, created_at, updated_at
					FROM image_metadata 
					WHERE private = FALSE OR private IS NULL
					ORDER BY created_at DESC
	`

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query public images: %v", err)
	}
	defer rows.Close()

	var metadata []*models.ImageMetadata
	for rows.Next() {
		var img models.ImageMetadata
		var tagsJSON, labelsJSON string

		err := rows.Scan(
			&img.ID,
			&img.Name,
			&tagsJSON,
			&img.Size,
			&img.Owner,
			&labelsJSON,
			&img.Private,
			&img.CreatedAt,
			&img.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}

		if err := json.Unmarshal([]byte(tagsJSON), &img.Tags); err != nil {
			return nil, fmt.Errorf("failed to parse tags: %v", err)
		}
		if err := json.Unmarshal([]byte(labelsJSON), &img.Labels); err != nil {
			return nil, fmt.Errorf("failed to parse labels: %v", err)
		}

		metadata = append(metadata, &img)
	}

	return metadata, nil
}

func (r *SQLiteRepository) UpdateImageVisibility(id string, private bool) error {
	result, err := r.db.Exec(
		"UPDATE image_metadata SET private = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		private, id,
	)
	if err != nil {
		return fmt.Errorf("failed to update visibility: %v", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("image not found: %s", id)
	}

	return nil
}

func (r *SQLiteRepository) CreateArtifactRepository(repo *models.ArtifactRepository) error {
	_, err := r.db.Exec(
		`INSERT INTO artifact_repositories (name, description, owner, private) 
		 VALUES (?, ?, ?, ?)`,
		repo.Name, repo.Description, repo.Owner, repo.Private,
	)
	return err
}

func (r *SQLiteRepository) GetArtifactRepository(name string) (*models.ArtifactRepository, error) {
	repo := &models.ArtifactRepository{}
	err := r.db.QueryRow(
		`SELECT id, name, description, owner, private, created_at, updated_at 
		 FROM artifact_repositories WHERE name = ?`,
		name,
	).Scan(&repo.ID, &repo.Name, &repo.Description, &repo.Owner, &repo.Private,
		&repo.CreatedAt, &repo.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

func (r *SQLiteRepository) GetArtifactRepositoryByID(repoID string) (*models.ArtifactRepository, error) {
	repo := &models.ArtifactRepository{}
	err := r.db.QueryRow(
		`SELECT id, name, description, owner, private, created_at, updated_at 
		 FROM artifact_repositories WHERE id = ?`,
		repoID,
	).Scan(&repo.ID, &repo.Name, &repo.Description, &repo.Owner, &repo.Private,
		&repo.CreatedAt, &repo.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

func (r *SQLiteRepository) ListArtifactRepositories(username string) ([]models.ArtifactRepository, error) {
	rows, err := r.db.Query(
		`SELECT id, name, description, owner, private, created_at, updated_at 
		 FROM artifact_repositories 
		 WHERE owner = ? OR (private = false)`,
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []models.ArtifactRepository
	for rows.Next() {
		var repo models.ArtifactRepository
		err := rows.Scan(&repo.ID, &repo.Name, &repo.Description, &repo.Owner,
			&repo.Private, &repo.CreatedAt, &repo.UpdatedAt)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}

	fmt.Printf("FOUND REPOS: %v\n\n", repos)
	return repos, nil
}

func (r *SQLiteRepository) DeleteArtifactRepository(name string) error {
	_, err := r.db.Exec("DELETE FROM artifact_repositories WHERE name = ?", name)
	if err != nil {
		return err
	}

	// DELETE ALL ARTIFACTS IN THIS REPO
	_, err = r.db.Exec(
		`DELETE FROM artifacts WHERE repo_id IN 
		 (SELECT id FROM artifact_repositories WHERE name = ?)`,
		name,
	)
	return err
}

func (r *SQLiteRepository) CreateArtifact(artifact *models.Artifact) error {
	if artifact.ID == "" {
		artifact.ID = uuid.New().String()
	}
	_, err := r.db.Exec(
		`INSERT INTO artifacts (id, repo_id, name, path, upload_id, version, size, mime_type, metadata)
		     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.ID, artifact.RepoID, artifact.Name, artifact.Path, artifact.UploadID,
		artifact.Version, artifact.Size, artifact.MimeType, artifact.Metadata,
	)
	return err
}

func (r *SQLiteRepository) ListArtifacts(repoID int) ([]models.Artifact, error) {
	rows, err := r.db.Query( // GOOGLE TOLD ME THIS WAS OK
		`SELECT COALESCE(id, ''), repo_id, name, path, upload_id, version, size, 
			 COALESCE(mime_type, ''), COALESCE(metadata, '{}'), created_at, updated_at 
			 FROM artifacts WHERE repo_id = ?`,
		repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []models.Artifact
	for rows.Next() {
		var artifact models.Artifact
		err := rows.Scan(
			&artifact.ID,
			&artifact.RepoID,
			&artifact.Name,
			&artifact.Path,
			&artifact.UploadID,
			&artifact.Version,
			&artifact.Size,
			&artifact.MimeType,
			&artifact.Metadata,
			&artifact.CreatedAt,
			&artifact.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan artifact: %v", err)
		}
		// GENERATE NEW UUID IF EMPTY
		if artifact.ID == "" {
			artifact.ID = uuid.New().String()
			_, err = r.db.Exec(
				"UPDATE artifacts SET id = ? WHERE repo_id = ? AND version = ? AND name = ?",
				artifact.ID, artifact.RepoID, artifact.Version, artifact.Name,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to update artifact ID: %v", err)
			}
		}

		// GET PROPS
		properties, err := r.GetArtifactProperties(artifact.ID)
		if err != nil {
			fmt.Printf("Coudn't get props for artifact: %v\n", artifact.ID)
		} else {
			artifact.Properties = properties
		}

		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func (r *SQLiteRepository) UpdateArtifactMetadata(id string, metadata string) error {
	_, err := r.db.Exec(
		"UPDATE artifacts SET metadata = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		metadata, id,
	)
	return err
}

func (r *SQLiteRepository) DeleteArtifact(repoID int, version string, id string) (models.Artifact, error) {
	var artifact models.Artifact
	err := r.db.QueryRow(
		`SELECT id, repo_id, name, path, upload_id, version, size, mime_type, metadata, created_at, updated_at 
         FROM artifacts WHERE repo_id = ? AND version = ? AND id = ?`,
		repoID, version, id,
	).Scan(
		&artifact.ID,
		&artifact.RepoID,
		&artifact.Name,
		&artifact.Path,
		&artifact.UploadID,
		&artifact.Version,
		&artifact.Size,
		&artifact.MimeType,
		&artifact.Metadata,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)

	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}

	// GET PROPS
	properties, err := r.GetArtifactProperties(id)
	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact properties: %v", err)
	}
	artifact.Properties = properties

	_, err = r.db.Exec("DELETE FROM artifacts WHERE id = ?", id)
	return artifact, err
}

func (r *SQLiteRepository) DeleteArtifactByPath(repoID int, version string, path string) (models.Artifact, error) {
	var artifact models.Artifact
	err := r.db.QueryRow(
		`SELECT id, repo_id, name, path, upload_id, version, size, mime_type, metadata, created_at, updated_at 
         FROM artifacts WHERE repo_id = ? AND version = ? AND path = ?`,
		repoID, version, path,
	).Scan(
		&artifact.ID,
		&artifact.RepoID,
		&artifact.Name,
		&artifact.Path,
		&artifact.UploadID,
		&artifact.Version,
		&artifact.Size,
		&artifact.MimeType,
		&artifact.Metadata,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)

	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}

	// GET PROPS
	properties, err := r.GetArtifactProperties(artifact.ID)
	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact properties: %v", err)
	}
	artifact.Properties = properties

	_, err = r.db.Exec("DELETE FROM artifacts WHERE id = ?", artifact.ID)
	return artifact, err
}

func (r *SQLiteRepository) DeleteArtifactByUploadID(repoID int, uploadID string) (models.Artifact, error) {
	var artifact models.Artifact
	err := r.db.QueryRow(
		`SELECT id, repo_id, name, path, upload_id, version, size, mime_type, metadata, created_at, updated_at 
         FROM artifacts WHERE repo_id = ? AND upload_id = ?`,
		repoID, uploadID,
	).Scan(
		&artifact.ID,
		&artifact.RepoID,
		&artifact.Name,
		&artifact.Path,
		&artifact.UploadID,
		&artifact.Version,
		&artifact.Size,
		&artifact.MimeType,
		&artifact.Metadata,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)

	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}

	// GET PROPS
	properties, err := r.GetArtifactProperties(artifact.ID)
	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact properties: %v", err)
	}
	artifact.Properties = properties

	_, err = r.db.Exec("DELETE FROM artifacts WHERE id = ?", artifact.ID)
	return artifact, err
}

func (r *SQLiteRepository) SearchArtifacts(criteria models.ArtifactSearchCriteria) ([]models.Artifact, error) {
	baseQuery := `
			SELECT DISTINCT a.id, a.repo_id, a.name, a.path, a.upload_id, a.version, 
						 a.size, a.mime_type, a.metadata, a.created_at, a.updated_at
			FROM artifacts a
			JOIN artifact_repositories ar ON a.repo_id = ar.id
	`

	// ONLY JOIN PROPERTIES TABLE IF WE HAVE PROPERTY FILTERS
	if len(criteria.Properties) > 0 {
		baseQuery = baseQuery + " JOIN artifact_properties p ON a.id = p.artifact_id"
	}

	// BUILD WHERE CLAUSE AND ARGS
	whereClause := []string{"(ar.owner = ? OR ar.private = FALSE)"} // REPO ACCESS CHECK

	// WE REALLY SHOULDNT BE HITTING THIS, SINCE WE SET ANONYMOUS USER IN HANDLER
	if criteria.Username == "" {
		return nil, fmt.Errorf("no username provided to search")
	}

	args := []interface{}{criteria.Username}

	if criteria.RepoID != nil {
		whereClause = append(whereClause, "a.repo_id = ?")
		args = append(args, *criteria.RepoID)
	}

	if criteria.Name != nil {
		whereClause = append(whereClause, "a.name LIKE ?")
		args = append(args, *criteria.Name)
	}

	if criteria.Version != nil {
		whereClause = append(whereClause, "a.version LIKE ?")
		args = append(args, *criteria.Version)
	}

	if criteria.Path != nil {
		whereClause = append(whereClause, "a.path LIKE ?")
		args = append(args, *criteria.Path)
	}

	// ADD PROPERTY FILTERS
	if len(criteria.Properties) > 0 {
		for key, value := range criteria.Properties {
			whereClause = append(whereClause,
				"EXISTS (SELECT 1 FROM artifact_properties p2 WHERE p2.artifact_id = a.id AND p2.key = ? AND p2.value = ?)")
			args = append(args, key, value)
		}
	}

	// COMBINE QUERY PARTS
	query := baseQuery + " WHERE " + strings.Join(whereClause, " AND ")

	// ADD SORTING
	if criteria.Sort != "" {
		// VALIDATE SORT FIELD TO PREVENT SQL INJECTION
		validSortFields := map[string]bool{
			"name": true, "version": true, "path": true, "size": true,
			"created_at": true, "updated_at": true,
		}

		if !validSortFields[criteria.Sort] {
			return nil, fmt.Errorf("invalid sort field: %s", criteria.Sort)
		}

		order := strings.ToUpper(criteria.Order)
		if order != "ASC" && order != "DESC" {
			order = "DESC"
		}

		query += fmt.Sprintf(" ORDER BY a.%s %s", criteria.Sort, order)
	}

	// ADD LIMIT AND OFFSET
	if criteria.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, criteria.Limit)
	}

	if criteria.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, criteria.Offset)
	}

	// EXECUTE QUERY
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search query: %w", err)
	}
	defer rows.Close()

	var artifacts []models.Artifact
	for rows.Next() {
		var a models.Artifact
		err := rows.Scan(
			&a.ID,
			&a.RepoID,
			&a.Name,
			&a.Path,
			&a.UploadID,
			&a.Version,
			&a.Size,
			&a.MimeType,
			&a.Metadata,
			&a.CreatedAt,
			&a.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan artifact row: %w", err)
		}

		properties, err := r.GetArtifactProperties(a.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get artifact properties: %w", err)
		}
		a.Properties = properties

		artifacts = append(artifacts, a)
	}

	return artifacts, nil
}

func (r *SQLiteRepository) GetArtifact(artifactID string) (models.Artifact, error) {
	var artifact models.Artifact
	err := r.db.QueryRow(
		`SELECT id, repo_id, name, path, upload_id, version, size, mime_type, metadata, created_at, updated_at 
         FROM artifacts WHERE id = ?`,
		artifactID,
	).Scan(
		&artifact.ID,
		&artifact.RepoID,
		&artifact.Name,
		&artifact.Path,
		&artifact.UploadID,
		&artifact.Version,
		&artifact.Size,
		&artifact.MimeType,
		&artifact.Metadata,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)
	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}

	// GET PROPS
	properties, err := r.GetArtifactProperties(artifactID)
	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact properties: %v", err)
	}
	artifact.Properties = properties

	return artifact, nil
}

func (r *SQLiteRepository) GetArtifactByPath(repoID int, path string) (models.Artifact, error) {
	var artifact models.Artifact
	err := r.db.QueryRow(
		`SELECT id, repo_id, name, path, upload_id, version, size, mime_type, metadata, created_at, updated_at 
         FROM artifacts WHERE repo_id = ? AND path = ?`,
		repoID, path,
	).Scan(
		&artifact.ID,
		&artifact.RepoID,
		&artifact.Name,
		&artifact.Path,
		&artifact.UploadID,
		&artifact.Version,
		&artifact.Size,
		&artifact.MimeType,
		&artifact.Metadata,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)
	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact: %v", err)
	}

	// GET PROPS
	properties, err := r.GetArtifactProperties(artifact.ID)
	if err != nil {
		return models.Artifact{}, fmt.Errorf("failed to get artifact properties: %v", err)
	}
	artifact.Properties = properties

	return artifact, nil
}

func (r *SQLiteRepository) UpdateArtifactPath(id string, name string, path string, version string) error {
	_, err := r.db.Exec(
		"UPDATE artifacts SET name = ?, path = ?, version = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		name, path, version, id,
	)
	return err
}

func (r *SQLiteRepository) GetAllSettings() (map[string]json.RawMessage, error) {
	rows, err := r.db.Query("SELECT key, value FROM settings")
	fmt.Printf("ROWS: %v", rows)
	if err != nil {
		fmt.Printf("ERROR: %v", err)
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]json.RawMessage)
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			fmt.Printf("ERROR: %v", err)
			return nil, err
		}
		settings[key] = json.RawMessage(value)
	}

	return settings, nil
}

func (r *SQLiteRepository) GetSettingsSection(section string) (json.RawMessage, error) {
	var value []byte
	err := r.db.QueryRow("SELECT value FROM settings WHERE key = ?", section).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			// RETURN EMPTY JSON IS SETTING DOESNT EXIST
			return json.RawMessage("{}"), nil
		}
		return nil, err
	}
	return json.RawMessage(value), nil
}

func (r *SQLiteRepository) UpdateSettingsSection(section string, settings json.RawMessage) error {
	var js json.RawMessage
	if err := json.Unmarshal(settings, &js); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}

	_, err := r.db.Exec(`
			INSERT INTO settings (key, value) 
			VALUES (?, ?) 
			ON CONFLICT(key) DO UPDATE SET 
					value = excluded.value,
					updated_at = CURRENT_TIMESTAMP
	`, section, settings)

	return err
}

func (r *SQLiteRepository) ResetSettingsSection(section string) error {
	// GET MODEL DEFAULTS
	settings, err := models.GetSettingsWithDefaults(section)
	if err != nil {
		return fmt.Errorf("unknown settings section: %s", section)
	}

	// TO JSON
	defaultValue, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal default settings: %v", err)
	}

	// UPDATE OR INSERT
	_, err = r.db.Exec(`
        INSERT INTO settings (key, value) 
        VALUES (?, ?) 
        ON CONFLICT(key) DO UPDATE SET 
            value = excluded.value,
            updated_at = CURRENT_TIMESTAMP
    `, section, defaultValue)

	return err
}

func (r *SQLiteRepository) SetArtifactProperties(artifactID string, properties map[string]string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// DELETE EXISTING PROPERTIES
	_, err = tx.Exec("DELETE FROM artifact_properties WHERE artifact_id = ?", artifactID)
	if err != nil {
		return err
	}

	// INSERT NEW PROPERTIES
	stmt, err := tx.Prepare("INSERT INTO artifact_properties (artifact_id, key, value) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for key, value := range properties {
		_, err = stmt.Exec(artifactID, key, value)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *SQLiteRepository) GetArtifactProperties(artifactID string) (map[string]string, error) {
	rows, err := r.db.Query("SELECT key, value FROM artifact_properties WHERE artifact_id = ?", artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	properties := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		properties[key] = value
	}

	return properties, nil
}
