package migrations

import (
	"fmt"
	"strconv"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/utils"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202607220001",
		name:    "rename_invalid_oidc_usernames",
		migrate: renameInvalidOIDCUsernames,
	})
}

// Email shaped oidc usernames become valid registry namespaces
func renameInvalidOIDCUsernames(tx *gorm.DB, log *logger.Logger) error {
	var users []db.User
	if err := tx.Find(&users, "auth_provider = ?", "oidc").Error; err != nil {
		return err
	}
	renamed := 0
	for i := range users {
		u := &users[i]
		if utils.UsernameRegex.MatchString(u.Username) {
			continue
		}
		email := ""
		if u.Email != nil {
			email = *u.Email
		}
		base := utils.UsernameFromClaims(u.ID, u.Username, email)
		name, err := availableUsername(tx, base)
		if err != nil {
			return err
		}
		if err := tx.Model(&db.User{}).Where("id = ?", u.ID).Update("username", name).Error; err != nil {
			return err
		}
		log.Info("renamed oidc user %s from %q to %q", u.ID, u.Username, name)
		renamed++
	}
	log.Info("renamed %d of %d oidc users", renamed, len(users))
	return nil
}

// Counter suffix until no user or org owns it
func availableUsername(tx *gorm.DB, base string) (string, error) {
	name := base
	for i := 2; i < 100; i++ {
		var users int64
		if err := tx.Model(&db.User{}).Where("username = ?", name).Count(&users).Error; err != nil {
			return "", err
		}
		if users == 0 {
			var orgs int64
			if err := tx.Model(&db.Organization{}).Where("name = ?", name).Count(&orgs).Error; err != nil {
				return "", err
			}
			if orgs == 0 {
				return name, nil
			}
		}
		suffix := strconv.Itoa(i)
		name = base
		if len(name)+len(suffix) > 40 {
			name = name[:40-len(suffix)]
		}
		name += suffix
	}
	return "", fmt.Errorf("no available username for %q", base)
}
