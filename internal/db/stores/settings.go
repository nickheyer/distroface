package stores

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ── Internal state kv, jwt secret and friends ────────────────────────────

func (s *Store) GetSystemSetting(ctx context.Context, key string) (string, error) {
	var setting db.SystemSetting
	err := s.db.WithContext(ctx).First(&setting, "key = ?", key).Error
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *Store) SetSystemSetting(ctx context.Context, key, value string) error {
	setting := db.SystemSetting{Key: key, Value: value}
	return s.db.WithContext(ctx).Save(&setting).Error
}

// ── Scoped settings documents ────────────────────────────────────────────

func (s *Store) GetSettingsValue(ctx context.Context, scope v1.SettingsScopeType, scopeID string) (string, bool, error) {
	var row db.SettingsRow
	err := s.db.WithContext(ctx).First(&row, "scope_type = ? AND scope_id = ?", int32(scope), scopeID).Error
	if err == gorm.ErrRecordNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Value, true, nil
}

func (s *Store) SetSettingsValue(ctx context.Context, scope v1.SettingsScopeType, scopeID, value string) error {
	row := db.SettingsRow{ScopeType: int32(scope), ScopeID: scopeID, Value: value}
	// Save misreads the empty scope id pk as a fresh row
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "scope_type"}, {Name: "scope_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&row).Error
}

func (s *Store) DeleteSettingsValue(ctx context.Context, scope v1.SettingsScopeType, scopeID string) error {
	return s.db.WithContext(ctx).Delete(&db.SettingsRow{}, "scope_type = ? AND scope_id = ?", int32(scope), scopeID).Error
}

func (s *Store) GetPortalOrgID(ctx context.Context, portalID string) (string, error) {
	var portal db.RegistryPortal
	err := s.db.WithContext(ctx).Select("org_id").First(&portal, "id = ?", portalID).Error
	if err != nil {
		return "", err
	}
	return portal.OrgID, nil
}

// ── Legacy kv and column migration ───────────────────────────────────────

// Legacy string keys retired by the typed settings documents
var legacySystemKeys = []string{
	"auth.local.enabled", "auth.local.allow_registration", "auth.anonymous_access",
	"auth.session_timeout", "tls.acme.enabled", "tls.acme.email",
	"tls.acme.directory_url", "tls.primary_source",
	"portals.hostname_blacklist", "portals.require_hostname_approval",
}

func legacyBool(v string) *bool {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil
	}
	return &b
}

func legacyInt32(v string) *int32 {
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil
	}
	out := int32(n)
	return &out
}

func legacyInt64(v string) *int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

var legacyPrimarySource = map[string]v1.CertSource{
	"config": v1.CertSource_CERT_SOURCE_CONFIG,
	"manual": v1.CertSource_CERT_SOURCE_MANUAL,
	"acme":   v1.CertSource_CERT_SOURCE_ACME,
	"app_ca": v1.CertSource_CERT_SOURCE_APP_CA,
}

// Builds a settings document from one flat legacy kv map
func settingsFromLegacyKV(kv map[string]string) *v1.Settings {
	out := &v1.Settings{}
	auth := &v1.AuthSettings{}
	acme := &v1.ACMESettings{}
	tls := &v1.TLSSettings{}
	portals := &v1.PortalPolicySettings{}
	arts := &v1.ArtifactSettings{}
	ret := &v1.ArtifactRetentionSettings{}

	for key, val := range kv {
		switch key {
		case "auth.local.enabled":
			auth.LocalEnabled = legacyBool(val)
		case "auth.local.allow_registration":
			auth.LocalAllowRegistration = legacyBool(val)
		case "auth.anonymous_access":
			auth.AnonymousAccess = legacyBool(val)
		case "auth.session_timeout":
			auth.SessionTimeoutSeconds = legacyInt32(val)
		case "tls.acme.enabled":
			acme.Enabled = legacyBool(val)
		case "tls.acme.email":
			acme.Email = proto.String(val)
		case "tls.acme.directory_url":
			acme.DirectoryUrl = proto.String(val)
		case "tls.primary_source":
			if src, ok := legacyPrimarySource[val]; ok {
				tls.PrimarySource = src.Enum()
			}
		case "portals.hostname_blacklist":
			var list []string
			if json.Unmarshal([]byte(val), &list) == nil {
				portals.HostnameBlacklist = list
			}
		case "portals.require_hostname_approval":
			portals.RequireHostnameApproval = legacyBool(val)
		case "artifacts.max_file_size_mb":
			arts.MaxFileSizeMb = legacyInt64(val)
		case "artifacts.private_by_default":
			arts.PrivateByDefault = legacyBool(val)
		case "artifacts.retention.enabled":
			ret.Enabled = legacyBool(val)
		case "artifacts.retention.max_versions":
			ret.MaxVersions = legacyInt32(val)
		case "artifacts.retention.max_age_days":
			ret.MaxAgeDays = legacyInt32(val)
		case "artifacts.retention.max_total_size":
			ret.MaxTotalSizeBytes = legacyInt64(val)
		case "artifacts.retention.exclude_latest":
			ret.ExcludeLatest = legacyBool(val)
		}
	}

	if proto.Size(ret) > 0 {
		arts.Retention = ret
	}
	for msg, assign := range map[proto.Message]func(){
		auth:    func() { out.Auth = auth },
		acme:    func() { out.Acme = acme },
		tls:     func() { out.Tls = tls },
		portals: func() { out.Portals = portals },
		arts:    func() { out.Artifacts = arts },
	} {
		if proto.Size(msg) > 0 {
			assign()
		}
	}
	return out
}

