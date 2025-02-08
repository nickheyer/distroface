-- Clear existing tables
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS groups;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS image_metadata;
DROP TABLE IF EXISTS artifact_repositories;
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS artifact_properties;
DROP TABLE IF EXISTS settings;

-- Settings table
CREATE TABLE IF NOT EXISTS settings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT UNIQUE NOT NULL,
    value TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Roles table
CREATE TABLE IF NOT EXISTS roles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    description TEXT NOT NULL,
    permissions TEXT NOT NULL, -- JSON array of permissions
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Groups table
CREATE TABLE IF NOT EXISTS groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    description TEXT NOT NULL,
    roles TEXT NOT NULL, -- JSON array of role names
    scope TEXT NOT NULL,
    target TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password TEXT NOT NULL,
    groups TEXT NOT NULL, -- JSON array of group names
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Image metadata table
CREATE TABLE IF NOT EXISTS image_metadata (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    tags TEXT NOT NULL, -- JSON array of tags
    size INTEGER NOT NULL,
    owner TEXT NOT NULL,
    labels TEXT, -- JSON object of labels
    private BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(owner) REFERENCES users(username)
);

-- Artifact repositories table
CREATE TABLE IF NOT EXISTS artifact_repositories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    description TEXT,
    owner TEXT NOT NULL,
    private BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(owner) REFERENCES users(username)
);

-- Artifact metadata table
CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    repo_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    size INTEGER NOT NULL,
    mime_type TEXT,
    metadata TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(repo_id) REFERENCES artifact_repositories(id)
);

-- Artifact properties table
CREATE TABLE IF NOT EXISTS artifact_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    artifact_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(artifact_id) REFERENCES artifacts(id) ON DELETE CASCADE
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_roles_name ON roles(name);
CREATE INDEX IF NOT EXISTS idx_groups_name ON groups(name);
CREATE INDEX IF NOT EXISTS idx_groups_scope ON groups(scope);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_image_metadata_owner ON image_metadata(owner);
CREATE INDEX IF NOT EXISTS idx_image_metadata_name ON image_metadata(name);
CREATE INDEX IF NOT EXISTS idx_artifact_properties_key ON artifact_properties(key);
CREATE INDEX IF NOT EXISTS idx_artifact_properties_value ON artifact_properties(value);
CREATE INDEX IF NOT EXISTS idx_artifact_properties_artifact ON artifact_properties(artifact_id);
CREATE INDEX IF NOT EXISTS idx_artifact_properties_lookup ON artifact_properties(artifact_id, key, value);
CREATE INDEX IF NOT EXISTS idx_settings_key ON settings(key);

-- Update triggers
CREATE TRIGGER update_roles_timestamp 
AFTER UPDATE ON roles
BEGIN
    UPDATE roles SET updated_at = CURRENT_TIMESTAMP 
    WHERE id = NEW.id;
END;

CREATE TRIGGER update_groups_timestamp 
AFTER UPDATE ON groups
BEGIN
    UPDATE groups SET updated_at = CURRENT_TIMESTAMP 
    WHERE id = NEW.id;
END;

CREATE TRIGGER update_users_timestamp 
AFTER UPDATE ON users
BEGIN
    UPDATE users SET updated_at = CURRENT_TIMESTAMP 
    WHERE id = NEW.id;
END;

CREATE TRIGGER update_image_metadata_timestamp 
AFTER UPDATE ON image_metadata
BEGIN
    UPDATE image_metadata SET updated_at = CURRENT_TIMESTAMP 
    WHERE id = NEW.id;
END;

CREATE TRIGGER update_settings_timestamp 
AFTER UPDATE ON settings
BEGIN
    UPDATE settings SET updated_at = CURRENT_TIMESTAMP 
    WHERE id = NEW.id;
END;


