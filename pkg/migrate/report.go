package migrate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nickheyer/distroface/pkg/config"
)

// CmdReport is the full dry-run analysis: everything the other commands would
// do, cross-checked between the v1 db and the v1 storage tree, without writes.
func CmdReport(ctx context.Context, cfg *config.MigrateConfig) error {
	v1db, err := OpenV1DB(cfg.V1DB)
	if err != nil {
		return err
	}
	defer v1db.Close()

	fmt.Println("═══ distroface v1 -> v2 migration report ═══")

	// ── Users ────────────────────────────────────────────────────────────
	users, err := v1db.Users()
	if err != nil {
		return err
	}
	groups, err := v1db.Groups()
	if err != nil {
		return err
	}
	roleCounts := map[string]int{}
	var badNames []string
	for _, u := range users {
		roles, _ := RolesForGroups(u.Groups)
		for _, r := range roles {
			roleCounts[r]++
		}
		if !namespaceRegex.MatchString(u.Username) {
			badNames = append(badNames, u.Username)
		}
	}
	fmt.Printf("\nUSERS: %d to import (bcrypt hashes port directly)\n", len(users))
	for _, role := range sortedStringKeys(roleCounts) {
		fmt.Printf("  role %-10s <- %d user(s)\n", role, roleCounts[role])
	}
	for name, roles := range groups {
		if _, known := groupRoleMap[name]; !known {
			fmt.Printf("  !! v1 group %q (roles %v) has no v2 mapping; members default to role 'user'\n", name, roles)
		}
	}
	if len(badNames) > 0 {
		fmt.Printf("  !! %d username(s) violate v2 name rules (imported as-is, but UI registration would reject them): %s\n",
			len(badNames), strings.Join(badNames, ", "))
	}

	// ── Images: db view ──────────────────────────────────────────────────
	images, err := v1db.Images()
	if err != nil {
		return err
	}
	dbNames := map[string]bool{}
	privacy := map[string]bool{}
	inconsistent := map[string]bool{}
	for _, img := range images {
		dbNames[img.Name] = true
		if prev, seen := privacy[img.Name]; seen && prev != img.Private {
			inconsistent[img.Name] = true
		}
		privacy[img.Name] = privacy[img.Name] || img.Private
	}
	flat, twoLevel := 0, 0
	for name := range dbNames {
		if strings.Contains(name, "/") {
			twoLevel++
		} else {
			flat++
		}
	}
	fmt.Printf("\nIMAGES (db): %d manifest row(s), %d distinct name(s) — %d flat (-> %s/*), %d two-level (pass through)\n",
		len(images), len(dbNames), flat, cfg.LegacyNS, twoLevel)
	if len(inconsistent) > 0 {
		fmt.Printf("  !! %d name(s) have INCONSISTENT private flags across manifests (resolved to private): %s\n",
			len(inconsistent), strings.Join(sortedStringKeys(inconsistent), ", "))
	}

	// ── Orgs ─────────────────────────────────────────────────────────────
	orgs, err := plannedOrgs(cfg)
	if err != nil {
		return err
	}
	v1Usernames := map[string]bool{}
	for _, u := range users {
		v1Usernames[u.Username] = true
	}
	fmt.Printf("\nORGS: %d to ensure (owner: %s)\n", len(orgs), orgOwnerUsername(cfg))
	for _, org := range orgs {
		kind := "namespace"
		if org == cfg.LegacyNS {
			kind = "legacy (flat names)"
		}
		note := ""
		if err := ValidateNamespace(org); err != nil {
			note = "  !! invalid v2 org name"
		}
		if v1Usernames[org] {
			note += "  !! collides with a v1 username"
		}
		fmt.Printf("  %-30s %s%s\n", org, kind, note)
	}

	// ── Images: storage view + cross-check ───────────────────────────────
	if cfg.V1Root != "" {
		v1s, err := NewV1Storage(cfg.V1Root)
		if err != nil {
			return err
		}
		repos, err := v1s.ListRepos()
		if err != nil {
			return err
		}
		storageNames := map[string]bool{}
		totalTags, brokenTags := 0, 0
		var brokenDetail []string
		for _, repo := range repos {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			storageNames[repo] = true
			scan := ScanRepo(v1s, repo, cfg.LegacyNS)
			totalTags += len(scan.Tags)
			for _, t := range scan.Tags {
				if issue := t.Issue(); issue != "" {
					brokenTags++
					brokenDetail = append(brokenDetail, fmt.Sprintf("  !! %s:%s — %s", repo, t.Tag, issue))
				}
			}
		}
		fmt.Printf("\nIMAGES (storage): %d repo(s), %d tag(s), %d broken tag(s)\n", len(repos), totalTags, brokenTags)
		for _, line := range brokenDetail {
			fmt.Println(line)
		}

		var dbOnly, storageOnly []string
		for name := range dbNames {
			if !storageNames[name] {
				dbOnly = append(dbOnly, name)
			}
		}
		for name := range storageNames {
			if !dbNames[name] {
				storageOnly = append(storageOnly, name)
			}
		}
		sort.Strings(dbOnly)
		sort.Strings(storageOnly)
		if len(dbOnly) > 0 {
			fmt.Printf("  !! %d name(s) in db but NOT in storage (cannot replay, content is gone): %s\n",
				len(dbOnly), strings.Join(dbOnly, ", "))
		}
		if len(storageOnly) > 0 {
			fmt.Printf("  ~ %d repo(s) in storage but not in db (replayed as public, no owner metadata): %s\n",
				len(storageOnly), strings.Join(storageOnly, ", "))
		}
	} else {
		fmt.Println("\nIMAGES (storage): skipped — pass -v1-root to scan the storage tree (blob existence, schema1 detection)")
	}

	// ── Artifacts ────────────────────────────────────────────────────────
	plan, err := PlanArtifacts(v1db)
	if err != nil {
		return err
	}
	var planSize int64
	propCount := 0
	for _, a := range plan.Artifacts {
		planSize += a.Size
		propCount += len(plan.Props[a.ID])
	}
	fmt.Printf("\nARTIFACTS: %d repo(s), %d artifact(s) (%s) + %d propertie(s) to import\n",
		len(plan.Repos), len(plan.Artifacts), humanBytes(planSize), propCount)
	if n := plan.TotalDupSkipped(); n > 0 {
		fmt.Printf("  dropping %d older duplicate row(s) of the same repo/version/path (v2 keeps only the newest)\n", n)
	}
	if len(plan.Invalid) > 0 {
		fmt.Printf("  !! %d row(s) are not importable (bad version/path) — run 'artifacts -dry-run' for detail\n", len(plan.Invalid))
	}
	fmt.Printf("  skipping %d orphaned artifact row(s) + %d orphaned property row(s)\n",
		plan.Orphans.OrphanedArtifacts, plan.Orphans.OrphanedProperties)

	// ── V2 state (optional) ──────────────────────────────────────────────
	if cfg.V2DB != "" {
		v2, err := OpenV2(cfg.V2DB)
		if err != nil {
			return err
		}
		defer v2.Close()
		existing := 0
		for _, u := range users {
			if user, err := v2.Store.GetUserByUsernameAndProvider(ctx, u.Username, "local"); err == nil && user != nil {
				existing++
			}
		}
		fmt.Printf("\nV2 STATE: %d of %d v1 users already exist (will be skipped)\n", existing, len(users))
	}

	fmt.Println("\nreport complete — run 'users', 'orgs', 'images', 'artifacts', then 'verify'")
	return nil
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
