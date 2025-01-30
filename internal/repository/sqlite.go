package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

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
