package migrate

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// CmdVerify produces the digest/tag parity report v1 vs v2: every v1 tag must
// exist in v2 under its mapped name with an identical manifest digest.
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

	fmt.Printf("\nverify: %d ok, %d mismatched, %d missing, %d extra", match, mismatch, missing, extra)
	if v2db != nil {
		fmt.Printf(", %d visibility divergences", visBad)
	}
	fmt.Println()

	if mismatch+missing+visBad > 0 {
		return fmt.Errorf("parity check failed: %d mismatched, %d missing, %d visibility divergences", mismatch, missing, visBad)
	}
	fmt.Println("v1 and v2 are in parity")
	return nil
}
