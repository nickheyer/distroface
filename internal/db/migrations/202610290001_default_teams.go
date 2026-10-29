package migrations

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610290001",
		name:    "default_teams",
		migrate: createDefaultTeams,
	})
}

// Existing orgs get the implicit Members team, write everywhere
func createDefaultTeams(tx *gorm.DB, log *logger.Logger) error {
	var orgs []db.Organization
	if err := tx.Find(&orgs).Error; err != nil {
		return err
	}
	units := map[string]string{}
	for _, u := range []string{db.GitUnitCode, db.GitUnitIssues, db.GitUnitPulls, db.GitUnitReleases, db.GitUnitWiki, db.GitUnitProjects, db.GitUnitPackages, db.GitUnitActions, db.GitUnitAgents} {
		units[u] = db.GitLevelWrite
	}
	raw, _ := json.Marshal(units)
	created := 0
	for _, org := range orgs {
		var n int64
		if err := tx.Model(&db.OrgTeam{}).Where("org_id = ? AND is_default = ?", org.ID, true).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		team := db.OrgTeam{
			ID:               uuid.New().String(),
			OrgID:            org.ID,
			Name:             "Members",
			Description:      "Every organization member",
			IsDefault:        true,
			CanCreateRepos:   true,
			IncludesAllRepos: true,
			Units:            string(raw),
		}
		if err := tx.Create(&team).Error; err != nil {
			return err
		}
		created++
	}
	if created > 0 {
		log.Info("created default teams for %d organizations", created)
	}
	return nil
}
