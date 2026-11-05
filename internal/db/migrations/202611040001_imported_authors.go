package migrations

import (
	"encoding/json"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202611040001",
		name:    "imported_authors",
		migrate: backfillImportedAuthors,
	})
}

// Issues imported before the column existed keep their author only in the import event
func backfillImportedAuthors(tx *gorm.DB, log *logger.Logger) error {
	var issues []db.GitIssue
	filled := 0
	err := tx.Select("id").Where("author_id = ? AND imported_author = ?", "", "").FindInBatches(&issues, 500, func(_ *gorm.DB, _ int) error {
		for _, is := range issues {
			var ev db.GitComment
			if err := tx.Select("payload").Where("issue_id = ? AND type = ?", is.ID, "imported").Order("created_at").First(&ev).Error; err != nil {
				continue
			}
			var p struct {
				Author string `json:"author"`
			}
			if json.Unmarshal([]byte(ev.Payload), &p) != nil || p.Author == "" {
				continue
			}
			if err := tx.Model(&db.GitIssue{}).Where("id = ?", is.ID).Update("imported_author", p.Author).Error; err != nil {
				return err
			}
			filled++
		}
		return nil
	}).Error
	if err != nil {
		return err
	}
	log.Info("recorded upstream authors on %d imported issues", filled)
	return nil
}
