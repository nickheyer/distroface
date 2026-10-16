package migrations

import (
	"fmt"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/utils"
	"gorm.io/gorm"
)

func init() {
	register(migration{
		id:      "202610140003",
		name:    "reserved_namespaces",
		migrate: renameReservedNamespaces,
	})
}

// Accounts and orgs that shadow a route move to a suffixed name
func renameReservedNamespaces(tx *gorm.DB, log *logger.Logger) error {
	hasCasbin := tx.Migrator().HasTable("casbin_rule")
	free := func(name string) (bool, error) {
		var n int64
		if err := tx.Model(&db.User{}).Where("username = ?", name).Count(&n).Error; err != nil || n > 0 {
			return false, err
		}
		if err := tx.Model(&db.Organization{}).Where("name = ?", name).Count(&n).Error; err != nil || n > 0 {
			return false, err
		}
		return true, nil
	}
	pick := func(base string) (string, error) {
		for i := 0; ; i++ {
			candidate := base + "-user"
			if i > 0 {
				candidate = fmt.Sprintf("%s-user-%d", base, i)
			}
			if ok, err := free(candidate); err != nil {
				return "", err
			} else if ok && utils.NamespaceAllowed(candidate) {
				return candidate, nil
			}
		}
	}
	moveNamespace := func(old, updated string) error {
		for _, model := range []any{&db.Repository{}, &db.ArtifactRepository{}, &db.GitRepository{}} {
			if err := tx.Model(model).Where("namespace = ?", old).Update("namespace", updated).Error; err != nil {
				return err
			}
		}
		if hasCasbin {
			for _, res := range []string{"repositories", "artifacts", "git"} {
				if err := tx.Exec(`UPDATE casbin_rule SET v3 = ? || substr(v3, ?) WHERE ptype = 'p' AND v1 = ? AND (v3 = ? OR v3 LIKE ?)`,
					updated, len(old)+1, res, old, old+"/%").Error; err != nil {
					return err
				}
			}
		}
		return nil
	}

	var users []db.User
	if err := tx.Find(&users).Error; err != nil {
		return err
	}
	for i := range users {
		u := &users[i]
		if !utils.ReservedNamespaces[u.Username] {
			continue
		}
		updated, err := pick(u.Username)
		if err != nil {
			return err
		}
		if err := tx.Model(&db.User{}).Where("id = ?", u.ID).Update("username", updated).Error; err != nil {
			return err
		}
		if err := moveNamespace(u.Username, updated); err != nil {
			return err
		}
		log.Warn("user %q shadowed a route and was renamed to %q", u.Username, updated)
	}

	var orgs []db.Organization
	if err := tx.Find(&orgs).Error; err != nil {
		return err
	}
	for i := range orgs {
		o := &orgs[i]
		if !utils.ReservedNamespaces[o.Name] {
			continue
		}
		updated, err := pick(o.Name)
		if err != nil {
			return err
		}
		if err := tx.Model(&db.Organization{}).Where("id = ?", o.ID).Update("name", updated).Error; err != nil {
			return err
		}
		if err := moveNamespace(o.Name, updated); err != nil {
			return err
		}
		log.Warn("organization %q shadowed a route and was renamed to %q", o.Name, updated)
	}
	return nil
}
