package migrations

import (
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610290002",
		name:    "split_pull_numbers",
		migrate: splitPullNumbers,
	})
}

// Pull requests continue from the shared high water mark
func splitPullNumbers(tx *gorm.DB, log *logger.Logger) error {
	res := tx.Model(&db.GitRepository{}).Where("pull_counter < issue_counter").Update("pull_counter", gorm.Expr("issue_counter"))
	if res.Error != nil {
		return res.Error
	}
	if tx.Migrator().HasIndex(&db.GitIssue{}, "idx_issue_repo_number") {
		if err := tx.Migrator().DropIndex(&db.GitIssue{}, "idx_issue_repo_number"); err != nil {
			return err
		}
	}
	if res.RowsAffected > 0 {
		log.Info("seeded pull counters for %d repositories", res.RowsAffected)
	}
	return nil
}
