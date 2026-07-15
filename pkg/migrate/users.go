package migrate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

func CmdUsers(ctx context.Context, cfg *config.MigrateConfig) error {
	v1db, err := OpenV1DB(cfg.V1DB)
	if err != nil {
		return err
	}
	defer v1db.Close()

	users, err := v1db.Users()
	if err != nil {
		return err
	}
	if cfg.DryRun {
		fmt.Printf("DRY RUN: %d v1 user(s) would be imported\n", len(users))
		for _, u := range users {
			roles, unknown := RolesForGroups(u.Groups)
			note := ""
			if !namespaceRegex.MatchString(u.Username) {
				note = "  !! username does not satisfy v2 name rules"
			}
			if len(unknown) > 0 {
				note += fmt.Sprintf("  !! unknown v1 group(s) %v -> defaulted to role 'user'", unknown)
			}
			fmt.Printf("  %-30s groups=%v -> roles=%v%s\n", u.Username, u.Groups, roles, note)
		}
		return nil
	}

	if cfg.V2DB == "" {
		return fmt.Errorf("-v2-db is required to import users")
	}
	v2, err := OpenV2(cfg.V2DB)
	if err != nil {
		return err
	}
	defer v2.Close()
	if err := v2.EnsureRolesSeeded(); err != nil {
		return fmt.Errorf("seed system roles: %w", err)
	}

	var created, skipped, failed int
	for _, u := range users {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !strings.HasPrefix(u.PasswordHash, "$2") {
			fmt.Printf("  !! %s: password hash is not bcrypt (%.4q...), importing anyway\n", u.Username, u.PasswordHash)
		}
		ok, roles, err := v2.ImportUser(ctx, u)
		switch {
		case err != nil:
			failed++
			fmt.Printf("  FAILED %s: %v\n", u.Username, err)
		case ok:
			created++
			logger.Logv(cfg, "  imported %s roles=%v", u.Username, roles)
		default:
			skipped++
			logger.Logv(cfg, "  exists  %s (skipped)", u.Username)
		}
	}
	fmt.Printf("users: %d imported, %d already existed, %d failed\n", created, skipped, failed)
	if failed > 0 {
		return fmt.Errorf("%d user(s) failed to import", failed)
	}
	return nil
}

// orgOwnerUsername picks the org owner: -v2-user if set, else "jenkins" (the
// v1 CI account that owns most content).
func orgOwnerUsername(cfg *config.MigrateConfig) string {
	if cfg.User != "" {
		return cfg.User
	}
	return "jenkins"
}

// plannedOrgs is the set of orgs migration needs: the legacy org for flat
// names plus every two-level namespace present in v1 (db and storage tree).
func plannedOrgs(cfg *config.MigrateConfig) ([]string, error) {
	seen := map[string]bool{cfg.LegacyNS: true}
	orgs := []string{cfg.LegacyNS}

	add := func(names []string) {
		for _, ns := range V1Namespaces(names) {
			if !seen[ns] {
				seen[ns] = true
				orgs = append(orgs, ns)
			}
		}
	}

	v1db, err := OpenV1DB(cfg.V1DB)
	if err != nil {
		return nil, err
	}
	images, err := v1db.Images()
	v1db.Close()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(images))
	for _, img := range images {
		names = append(names, img.Name)
	}
	add(names)

	if cfg.V1Root != "" {
		v1s, err := NewV1Storage(cfg.V1Root)
		if err != nil {
			return nil, err
		}
		repos, err := v1s.ListRepos()
		if err != nil {
			return nil, err
		}
		add(repos)
	}

	sort.Strings(orgs[1:]) // keep legacy org first
	return orgs, nil
}

func CmdOrgs(ctx context.Context, cfg *config.MigrateConfig) error {
	orgs, err := plannedOrgs(cfg)
	if err != nil {
		return err
	}
	owner := orgOwnerUsername(cfg)

	if cfg.DryRun {
		fmt.Printf("DRY RUN: %d org(s) would be ensured (owner: %s)\n", len(orgs), owner)
		for _, org := range orgs {
			note := ""
			if err := ValidateNamespace(org); err != nil {
				note = "  !! " + err.Error()
			}
			kind := "namespace org"
			if org == cfg.LegacyNS {
				kind = "legacy org (flat names map here)"
			}
			fmt.Printf("  %-30s %s%s\n", org, kind, note)
		}
		return nil
	}

	if cfg.V2DB == "" {
		return fmt.Errorf("-v2-db is required to create orgs")
	}
	v2, err := OpenV2(cfg.V2DB)
	if err != nil {
		return err
	}
	defer v2.Close()

	var created, existed, failed int
	for _, org := range orgs {
		ok, err := v2.EnsureOrg(ctx, org, owner)
		switch {
		case err != nil:
			failed++
			fmt.Printf("  FAILED %s: %v\n", org, err)
		case ok:
			created++
			fmt.Printf("  created org %s (owner: %s)\n", org, owner)
		default:
			existed++
			logger.Logv(cfg, "  exists  %s (skipped)", org)
		}
	}
	fmt.Printf("orgs: %d created, %d already existed, %d failed\n", created, existed, failed)
	if failed > 0 {
		return fmt.Errorf("%d org(s) failed", failed)
	}
	return nil
}
