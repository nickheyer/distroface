package migrations

import (
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610210001",
		name:    "workflow_artifact_kind",
		migrate: markWorkflowArtifacts,
	})
}

// Run uploads predating the kind column carry the actions.run property
func markWorkflowArtifacts(tx *gorm.DB, log *logger.Logger) error {
	// Fresh databases never had the column, later migrations drop it
	if cols, err := actualColumns(tx, &db.Artifact{}); err != nil || !cols["kind"] {
		return err
	}
	res := tx.Exec(`UPDATE artifacts SET kind = ? WHERE kind = '' AND id IN (SELECT artifact_id FROM artifact_properties WHERE key = 'actions.run')`, "workflow")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Info("marked %d workflow artifacts", res.RowsAffected)
	}
	// Rows born after run artifacts moved onto their runs have no package link
	present, err := actualColumns(tx, &db.ActionsArtifact{})
	if err != nil || !present["artifact_id"] {
		return err
	}
	return tx.Exec(`UPDATE actions_artifacts SET job_id = COALESCE((SELECT p.value FROM artifact_properties p WHERE p.artifact_id = actions_artifacts.artifact_id AND p.key = 'actions.job'), '') WHERE job_id = ''`).Error
}
