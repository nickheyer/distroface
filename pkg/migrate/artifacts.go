package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sync"

	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// ArtifactPlan is the resolved v1 -> v2 artifact import set. V1 kept every
// re-upload of the same (repo, version, path); v2 enforces uniqueness with
// replace-on-push semantics, so the plan keeps only the newest row per
// identity — exactly what v2 would have retained had it been running all along.
type ArtifactPlan struct {
	Repos      []V1ArtifactRepo
	Artifacts  []V1Artifact // newest per (repo, version, path), in v1 db order
	Props      map[string]map[string]string
	DupSkipped map[int64]int // per v1 repo ID, older duplicate rows dropped
	Invalid    []string      // rows v2 cannot address (bad version/path), skipped
	Orphans    V1OrphanStats
}

func (p *ArtifactPlan) TotalDupSkipped() int {
	total := 0
	for _, n := range p.DupSkipped {
		total += n
	}
	return total
}

func PlanArtifacts(v1db *V1DB) (*ArtifactPlan, error) {
	plan := &ArtifactPlan{DupSkipped: map[int64]int{}}
	var err error
	if plan.Repos, err = v1db.ArtifactRepos(); err != nil {
		return nil, err
	}
	arts, err := v1db.LiveArtifacts()
	if err != nil {
		return nil, err
	}
	if plan.Props, err = v1db.LiveProperties(); err != nil {
		return nil, err
	}
	if plan.Orphans, err = v1db.OrphanStats(); err != nil {
		return nil, err
	}

	type key struct {
		repoID        int64
		version, path string
	}
	newest := map[key]V1Artifact{}
	valid := make([]V1Artifact, 0, len(arts))
	for _, a := range arts {
		if err := artifacts.ValidateVersion(a.Version); err != nil {
			plan.Invalid = append(plan.Invalid, fmt.Sprintf("%s %s@%s (id %s): %v", a.RepoName, a.Path, a.Version, a.ID, err))
			continue
		}
		if err := artifacts.ValidatePath(a.Path); err != nil {
			plan.Invalid = append(plan.Invalid, fmt.Sprintf("%s %s@%s (id %s): %v", a.RepoName, a.Path, a.Version, a.ID, err))
			continue
		}
		valid = append(valid, a)

		k := key{a.RepoID, a.Version, a.Path}
		cur, seen := newest[k]
		if !seen || a.CreatedAt.After(cur.CreatedAt) ||
			(a.CreatedAt.Equal(cur.CreatedAt) && a.RowID > cur.RowID) {
			newest[k] = a
		}
	}
	for _, a := range valid {
		if newest[key{a.RepoID, a.Version, a.Path}].ID == a.ID {
			plan.Artifacts = append(plan.Artifacts, a)
		} else {
			plan.DupSkipped[a.RepoID]++
		}
	}
	return plan, nil
}

type ArtifactStatus string

const (
	ArtImported ArtifactStatus = "imported"
	ArtExists   ArtifactStatus = "exists"   // same v1 artifact ID already in v2
	ArtOccupied ArtifactStatus = "occupied" // identity taken by a different (newer) v2 row
	ArtFailed   ArtifactStatus = "failed"
)

type ArtifactResult struct {
	Art    V1Artifact
	Status ArtifactStatus
	Err    error
}

// CmdArtifacts imports v1 artifact repos into v2: blobs into content-addressed
// storage, rows + properties into the v2 db, preserving v1 IDs and timestamps.
// Without -v2-db/-v2-artifacts it prints the migration plan and exits.
func CmdArtifacts(ctx context.Context, cfg *config.MigrateConfig) error {
	v1db, err := OpenV1DB(cfg.V1DB)
	if err != nil {
		return err
	}
	defer v1db.Close()

	plan, err := PlanArtifacts(v1db)
	if err != nil {
		return err
	}

	var v1s *V1Storage
	if cfg.V1Root != "" {
		if v1s, err = NewV1Storage(cfg.V1Root); err != nil {
			return err
		}
	}

	printArtifactPlan(plan, v1s)

	if cfg.DryRun {
		return nil
	}
	if cfg.V2DB == "" || cfg.V2Artifacts == "" {
		fmt.Println("\nplan only — pass -v1-root, -v2-db, and -v2-artifacts (the v2 artifacts.storage_path) to import")
		return nil
	}
	if v1s == nil {
		return fmt.Errorf("-v1-root is required to import artifact files")
	}
	if info, err := os.Stat(cfg.V2Artifacts); err != nil || !info.IsDir() {
		return fmt.Errorf("v2 artifacts root %s not found (has the v2 server been started once?)", cfg.V2Artifacts)
	}

	v2, err := OpenV2(cfg.V2DB)
	if err != nil {
		return err
	}
	defer v2.Close()
	blobs, err := artifacts.NewBlobStore(cfg.V2Artifacts)
	if err != nil {
		return err
	}

	// Repos first: v1 integer IDs remap to v2 rows by name.
	repoMap := map[int64]int64{}
	for _, r := range plan.Repos {
		id, created, err := v2.EnsureArtifactRepo(ctx, r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAILED repo %s: %v (its artifacts will be skipped)\n", r.Name, err)
			continue
		}
		repoMap[r.ID] = id
		if created {
			fmt.Printf("created artifact repo %s (owner %s)\n", r.Name, r.Owner)
		} else {
			logger.Logv(cfg, "artifact repo %s already exists", r.Name)
		}
	}

	results := importArtifacts(ctx, cfg, v2, blobs, v1s, plan, repoMap)
	return summarizeArtifactResults(results, plan)
}

