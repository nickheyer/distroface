package migrations

import (
	"fmt"
	"strings"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610280002",
		name:    "actions_artifacts_runs",
		migrate: moveActionsArtifactsToRuns,
	})
}

const (
	// Characters the toolkit refuses in artifact names
	artifactNameBadChars = "\\/\":<>|*?\r\n"
	// Unique per run and name, built after legacy folding
	actionsArtifactRunNameIndex = "idx_actions_artifact_run_name"
)

// Run artifacts leave package files and carry blobs
func moveActionsArtifactsToRuns(tx *gorm.DB, log *logger.Logger) error {
	if !tx.Migrator().HasTable(&db.ActionsArtifact{}) {
		return nil
	}
	present, err := actualColumns(tx, &db.ActionsArtifact{})
	if err != nil {
		return err
	}
	if present["artifact_id"] {
		if err := moveLegacyArtifactRows(tx, log, present); err != nil {
			return err
		}
	}
	return ensureActionsArtifactIndexes(tx)
}

func moveLegacyArtifactRows(tx *gorm.DB, log *logger.Logger, present map[string]bool) error {
	type oldRow struct {
		ID         string
		RunID      string
		ArtifactID string
		Name       string
		FileCount  int32
	}
	var rows []oldRow
	cols := "id, run_id, artifact_id, name"
	if present["file_count"] {
		cols += ", file_count"
	}
	if err := tx.Raw("SELECT " + cols + " FROM actions_artifacts WHERE digest = '' ORDER BY created_at ASC, id ASC").Scan(&rows).Error; err != nil {
		return err
	}
	counters := map[string]int64{}
	packages := map[string]*db.Artifact{}
	taken := map[string]bool{}
	moved, dropped, renamed := 0, 0, 0
	for _, row := range rows {
		art, ok := packages[row.ArtifactID]
		if !ok {
			art = &db.Artifact{}
			if err := tx.First(art, "id = ?", row.ArtifactID).Error; err != nil {
				if err != gorm.ErrRecordNotFound {
					return err
				}
				art = nil
			}
			packages[row.ArtifactID] = art
		}
		var run db.ActionsRun
		runErr := tx.First(&run, "id = ?", row.RunID).Error
		if runErr != nil && runErr != gorm.ErrRecordNotFound {
			return runErr
		}
		if art == nil || runErr != nil {
			if err := tx.Delete(&db.ActionsArtifact{}, "id = ?", row.ID).Error; err != nil {
				return err
			}
			dropped++
			continue
		}
		if _, ok := counters[run.RepoID]; !ok {
			var n int64
			if err := tx.Raw("SELECT COALESCE(MAX(a.number), 0) FROM actions_artifacts a JOIN actions_runs r ON r.id = a.run_id WHERE r.repo_id = ?", run.RepoID).Scan(&n).Error; err != nil {
				return err
			}
			counters[run.RepoID] = n
		}
		counters[run.RepoID]++
		// Legacy per file rows become standalone files, zips keep their type
		contentType := "application/zip"
		if row.FileCount == 0 && art.MimeType != "" {
			contentType = art.MimeType
		}
		update := map[string]any{"digest": art.Digest, "size": art.Size, "content_type": contentType, "status": "confirmed", "number": counters[run.RepoID]}
		name := uniqueArtifactName(taken, row.RunID, row.Name, art.Path)
		if name != row.Name {
			update["name"] = name
			renamed++
			log.Warn("run artifact %s of run %s shares its name with an earlier upload, now %q", row.ID, row.RunID, name)
		}
		if err := tx.Model(&db.ActionsArtifact{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
			return err
		}
		moved++
	}
	for repoID, n := range counters {
		if err := tx.Exec("UPDATE git_repositories SET artifact_counter = ? WHERE id = ? AND artifact_counter < ?", n, repoID, n).Error; err != nil {
			return err
		}
	}
	// Package rows go, run rows own the blob now
	for id, art := range packages {
		if art == nil {
			continue
		}
		if err := tx.Where("artifact_id = ?", id).Delete(&db.ArtifactProperty{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&db.Artifact{}, "id = ?", id).Error; err != nil {
			return err
		}
	}
	for _, col := range []string{"artifact_id", "file_count"} {
		if present[col] {
			if err := tx.Migrator().DropColumn(&db.ActionsArtifact{}, col); err != nil {
				return err
			}
		}
	}
	log.Info("moved %d run artifacts onto their runs, dropped %d orphans, renamed %d legacy files, removed %d package rows", moved, dropped, renamed, len(packages))
	return nil
}

// Legacy per file rows get a file name suffix
func uniqueArtifactName(taken map[string]bool, runID, name, filePath string) string {
	key := func(n string) string { return runID + "\x00" + n }
	if !taken[key(name)] {
		taken[key(name)] = true
		return name
	}
	base := name
	if file := strings.TrimSpace(filePath); file != "" {
		clean := strings.Map(func(r rune) rune {
			if strings.ContainsRune(artifactNameBadChars, r) {
				return '-'
			}
			return r
		}, file)
		base = name + " " + strings.Trim(clean, "- ")
	}
	candidate := base
	for i := 2; taken[key(candidate)]; i++ {
		candidate = fmt.Sprintf("%s (%d)", base, i)
	}
	taken[key(candidate)] = true
	return candidate
}

// Column drops rebuild the table, indexes come back here
func ensureActionsArtifactIndexes(tx *gorm.DB) error {
	for _, field := range []string{"RunID", "Number", "Digest", "Status", "ExpiresAt"} {
		if tx.Migrator().HasIndex(&db.ActionsArtifact{}, field) {
			continue
		}
		if err := tx.Migrator().CreateIndex(&db.ActionsArtifact{}, field); err != nil {
			return err
		}
	}
	if tx.Migrator().HasIndex(&db.ActionsArtifact{}, actionsArtifactRunNameIndex) {
		return nil
	}
	return tx.Exec("CREATE UNIQUE INDEX " + actionsArtifactRunNameIndex + " ON actions_artifacts (run_id, name)").Error
}

// Columns the table really has, the migrator's name match is too loose
func actualColumns(tx *gorm.DB, model any) (map[string]bool, error) {
	types, err := tx.Migrator().ColumnTypes(model)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, t := range types {
		out[t.Name()] = true
	}
	return out, nil
}
