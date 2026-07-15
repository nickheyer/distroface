package migrate

import (
	"context"
	"fmt"
	"os"

	"github.com/nickheyer/distroface/pkg/config"
)

// CmdArtifacts analyzes v1 artifact data: what is live (will migrate) vs
// orphaned (intentionally skipped — v1 leaked rows on repo deletion).
//
// The actual import lands once the v2 artifact backend exists (Phase 7);
// everything read here (live artifacts + live properties + file paths) is the
// exact input that import will consume.
func CmdArtifacts(ctx context.Context, cfg *config.MigrateConfig) error {
	v1db, err := OpenV1DB(cfg.V1DB)
	if err != nil {
		return err
	}
	defer v1db.Close()

	repos, err := v1db.ArtifactRepos()
	if err != nil {
		return err
	}
	arts, err := v1db.LiveArtifacts()
	if err != nil {
		return err
	}
	props, err := v1db.LiveProperties()
	if err != nil {
		return err
	}
	orphans, err := v1db.OrphanStats()
	if err != nil {
		return err
	}

	var v1s *V1Storage
	if cfg.V1Root != "" {
		if v1s, err = NewV1Storage(cfg.V1Root); err != nil {
			return err
		}
	}

	type repoTally struct {
		count, missing int
		size           int64
		props          int
	}
	tally := map[string]*repoTally{}
	for _, r := range repos {
		tally[r.Name] = &repoTally{}
	}
	for _, a := range arts {
		t := tally[a.RepoName]
		t.count++
		t.size += a.Size
		t.props += len(props[a.ID])
		if v1s != nil {
			if _, err := os.Stat(v1s.ArtifactFilePath(a)); err != nil {
				t.missing++
			}
		}
	}

	fmt.Printf("v1 artifact repositories: %d\n\n", len(repos))
	for _, r := range repos {
		t := tally[r.Name]
		visibility := "public"
		if r.Private {
			visibility = "private"
		}
		fmt.Printf("  %-20s owner=%-16s %-7s %5d live artifact(s), %8s, %6d live propertie(s)",
			r.Name, r.Owner, visibility, t.count, humanBytes(t.size), t.props)
		if v1s != nil {
			fmt.Printf(", %d missing file(s) on disk", t.missing)
		}
		fmt.Println()
	}

	fmt.Printf("\nskipped as orphaned (no surviving repo/artifact row): %d artifact(s), %d propertie(s)\n",
		orphans.OrphanedArtifacts, orphans.OrphanedProperties)
	if v1s == nil {
		fmt.Println("(pass -v1-root to check artifact files on disk)")
	}

	fmt.Println("\nNOTE: artifact import requires the v2 artifact backend (Phase 7), which does not exist yet.")
	fmt.Println("This analysis is the exact live-data set that import will replay once it lands.")
	return nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