// stagedArtifact is a blob already landed in v2 content storage, ready for
// its row + properties to be inserted.
type stagedArtifact struct {
	a      V1Artifact
	props  map[string]string
	repoID int64
	digest string
	size   int64
	mime   string
}

// importArtifacts runs a two-stage pipeline: workers hash-copy v1 files into
// the blob store in parallel, one inserter writes rows. SQLite rejects
// concurrent read-then-write transactions (immediate BUSY on lock upgrade in
// WAL), so all db writes stay on a single goroutine.
func importArtifacts(ctx context.Context, cfg *config.MigrateConfig, v2 *V2, blobs *artifacts.BlobStore, v1s *V1Storage, plan *ArtifactPlan, repoMap map[int64]int64) []ArtifactResult {
	jobs := cfg.Jobs
	if jobs < 1 {
		jobs = 1
	}
	var (
		mu      sync.Mutex
		results []ArtifactResult
	)
	record := func(a V1Artifact, status ArtifactStatus, err error) {
		switch status {
		case ArtImported:
			fmt.Printf("  imported %s %s@%s (%s)\n", a.RepoName, a.Path, a.Version, humanBytes(a.Size))
		case ArtExists:
			logger.Logv(cfg, "  exists   %s %s@%s (skipped)", a.RepoName, a.Path, a.Version)
		case ArtOccupied:
			fmt.Printf("  kept v2  %s %s@%s (a different artifact already holds this version+path)\n", a.RepoName, a.Path, a.Version)
		case ArtFailed:
			fmt.Fprintf(os.Stderr, "  FAILED   %s %s@%s: %v\n", a.RepoName, a.Path, a.Version, err)
		}
		mu.Lock()
		results = append(results, ArtifactResult{Art: a, Status: status, Err: err})
		mu.Unlock()
	}

	staged := make(chan stagedArtifact, jobs)
	inserterDone := make(chan []string)
	go func() {
		var failedDigests []string
		for s := range staged {
			if err := insertArtifact(ctx, v2, s); err != nil {
				failedDigests = append(failedDigests, s.digest)
				record(s.a, ArtFailed, err)
			} else {
				record(s.a, ArtImported, nil)
			}
		}
		inserterDone <- failedDigests
	}()

	var wg sync.WaitGroup
	work := make(chan V1Artifact)
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range work {
				repoID, ok := repoMap[a.RepoID]
				if !ok {
					record(a, ArtFailed, fmt.Errorf("repo %s was not created in v2", a.RepoName))
					continue
				}
				s, status, err := stageArtifact(ctx, v2, blobs, v1s, a, plan.Props[a.ID], repoID)
				if status != "" {
					record(a, status, err)
					continue
				}
				staged <- s
			}
		}()
	}
	for _, a := range plan.Artifacts {
		if ctx.Err() != nil {
			break
		}
		work <- a
	}
	close(work)
	wg.Wait()
	close(staged)

	// GC only after every copy has settled so a shared digest cannot vanish
	// under an artifact that still needs it.
	for _, digest := range <-inserterDone {
		if n, err := v2.Store.CountArtifactsByDigest(ctx, digest); err == nil && n == 0 {
			blobs.DeleteBlob(digest)
		}
	}
	return results
}

