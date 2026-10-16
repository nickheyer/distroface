package migrations

import (
	"strings"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610140001",
		name:    "git_repo_links",
		migrate: linkPackagesToGitRepos,
	})
}

// Image and artifact repos named like an existing git repo become its packages
func linkPackagesToGitRepos(tx *gorm.DB, log *logger.Logger) error {
	var gitRepos []db.GitRepository
	if err := tx.Find(&gitRepos).Error; err != nil {
		return err
	}
	linked := 0
	for i := range gitRepos {
		g := &gitRepos[i]
		res := tx.Model(&db.Repository{}).
			Where("namespace = ? AND git_repo_id IS NULL AND (name = ? OR name LIKE ? ESCAPE '\\')", g.Namespace, g.Name, strings.ReplaceAll(strings.ReplaceAll(g.Name, "%", "\\%"), "_", "\\_")+"/%").
			Update("git_repo_id", g.ID)
		if res.Error != nil {
			return res.Error
		}
		linked += int(res.RowsAffected)
		res = tx.Model(&db.ArtifactRepository{}).
			Where("namespace = ? AND name = ? AND git_repo_id IS NULL", g.Namespace, g.Name).
			Update("git_repo_id", g.ID)
		if res.Error != nil {
			return res.Error
		}
		linked += int(res.RowsAffected)
	}
	if linked > 0 {
		log.Info("linked %d existing packages to their source repositories", linked)
	}
	return nil
}
