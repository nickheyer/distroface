package migrations

import (
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610280001",
		name:    "user_repo_actions_units",
		migrate: enableUserRepoUnits,
	})
}

// Repositories under user namespaces follow the instance for actions and agents
func enableUserRepoUnits(tx *gorm.DB, log *logger.Logger) error {
	var row db.SettingsRow
	err := tx.Where("scope_type = ? AND scope_id = ''", int32(v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM)).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	var sys v1.Settings
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(row.Value), &sys); err != nil {
		return err
	}
	actions := sys.GetGit().GetActions().GetEnabled()
	agents := sys.GetGit().GetAgents().GetEnabled()
	if !actions && !agents {
		return nil
	}
	var repos []db.GitRepository
	if err := tx.Where("is_org_namespace = ?", false).Find(&repos).Error; err != nil {
		return err
	}
	flipped := 0
	for i := range repos {
		units := &v1.GitRepoUnits{Issues: true, Pulls: true, Releases: true, Wiki: true, Projects: true, Packages: true}
		if repos[i].Units != "" && repos[i].Units != "{}" {
			_ = protojson.Unmarshal([]byte(repos[i].Units), units)
		}
		changed := false
		if actions && !units.Actions {
			units.Actions, changed = true, true
		}
		if agents && !units.Agents {
			units.Agents, changed = true, true
		}
		if !changed {
			continue
		}
		b, err := protojson.Marshal(units)
		if err != nil {
			return err
		}
		if err := tx.Model(&db.GitRepository{}).Where("id = ?", repos[i].ID).UpdateColumn("units", string(b)).Error; err != nil {
			return err
		}
		flipped++
	}
	if flipped > 0 {
		log.Info("enabled actions or agents on %d user repositories", flipped)
	}
	return nil
}
