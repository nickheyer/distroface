package migrations

import (
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	register(migration{
		id:      "202609300001",
		name:    "portal_anonymous_policy",
		migrate: portalRequireAuthToSettings,
	})
}

// Legacy require_auth portals become a portal tier deny
func portalRequireAuthToSettings(tx *gorm.DB, log *logger.Logger) error {
	var portals []db.RegistryPortal
	if err := tx.Find(&portals, "require_auth = ?", true).Error; err != nil {
		return err
	}
	for i := range portals {
		p := &portals[i]
		row := db.SettingsRow{ScopeType: int32(v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL), ScopeID: p.ID}
		doc := &v1.Settings{}
		found := tx.First(&row, "scope_type = ? AND scope_id = ?", row.ScopeType, row.ScopeID).Error == nil
		if found {
			if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(row.Value), doc); err != nil {
				return err
			}
		}
		if doc.GetAuth() == nil || doc.GetAuth().AnonymousAccess == nil {
			if doc.Auth == nil {
				doc.Auth = &v1.AuthSettings{}
			}
			doc.Auth.AnonymousAccess = proto.Bool(false)
			raw, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(doc)
			if err != nil {
				return err
			}
			row.Value = string(raw)
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "scope_type"}, {Name: "scope_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&db.RegistryPortal{}).Where("id = ?", p.ID).Update("require_auth", false).Error; err != nil {
			return err
		}
		log.Info("portal %s (%s): require_auth moved to the portal settings tier", p.Name, p.ID)
	}
	return nil
}
