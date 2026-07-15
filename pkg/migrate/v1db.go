package migrate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// V1 domain types, matching the schema on the v1 main branch (internal/db/schema.go).

type V1User struct {
	ID           int
	Username     string
	PasswordHash string // bcrypt, ports directly into v2 users.password_hash
	Groups       []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type V1Group struct {
	Name  string
	Roles []string
}

// V1Image is one image_metadata row: one manifest digest with its tag set.
type V1Image struct {
	Digest    string // id column: manifest digest "sha256:..."
	Name      string // repo name, flat ("foo") or two-level ("foo/bar")
	Tags      []string
	Size      int64
	Owner     string
	Private   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type V1ArtifactRepo struct {
	ID      int64
	Name    string
	Desc    string
	Owner   string
	Private bool
}

type V1Artifact struct {
	ID       string
	RepoID   int64
	RepoName string
	Name     string
	Path     string
	UploadID string
	Version  string
	Size     int64
	MimeType string
	Metadata string
}

type V1DB struct {
	db *sql.DB
}

func OpenV1DB(path string) (*V1DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("v1 db not found: %w", err)
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open v1 db: %w", err)
	}
	return &V1DB{db: db}, nil
}

func (v *V1DB) Close() error { return v.db.Close() }

func (v *V1DB) Users() ([]V1User, error) {
	rows, err := v.db.Query(`SELECT id, username, password, groups, created_at, updated_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []V1User
	for rows.Next() {
		var u V1User
		var pw, groups []byte
		var created, updated string
		if err := rows.Scan(&u.ID, &u.Username, &pw, &groups, &created, &updated); err != nil {
			return nil, err
		}
		u.PasswordHash = string(pw)
		if err := json.Unmarshal(groups, &u.Groups); err != nil {
			return nil, fmt.Errorf("user %s: bad groups json %q: %w", u.Username, groups, err)
		}
		u.CreatedAt = parseV1Time(created)
		u.UpdatedAt = parseV1Time(updated)
		users = append(users, u)
	}
	return users, rows.Err()
}

func (v *V1DB) Groups() (map[string][]string, error) {
	rows, err := v.db.Query(`SELECT name, roles FROM groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make(map[string][]string)
	for rows.Next() {
		var name string
		var roles []byte
		if err := rows.Scan(&name, &roles); err != nil {
			return nil, err
		}
		var parsed []string
		if err := json.Unmarshal(roles, &parsed); err != nil {
			return nil, fmt.Errorf("group %s: bad roles json: %w", name, err)
		}
		groups[name] = parsed
	}
	return groups, rows.Err()
}

func (v *V1DB) Images() ([]V1Image, error) {
	rows, err := v.db.Query(`SELECT id, name, tags, size, owner, private, created_at, updated_at FROM image_metadata ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []V1Image
	for rows.Next() {
		var img V1Image
		var tags []byte
		var created, updated string
		if err := rows.Scan(&img.Digest, &img.Name, &tags, &img.Size, &img.Owner, &img.Private, &created, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tags, &img.Tags); err != nil {
			return nil, fmt.Errorf("image %s: bad tags json: %w", img.Name, err)
		}
		img.CreatedAt = parseV1Time(created)
		img.UpdatedAt = parseV1Time(updated)
		images = append(images, img)
	}
	return images, rows.Err()
}

func (v *V1DB) ArtifactRepos() ([]V1ArtifactRepo, error) {
	rows, err := v.db.Query(`SELECT id, name, COALESCE(description,''), owner, private FROM artifact_repositories ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []V1ArtifactRepo
	for rows.Next() {
		var r V1ArtifactRepo
		if err := rows.Scan(&r.ID, &r.Name, &r.Desc, &r.Owner, &r.Private); err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

// LiveArtifacts returns artifacts whose repo still exists. V1 leaked rows when
// repos were deleted (no FK cascade): those orphans are intentionally skipped.
func (v *V1DB) LiveArtifacts() ([]V1Artifact, error) {
	rows, err := v.db.Query(`
		SELECT a.id, a.repo_id, r.name, a.name, a.path, a.upload_id, a.version, a.size, COALESCE(a.mime_type,''), COALESCE(a.metadata,'')
		FROM artifacts a
		JOIN artifact_repositories r ON r.id = a.repo_id
		ORDER BY r.name, a.version, a.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var arts []V1Artifact
	for rows.Next() {
		var a V1Artifact
		if err := rows.Scan(&a.ID, &a.RepoID, &a.RepoName, &a.Name, &a.Path, &a.UploadID, &a.Version, &a.Size, &a.MimeType, &a.Metadata); err != nil {
			return nil, err
		}
		arts = append(arts, a)
	}
	return arts, rows.Err()
}

// LiveProperties returns properties of live artifacts only, keyed by artifact ID.
func (v *V1DB) LiveProperties() (map[string]map[string]string, error) {
	rows, err := v.db.Query(`
		SELECT p.artifact_id, p.key, p.value
		FROM artifact_properties p
		WHERE EXISTS (SELECT 1 FROM artifacts a JOIN artifact_repositories r ON r.id = a.repo_id WHERE a.id = p.artifact_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	props := make(map[string]map[string]string)
	for rows.Next() {
		var id, key, val string
		if err := rows.Scan(&id, &key, &val); err != nil {
			return nil, err
		}
		if props[id] == nil {
			props[id] = make(map[string]string)
		}
		props[id][key] = val
	}
	return props, rows.Err()
}

type V1OrphanStats struct {
	OrphanedArtifacts  int64
	OrphanedProperties int64
}

func (v *V1DB) OrphanStats() (V1OrphanStats, error) {
	var s V1OrphanStats
	err := v.db.QueryRow(`SELECT COUNT(*) FROM artifacts a WHERE NOT EXISTS (SELECT 1 FROM artifact_repositories r WHERE r.id = a.repo_id)`).Scan(&s.OrphanedArtifacts)
	if err != nil {
		return s, err
	}
	err = v.db.QueryRow(`SELECT COUNT(*) FROM artifact_properties p WHERE NOT EXISTS (SELECT 1 FROM artifacts a JOIN artifact_repositories r ON r.id = a.repo_id WHERE a.id = p.artifact_id)`).Scan(&s.OrphanedProperties)
	return s, err
}

var v1TimeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
}

// parseV1Time parses sqlite DATETIME text (CURRENT_TIMESTAMP is UTC).
func parseV1Time(s string) time.Time {
	for _, layout := range v1TimeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}