// stageArtifact copies one v1 file into the v2 blob store, hashing en route,
// deduplicated by digest. A non-empty status means the artifact is already
// settled (skip or failure) and nothing was staged.
func stageArtifact(ctx context.Context, v2 *V2, blobs *artifacts.BlobStore, v1s *V1Storage, a V1Artifact, props map[string]string, repoID int64) (stagedArtifact, ArtifactStatus, error) {
	none := stagedArtifact{}
	if existing, err := v2.Store.GetArtifact(ctx, a.ID); err != nil {
		return none, ArtFailed, err
	} else if existing != nil {
		return none, ArtExists, nil
	}
	if occupant, err := v2.Store.GetArtifactByPathVersion(ctx, repoID, a.Version, a.Path); err != nil {
		return none, ArtFailed, err
	} else if occupant != nil {
		return none, ArtOccupied, nil
	}

	src, err := os.Open(v1s.ArtifactFilePath(a))
	if err != nil {
		return none, ArtFailed, fmt.Errorf("v1 file missing: %w", err)
	}
	uploadID, err := blobs.InitiateUpload()
	if err != nil {
		src.Close()
		return none, ArtFailed, err
	}
	_, err = blobs.AppendChunk(uploadID, src)
	src.Close()
	if err != nil {
		blobs.CancelUpload(uploadID)
		return none, ArtFailed, err
	}
	digest, size, detectedMime, err := blobs.CompleteUpload(uploadID)
	if err != nil {
		blobs.CancelUpload(uploadID)
		return none, ArtFailed, err
	}
	if size != a.Size {
		fmt.Fprintf(os.Stderr, "  WARN %s %s@%s: v1 db size %d differs from file size %d (using file)\n",
			a.RepoName, a.Path, a.Version, a.Size, size)
	}
	return stagedArtifact{a: a, props: props, repoID: repoID, digest: digest, size: size, mime: detectedMime}, "", nil
}

// insertArtifact writes the row + properties, preserving the v1 artifact ID
// and timestamps so re-runs skip cleanly.
func insertArtifact(ctx context.Context, v2 *V2, s stagedArtifact) error {
	a := s.a
	mimeType := a.MimeType
	if mimeType == "" {
		mimeType = s.mime
	}
	metadata := a.Metadata
	if metadata == "" {
		metadata = "{}"
	} else if !json.Valid([]byte(metadata)) {
		fmt.Fprintf(os.Stderr, "  WARN %s %s@%s: v1 metadata is not valid JSON, replaced with {}\n", a.RepoName, a.Path, a.Version)
		metadata = "{}"
	}
	name := a.Name
	if name == "" {
		name = path.Base(a.Path)
	}

	artifact := &db.Artifact{
		ID:        a.ID,
		RepoID:    s.repoID,
		Name:      name,
		Path:      a.Path,
		UploadID:  a.UploadID,
		Version:   a.Version,
		Digest:    s.digest,
		Size:      s.size,
		MimeType:  mimeType,
		Metadata:  metadata,
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
	_, err := v2.Store.CreateArtifact(ctx, artifact, s.props)
	return err
}

func printArtifactPlan(plan *ArtifactPlan, v1s *V1Storage) {
	type repoTally struct {
		count, missing int
		size           int64
		props          int
	}
	tally := map[int64]*repoTally{}
	for _, r := range plan.Repos {
		tally[r.ID] = &repoTally{}
	}
	for _, a := range plan.Artifacts {
		t := tally[a.RepoID]
		t.count++
		t.size += a.Size
		t.props += len(plan.Props[a.ID])
		if v1s != nil {
			if _, err := os.Stat(v1s.ArtifactFilePath(a)); err != nil {
				t.missing++
			}
		}
	}

	fmt.Printf("v1 artifact repositories: %d\n\n", len(plan.Repos))
	for _, r := range plan.Repos {
		t := tally[r.ID]
		visibility := "public"
		if r.Private {
			visibility = "private"
		}
		fmt.Printf("  %-20s owner=%-16s %-7s %5d artifact(s), %8s, %6d propertie(s)",
			r.Name, r.Owner, visibility, t.count, humanBytes(t.size), t.props)
		if n := plan.DupSkipped[r.ID]; n > 0 {
			fmt.Printf(", %d older duplicate(s) dropped", n)
		}
		if v1s != nil {
			fmt.Printf(", %d missing file(s) on disk", t.missing)
		}
		fmt.Println()
	}

	if n := plan.TotalDupSkipped(); n > 0 {
		fmt.Printf("\ndropping %d older duplicate row(s): v1 kept every re-upload of the same repo/version/path, v2 keeps only the newest (replace-on-push)\n", n)
	}
	for _, msg := range plan.Invalid {
		fmt.Printf("  !! not importable in v2: %s\n", msg)
	}
	fmt.Printf("skipped as orphaned (no surviving repo row): %d artifact(s), %d propertie(s)\n",
		plan.Orphans.OrphanedArtifacts, plan.Orphans.OrphanedProperties)
	if v1s == nil {
		fmt.Println("(pass -v1-root to check artifact files on disk)")
	}
}

func summarizeArtifactResults(results []ArtifactResult, plan *ArtifactPlan) error {
	var imported, exists, occupied, failed int
	for _, r := range results {
		switch r.Status {
		case ArtImported:
			imported++
		case ArtExists:
			exists++
		case ArtOccupied:
			occupied++
		case ArtFailed:
			failed++
		}
	}
	fmt.Printf("\nartifacts: %d imported, %d already existed, %d kept newer v2 content, %d failed (%d duplicate row(s) dropped by plan)\n",
		imported, exists, occupied, failed, plan.TotalDupSkipped())
	if failed > 0 {
		return fmt.Errorf("%d artifact(s) failed to import", failed)
	}
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
