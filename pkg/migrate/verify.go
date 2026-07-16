package migrate

import (
	"context"
	"fmt"
	"os"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// CmdVerify produces the parity report v1 vs v2: every v1 tag must exist in v2
// under its mapped name with an identical manifest digest, and with -v2-db
// every planned v1 artifact must exist with matching size and blob.
func CmdVerify(ctx context.Context, cfg *config.MigrateConfig) error {
	if cfg.V1Root == "" {
		return fmt.Errorf("-v1-root is required for verification")
	}
	if cfg.Registry == "" {
		return fmt.Errorf("-v2-url is required for verification")
	}
	v1s, err := NewV1Storage(cfg.V1Root)
	if err != nil {
		return err
	}
	repos, err := v1s.ListRepos()
	if err != nil {
		return err
	}

	replayer := NewReplayer(ctx, cfg, v1s) // reuse auth/ref plumbing

	var v2db *V2
	if cfg.V2DB != "" {
		if v2db, err = OpenV2(cfg.V2DB); err != nil {
			return err
		}
		defer v2db.Close()
	}

	privacy := map[string]bool{}
	if v1db, err := OpenV1DB(cfg.V1DB); err == nil {
		if images, err := v1db.Images(); err == nil {
			for _, img := range images {
				privacy[img.Name] = privacy[img.Name] || img.Private
			}
		}
		v1db.Close()
	}

	var match, mismatch, missing, extra, visBad int
	for _, repo := range repos {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		mapped := MapRepoName(repo, cfg.LegacyNS)
		repoRef, err := replayer.repoRef(mapped)
		if err != nil {
			return err
		}

		tags, err := v1s.ListTags(repo)
		if err != nil {
			return err
		}

		remoteTags := map[string]bool{}
		if listed, err := remote.List(repoRef, replayer.opts...); err == nil {
			for _, t := range listed {
				remoteTags[t] = true
			}
		}

		for _, tag := range tags {
			v1Digest, err := v1s.TagDigest(repo, tag)
			if err != nil {
				fmt.Printf("BROKEN   %s:%s (v1 unreadable: %v)\n", mapped, tag, err)
				missing++
				continue
			}
			head, err := remote.Head(repoRef.Tag(tag), replayer.opts...)
			switch {
			case err != nil:
				fmt.Printf("MISSING  %s:%s (want %s)\n", mapped, tag, v1Digest)
				missing++
			case head.Digest.String() != v1Digest:
				fmt.Printf("MISMATCH %s:%s v1=%s v2=%s\n", mapped, tag, v1Digest, head.Digest)
				mismatch++
			default:
				match++
				logger.Logv(cfg, "OK       %s:%s %s", mapped, tag, v1Digest)
			}
			delete(remoteTags, tag)
		}

		for _, tag := range sortedStringKeys(remoteTags) {
			fmt.Printf("EXTRA    %s:%s (present in v2, not in v1 — informational)\n", mapped, tag)
			extra++
		}

		if v2db != nil {
			ns, repoName := SplitRepoName(mapped)
			r, err := v2db.Store.GetRepository(ctx, ns, repoName)
			if err == nil && r != nil && r.IsPrivate != privacy[repo] {
				fmt.Printf("VISIBILITY %s: v1 private=%v, v2 private=%v\n", mapped, privacy[repo], r.IsPrivate)
				visBad++
			}
		}
	}

	var artOK, artMissing, artBad int
	if v2db != nil {
		if artOK, artMissing, artBad, err = verifyArtifacts(ctx, cfg, v2db, v1s); err != nil {
			return err
		}
	}

	fmt.Printf("\nverify: %d ok, %d mismatched, %d missing, %d extra", match, mismatch, missing, extra)
	if v2db != nil {
		fmt.Printf(", %d visibility divergences", visBad)
		fmt.Printf("; artifacts: %d ok, %d missing, %d divergent", artOK, artMissing, artBad)
	}
	fmt.Println()

	if mismatch+missing+visBad+artMissing+artBad > 0 {
		return fmt.Errorf("parity check failed: %d mismatched, %d missing, %d visibility divergences, %d artifact(s) missing, %d artifact(s) divergent",
			mismatch, missing, visBad, artMissing, artBad)
	}
	fmt.Println("v1 and v2 are in parity")
	return nil
}

// verifyArtifacts checks every planned v1 artifact against v2: the row must
// exist (by preserved ID, else by identity), sizes must match the v1 file, and
// with -v2-artifacts the content-addressed blob must be present and whole.
func verifyArtifacts(ctx context.Context, cfg *config.MigrateConfig, v2db *V2, v1s *V1Storage) (ok, missing, bad int, err error) {
	v1db, err := OpenV1DB(cfg.V1DB)
	if err != nil {
		return 0, 0, 0, err
	}
	defer v1db.Close()
	plan, err := PlanArtifacts(v1db)
	if err != nil {
		return 0, 0, 0, err
	}

	var blobs *artifacts.BlobStore
	if cfg.V2Artifacts != "" {
		if blobs, err = artifacts.NewBlobStore(cfg.V2Artifacts); err != nil {
			return 0, 0, 0, err
		}
	}

	repoIDs := map[int64]int64{}
	for _, r := range plan.Repos {
		v2r, err := v2db.Store.GetArtifactRepository(ctx, r.Owner, r.Name)
		if err != nil {
			return 0, 0, 0, err
		}
		if v2r == nil {
			fmt.Printf("ART MISSING  repo %s (not created in v2)\n", r.Name)
			continue
		}
		repoIDs[r.ID] = v2r.ID
	}

	for _, a := range plan.Artifacts {
		if ctx.Err() != nil {
			return ok, missing, bad, ctx.Err()
		}
		row, err := v2db.Store.GetArtifact(ctx, a.ID)
		if err != nil {
			return ok, missing, bad, err
		}
		if row == nil {
			if v2RepoID, found := repoIDs[a.RepoID]; found {
				if row, err = v2db.Store.GetArtifactByIdentity(ctx, v2RepoID, a.Version, a.Path, plan.Props[a.ID]); err != nil {
					return ok, missing, bad, err
				}
			}
		}
		if row == nil {
			fmt.Printf("ART MISSING  %s %s@%s\n", a.RepoName, a.Path, a.Version)
			missing++
			continue
		}

		divergent := false
		if info, statErr := os.Stat(v1s.ArtifactFilePath(a)); statErr == nil && info.Size() != row.Size {
			fmt.Printf("ART MISMATCH %s %s@%s: v1 file %d bytes, v2 row %d bytes\n", a.RepoName, a.Path, a.Version, info.Size(), row.Size)
			divergent = true
		}
		if row.PropsHash != db.PropsFingerprint(plan.Props[a.ID]) {
			fmt.Printf("ART MISMATCH %s %s@%s: v2 property set diverges from v1\n", a.RepoName, a.Path, a.Version)
			divergent = true
		}
		if blobs != nil && !divergent {
			f, info, blobErr := blobs.OpenBlob(row.Digest)
			switch {
			case blobErr != nil:
				fmt.Printf("ART MISMATCH %s %s@%s: blob %s unreadable: %v\n", a.RepoName, a.Path, a.Version, row.Digest, blobErr)
				divergent = true
			case info.Size() != row.Size:
				f.Close()
				fmt.Printf("ART MISMATCH %s %s@%s: blob %s is %d bytes, row says %d\n", a.RepoName, a.Path, a.Version, row.Digest, info.Size(), row.Size)
				divergent = true
			default:
				f.Close()
			}
		}
		if divergent {
			bad++
		} else {
			ok++
			logger.Logv(cfg, "ART OK       %s %s@%s", a.RepoName, a.Path, a.Version)
		}
	}
	return ok, missing, bad, nil
}