// Moves acme.internal_enabled to ca.acme_enabled
func (s *Store) migrateInternalACMEFlag() error {
	var rows []db.SettingsRow
	if err := s.db.Where("value LIKE ?", "%internal_enabled%").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var doc map[string]json.RawMessage
		if json.Unmarshal([]byte(row.Value), &doc) != nil {
			continue
		}
		var acme map[string]json.RawMessage
		if json.Unmarshal(doc["acme"], &acme) != nil {
			continue
		}
		flag, ok := acme["internal_enabled"]
		if !ok {
			continue
		}
		delete(acme, "internal_enabled")
		if len(acme) == 0 {
			delete(doc, "acme")
		} else {
			doc["acme"], _ = json.Marshal(acme)
		}
		ca := map[string]json.RawMessage{}
		json.Unmarshal(doc["ca"], &ca)
		ca["acme_enabled"] = flag
		doc["ca"], _ = json.Marshal(ca)
		value, err := json.Marshal(doc)
		if err != nil {
			continue
		}
		if err := s.db.Model(&db.SettingsRow{}).
			Where("scope_type = ? AND scope_id = ?", row.ScopeType, row.ScopeID).
			Update("value", string(value)).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) saveLegacySettings(scope v1.SettingsScopeType, scopeID string, doc *v1.Settings) error {
	if proto.Size(doc) == 0 {
		return nil
	}
	var count int64
	s.db.Model(&db.SettingsRow{}).
		Where("scope_type = ? AND scope_id = ?", int32(scope), scopeID).Count(&count)
	if count > 0 {
		return nil
	}
	raw, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(doc)
	if err != nil {
		return err
	}
	return s.db.Create(&db.SettingsRow{ScopeType: int32(scope), ScopeID: scopeID, Value: string(raw)}).Error
}

// Converts kv rows and portal columns into settings documents
func (s *Store) migrateLegacySettings() error {
	// System kv rows
	var sysRows []db.SystemSetting
	if err := s.db.Where("key IN ?", legacySystemKeys).Find(&sysRows).Error; err == nil && len(sysRows) > 0 {
		kv := map[string]string{}
		for _, r := range sysRows {
			kv[r.Key] = r.Value
		}
		if err := s.saveLegacySettings(v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", settingsFromLegacyKV(kv)); err != nil {
			return err
		}
		if err := s.db.Delete(&db.SystemSetting{}, "key IN ?", legacySystemKeys).Error; err != nil {
			return err
		}
	}

	// Org kv table
	if s.db.Migrator().HasTable("org_settings") {
		type orgSetting struct {
			OrgID string
			Key   string
			Value string
		}
		var rows []orgSetting
		if err := s.db.Table("org_settings").Find(&rows).Error; err != nil {
			return err
		}
		perOrg := map[string]map[string]string{}
		for _, r := range rows {
			if perOrg[r.OrgID] == nil {
				perOrg[r.OrgID] = map[string]string{}
			}
			perOrg[r.OrgID][r.Key] = r.Value
		}
		for orgID, kv := range perOrg {
			if err := s.saveLegacySettings(v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG, orgID, settingsFromLegacyKV(kv)); err != nil {
				return err
			}
		}
		if err := s.db.Migrator().DropTable("org_settings"); err != nil {
			return err
		}
	}

	if err := s.migrateInternalACMEFlag(); err != nil {
		return err
	}

	// Portal acme override columns
	if s.db.Migrator().HasColumn(&db.RegistryPortal{}, "acme_email") {
		type portalACME struct {
			ID               string
			ACMEEmail        string `gorm:"column:acme_email"`
			ACMEDirectoryURL string `gorm:"column:acme_directory_url"`
		}
		var rows []portalACME
		if err := s.db.Table("registry_portals").
			Where("acme_email <> '' OR acme_directory_url <> ''").Find(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			acme := &v1.ACMESettings{}
			if r.ACMEEmail != "" {
				acme.Email = proto.String(r.ACMEEmail)
			}
			if r.ACMEDirectoryURL != "" {
				acme.DirectoryUrl = proto.String(r.ACMEDirectoryURL)
			}
			doc := &v1.Settings{Acme: acme}
			if err := s.saveLegacySettings(v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL, r.ID, doc); err != nil {
				return err
			}
		}
		for _, col := range []string{"acme_email", "acme_directory_url"} {
			if err := s.db.Exec("ALTER TABLE registry_portals DROP COLUMN " + col).Error; err != nil {
				return err
			}
		}
	}

	return nil
}
