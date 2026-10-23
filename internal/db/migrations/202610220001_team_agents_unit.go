package migrations

import (
	"encoding/json"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610220001",
		name:    "team_agents_unit",
		migrate: addTeamAgentsUnit,
	})
}

// Existing teams inherit their actions level on the new agents unit
func addTeamAgentsUnit(tx *gorm.DB, log *logger.Logger) error {
	var teams []db.OrgTeam
	if err := tx.Find(&teams).Error; err != nil {
		return err
	}
	changed := 0
	for i := range teams {
		t := &teams[i]
		units := map[string]string{}
		if t.Units != "" {
			_ = json.Unmarshal([]byte(t.Units), &units)
		}
		if _, ok := units[db.GitUnitAgents]; ok {
			continue
		}
		level := units[db.GitUnitActions]
		if level == "" {
			level = db.GitLevelNone
		}
		units[db.GitUnitAgents] = level
		raw, err := json.Marshal(units)
		if err != nil {
			return err
		}
		if err := tx.Model(&db.OrgTeam{}).Where("id = ?", t.ID).Update("units", string(raw)).Error; err != nil {
			return err
		}
		changed++
	}
	if changed > 0 {
		log.Info("added the agents unit to %d teams", changed)
	}
	return nil
}
