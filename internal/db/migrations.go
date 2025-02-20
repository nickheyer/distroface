package db

import (
	"database/sql"
	"fmt"

	"github.com/nickheyer/distroface/internal/models"
)

func RunMigrations(db *sql.DB, cfg *models.Config) error {
	if !cfg.Init.Migrations {
		return nil
	}

	// ENSURE MIGRATION TABLE
	if err := ensureMigrationTable(db); err != nil {
		return fmt.Errorf("failed to create migration table: %w", err)
	}

	// RUN IN ORDER
	for _, m := range migrations {
		if err := runMigration(db, m); err != nil {
			return fmt.Errorf("failed to run migration %s: %w", m.id, err)
		}
	}

	return nil
}

type migration struct {
	id   string
	stmt string
}

func ensureMigrationTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			id TEXT PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func runMigration(db *sql.DB, m migration) error {
	// CHECK IF MIGRATION APPLIED
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE id = ?)", m.id).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	// RUN MIGRATION IN TX
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(m.stmt); err != nil {
		return err
	}

	if _, err := tx.Exec("INSERT INTO schema_migrations (id) VALUES (?)", m.id); err != nil {
		return err
	}

	return tx.Commit()
}

var migrations = []migration{
	{
		id: "001_add_upload_id",
		stmt: `
			ALTER TABLE artifacts ADD COLUMN upload_id TEXT;
			UPDATE artifacts SET upload_id = hex(randomblob(16)) WHERE upload_id IS NULL;
			CREATE TABLE artifacts_new (
				id TEXT PRIMARY KEY,
				repo_id INTEGER NOT NULL,
				name TEXT NOT NULL,
				path TEXT NOT NULL,
				upload_id TEXT NOT NULL,
				version TEXT NOT NULL,
				size INTEGER NOT NULL,
				mime_type TEXT,
				metadata TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY(repo_id) REFERENCES artifact_repositories(id)
			);
			INSERT INTO artifacts_new SELECT * FROM artifacts;
			DROP TABLE artifacts;
			ALTER TABLE artifacts_new RENAME TO artifacts;
		`,
	},
}
