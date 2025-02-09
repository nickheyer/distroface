package db

import (
	"database/sql"
	"fmt"

	"github.com/nickheyer/distroface/internal/models"
	"golang.org/x/crypto/bcrypt"
)

func RunInit(db *sql.DB, cfg *models.Config) error {
	var err error

	// IF ROLES ENABLED
	if cfg.Init.Roles {
		if _, err = db.Exec(rolesSQL); err != nil {
			return fmt.Errorf("failed to init roles: %w", err)
		}
	}

	// IF GROUPS ENABLED
	if cfg.Init.Groups {
		if _, err = db.Exec(groupsSQL); err != nil {
			return fmt.Errorf("failed to init groups: %w", err)
		}
	}

	// IF USER ENABLED
	if cfg.Init.User {
		userSQL, err := genUserSQL(cfg)
		if err != nil {
			return fmt.Errorf("failed generating user insert SQL: %w", err)
		}

		if _, err := db.Exec(userSQL); err != nil {
			return fmt.Errorf("failed to init user: %w", err)
		}
	}
	return nil
}

const rolesSQL = `
INSERT INTO roles (name, description, permissions) VALUES 
('anonymous', 'Unauthenticated access', 
 '[
    {"action":"VIEW","resource":"WEBUI"},
    {"action":"LOGIN","resource":"WEBUI"},
    {"action":"PULL","resource":"IMAGE"},
    {"action":"VIEW","resource":"TAG"},
    {"action":"VIEW","resource":"ARTIFACT"},
    {"action":"DOWNLOAD","resource":"ARTIFACT"},
    {"action":"VIEW","resource":"REPO"}
 ]'),

('reader', 'Basic read access', 
 '[
    {"action":"VIEW","resource":"WEBUI"},
    {"action":"LOGIN","resource":"WEBUI"},
    {"action":"PULL","resource":"IMAGE"},
    {"action":"VIEW","resource":"TAG"},
    {"action":"VIEW","resource":"USER"},
    {"action":"VIEW","resource":"GROUP"},
    {"action":"VIEW","resource":"ARTIFACT"},
    {"action":"DOWNLOAD","resource":"ARTIFACT"},
    {"action":"VIEW","resource":"REPO"}
 ]'),

('developer', 'Standard developer access', 
 '[
    {"action":"MIGRATE","resource":"TASK"},
    {"action":"VIEW","resource":"WEBUI"},
    {"action":"LOGIN","resource":"WEBUI"},
    {"action":"UPDATE","resource":"WEBUI"},
    {"action":"PULL","resource":"IMAGE"},
    {"action":"PUSH","resource":"IMAGE"},
    {"action":"VIEW","resource":"IMAGE"},
    {"action":"VIEW","resource":"TAG"},
    {"action":"CREATE","resource":"TAG"},
    {"action":"DELETE","resource":"TAG"},
    {"action":"VIEW","resource":"USER"},
    {"action":"VIEW","resource":"GROUP"},
    {"action":"VIEW","resource":"ARTIFACT"},
    {"action":"UPLOAD","resource":"ARTIFACT"},
    {"action":"UPDATE","resource":"ARTIFACT"},
    {"action":"DOWNLOAD","resource":"ARTIFACT"},
    {"action":"DELETE","resource":"ARTIFACT"},
    {"action":"VIEW","resource":"REPO"},
    {"action":"CREATE","resource":"REPO"},
    {"action":"DELETE","resource":"REPO"}
 ]'),

('administrator', 'Full system access', 
 '[
    {"action":"ADMIN","resource":"SYSTEM"},
    {"action":"VIEW","resource":"WEBUI"},
    {"action":"LOGIN","resource":"WEBUI"},
    {"action":"VIEW","resource":"USER"},
    {"action":"CREATE","resource":"USER"},
    {"action":"UPDATE","resource":"USER"},
    {"action":"DELETE","resource":"USER"},
    {"action":"VIEW","resource":"GROUP"},
    {"action":"CREATE","resource":"GROUP"},
    {"action":"UPDATE","resource":"GROUP"},
    {"action":"DELETE","resource":"GROUP"},
    {"action":"UPDATE","resource":"WEBUI"},
    {"action":"PULL","resource":"IMAGE"},
    {"action":"PUSH","resource":"IMAGE"},
    {"action":"MIGRATE","resource":"TASK"},
    {"action":"DELETE","resource":"IMAGE"},
    {"action":"VIEW","resource":"TAG"},
    {"action":"CREATE","resource":"TAG"},
    {"action":"DELETE","resource":"TAG"},
    {"action":"VIEW","resource":"ARTIFACT"},
    {"action":"UPLOAD","resource":"ARTIFACT"},
    {"action":"UPDATE","resource":"ARTIFACT"},
    {"action":"DOWNLOAD","resource":"ARTIFACT"},
    {"action":"DELETE","resource":"ARTIFACT"},
    {"action":"VIEW","resource":"REPO"},
    {"action":"CREATE","resource":"REPO"},
    {"action":"DELETE","resource":"REPO"}
 ]');
`

const groupsSQL = `
INSERT INTO groups (name, description, roles, scope) VALUES 
('admins', 'System Administrators', '["administrator"]', 'system:all'),
('developers', 'Development Team', '["developer"]', 'system:all'),
('readers', 'Read-only Users', '["reader"]', 'system:all');
`

func genUserSQL(cfg *models.Config) (string, error) {
	// GENERATE BCRYPT HASH FOR PASS
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.Init.Password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password for user %q: %w", cfg.Init.Username, err)
	}

	// RETURN GENERATED SQL, PUT USER IN ADMINS FOR EZ
	return fmt.Sprintf(`
INSERT INTO users (username, password, groups) 
VALUES ('%s', '%s', '["admins"]');
`, cfg.Init.Username, hash), nil
}
