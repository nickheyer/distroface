package migrate

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// fileLayer streams a v1 blob file as a v1.Layer for upload. The digest is
// taken from the manifest descriptor; remote verifies content on upload.
type fileLayer struct {
	path   string
	digest ggcrv1.Hash
	size   int64
	mt     types.MediaType
}

func (l *fileLayer) Digest() (ggcrv1.Hash, error)         { return l.digest, nil }
func (l *fileLayer) DiffID() (ggcrv1.Hash, error)         { return l.digest, nil }
func (l *fileLayer) Size() (int64, error)                 { return l.size, nil }
func (l *fileLayer) MediaType() (types.MediaType, error)  { return l.mt, nil }
func (l *fileLayer) Compressed() (io.ReadCloser, error)   { return os.Open(l.path) }
func (l *fileLayer) Uncompressed() (io.ReadCloser, error) { return os.Open(l.path) }

// rawManifest pushes exact v1 manifest bytes so digests are preserved.
type rawManifest struct {
	raw []byte
	mt  types.MediaType
}

func (m rawManifest) RawManifest() ([]byte, error)        { return m.raw, nil }
func (m rawManifest) MediaType() (types.MediaType, error) { return m.mt, nil }

type TagStatus string

const (
	TagPushed   TagStatus = "pushed"
	TagUpToDate TagStatus = "up-to-date"
	TagFailed   TagStatus = "failed"
)

type TagResult struct {
	Repo   string // v1 name
	Mapped string
	Tag    string
	Digest string
	Status TagStatus
	Err    error
}

type Replayer struct {
	cfg  *config.MigrateConfig
	v1   *V1Storage
	opts []remote.Option
}

func NewReplayer(ctx context.Context, cfg *config.MigrateConfig, v1s *V1Storage) *Replayer {
	return &Replayer{
		cfg: cfg,
		v1:  v1s,
		opts: []remote.Option{
			remote.WithAuth(&authn.Basic{Username: cfg.User, Password: cfg.Pass}),
			remote.WithContext(ctx),
			remote.WithUserAgent("migrate"),
		},
	}
}

func (r *Replayer) repoRef(mapped string) (name.Repository, error) {
	var opts []name.Option
	if r.cfg.PlainHTTP {
		opts = append(opts, name.Insecure)
	}
	return name.NewRepository(r.cfg.Registry+"/"+mapped, opts...)
}

// PushRepo replays every tag of one v1 repo into v2.
func (r *Replayer) PushRepo(ctx context.Context, v1Name string) ([]TagResult, error) {
	mapped := MapRepoName(v1Name, r.cfg.LegacyNS)
	repo, err := r.repoRef(mapped)
	if err != nil {
		return nil, fmt.Errorf("repo ref %s: %w", mapped, err)
	}
	tags, err := r.v1.ListTags(v1Name)
	if err != nil {
		return nil, err
	}

	// Tags often share digests; ensure each manifest tree only once per repo.
	ensured := make(map[string]rawManifest)

	var results []TagResult
	for _, tag := range tags {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		res := TagResult{Repo: v1Name, Mapped: mapped, Tag: tag}
		res.Digest, err = r.v1.TagDigest(v1Name, tag)
		if err != nil {
			res.Status, res.Err = TagFailed, err
			results = append(results, res)
			continue
		}

		tagRef := repo.Tag(tag)
		if head, err := remote.Head(tagRef, r.opts...); err == nil && head.Digest.String() == res.Digest {
			res.Status = TagUpToDate
			results = append(results, res)
			continue
		}

		manifest, err := r.ensureTree(ctx, repo, v1Name, res.Digest, ensured)
		if err != nil {
			res.Status, res.Err = TagFailed, err
			results = append(results, res)
			continue
		}
		if err := remote.Put(tagRef, manifest, r.opts...); err != nil {
			res.Status, res.Err = TagFailed, fmt.Errorf("put manifest: %w", err)
			results = append(results, res)
			continue
		}
		res.Status = TagPushed
		results = append(results, res)
		logger.Logv(r.cfg, "  pushed %s:%s (%s)", mapped, tag, res.Digest)
	}
	return results, nil
}

// ensureTree uploads everything a manifest needs (blobs, child manifests for
// indexes) and returns the manifest ready to Put. Children are Put by digest;
// the caller Puts the root at its tag.
func (r *Replayer) ensureTree(ctx context.Context, repo name.Repository, v1Name, digest string, ensured map[string]rawManifest) (rawManifest, error) {
	if m, ok := ensured[digest]; ok {
		return m, nil
	}

	raw, err := r.v1.ManifestBytes(v1Name, digest)
	if err != nil {
		return rawManifest{}, fmt.Errorf("manifest %s: %w", digest, err)
	}
	m, err := ParseV1Manifest(raw)
	if err != nil {
		return rawManifest{}, fmt.Errorf("manifest %s: parse: %w", digest, err)
	}
	if m.IsSchema1() {
		return rawManifest{}, fmt.Errorf("manifest %s is docker schema1, which distribution v3 rejects (re-push from a modern client or skip)", digest)
	}

	if m.IsIndex() {
		for _, child := range m.Manifests {
			childManifest, err := r.ensureTree(ctx, repo, v1Name, child.Digest, ensured)
			if err != nil {
				return rawManifest{}, fmt.Errorf("index child %s: %w", child.Digest, err)
			}
			childDigest, err := name.NewDigest(repo.String() + "@" + child.Digest)
			if err != nil {
				return rawManifest{}, err
			}
			if err := remote.Put(childDigest, childManifest, r.opts...); err != nil {
				return rawManifest{}, fmt.Errorf("put index child %s: %w", child.Digest, err)
			}
		}
	} else {
		for _, blob := range m.Blobs() {
			size, err := r.v1.StatBlob(blob.Digest)
			if err != nil {
				return rawManifest{}, fmt.Errorf("blob %s missing in v1 storage: %w", blob.Digest, err)
			}
			hash, err := ggcrv1.NewHash(blob.Digest)
			if err != nil {
				return rawManifest{}, fmt.Errorf("blob %s: %w", blob.Digest, err)
			}
			layer := &fileLayer{
				path:   r.v1.BlobPath(blob.Digest),
				digest: hash,
				size:   size,
				mt:     types.MediaType(blob.MediaType),
			}
			// remote.WriteLayer HEADs the blob first and skips if present (dedup).
			if err := remote.WriteLayer(repo, layer, r.opts...); err != nil {
				return rawManifest{}, fmt.Errorf("upload blob %s: %w", blob.Digest, err)
			}
		}
	}

	result := rawManifest{raw: raw, mt: types.MediaType(m.EffectiveMediaType())}
	ensured[digest] = result
	return result, nil
}

func CmdImages(ctx context.Context, cfg *config.MigrateConfig) error {
	if cfg.V1Root == "" {
		return fmt.Errorf("-v1-root is required for image replay")
	}
	if cfg.Registry == "" {
		return fmt.Errorf("-v2-url is required for image replay")
	}
	v1s, err := NewV1Storage(cfg.V1Root)
	if err != nil {
		return err
	}
	repos, err := v1s.ListRepos()
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("no repositories found under %s/repositories", cfg.V1Root)
	}

	// v1 db: privacy flags for the post-push visibility pass.
	privacy := map[string]bool{}
	if v1db, err := OpenV1DB(cfg.V1DB); err == nil {
		images, err := v1db.Images()
		v1db.Close()
		if err != nil {
			return err
		}
		for _, img := range images {
			// A name is private if ANY of its manifests were marked private.
			privacy[img.Name] = privacy[img.Name] || img.Private
		}
	} else {
		fmt.Printf("WARN: v1 db unavailable (%v): repo visibility will not be applied\n", err)
	}

	if cfg.DryRun {
		return dryRunImages(cfg, v1s, repos, privacy)
	}
	if cfg.User == "" || cfg.Pass == "" {
		return fmt.Errorf("-v2-user and -v2-pass (or $V2_PASSWORD) are required; use an admin account or df_ token")
	}

	// Suppress webhook dispatch during replay.
	var v2 *V2
	if cfg.V2DB != "" {
		v2, err = OpenV2(cfg.V2DB)
		if err != nil {
			return err
		}
		defer v2.Close()
		restore, n, err := v2.SuppressWebhooks(ctx)
		if err != nil {
			return fmt.Errorf("suppress webhooks: %w", err)
		}
		if n > 0 {
			fmt.Printf("suppressed %d active webhook(s) for the duration of the replay\n", n)
		}
		defer func() {
			if err := restore(); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: failed to restore webhooks, re-enable manually: %v\n", err)
			} else if n > 0 {
				fmt.Printf("restored %d webhook(s)\n", n)
			}
		}()
	} else {
		fmt.Println("WARN: no -v2-db given: active webhooks will fire during replay")
	}

	replayer := NewReplayer(ctx, cfg, v1s)
	results := replayRepos(ctx, cfg, replayer, repos)

	// Post-pass: apply v1 privacy to the auto-created repo rows.
	pushedRepos := map[string]bool{}
	for _, res := range results {
		if res.Status != TagFailed {
			pushedRepos[res.Repo] = true
		}
	}
	if v2 != nil {
		for _, repo := range sortedStringKeys(pushedRepos) {
			ns, repoName := SplitRepoName(MapRepoName(repo, cfg.LegacyNS))
			found, err := v2.SetRepoVisibility(ctx, ns, repoName, privacy[repo])
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARN: visibility %s/%s: %v\n", ns, repoName, err)
			} else if !found {
				fmt.Fprintf(os.Stderr, "WARN: repo row %s/%s missing, visibility not applied\n", ns, repoName)
			}
		}
	} else if anyPrivate(privacy) {
		fmt.Println("WARN: no -v2-db given: private repos were NOT marked private in v2")
	}

	return summarizeResults(results)
}

func replayRepos(ctx context.Context, cfg *config.MigrateConfig, replayer *Replayer, repos []string) []TagResult {
	jobs := cfg.Jobs
	if jobs < 1 {
		jobs = 1
	}
	var (
		mu      sync.Mutex
		results []TagResult
		wg      sync.WaitGroup
	)
	work := make(chan string)
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range work {
				fmt.Printf("replaying %s -> %s\n", repo, MapRepoName(repo, cfg.LegacyNS))
				res, err := replayer.PushRepo(ctx, repo)
				mu.Lock()
				results = append(results, res...)
				if err != nil && ctx.Err() == nil {
					results = append(results, TagResult{Repo: repo, Status: TagFailed, Err: err})
				}
				mu.Unlock()
			}
		}()
	}
	for _, repo := range repos {
		if ctx.Err() != nil {
			break
		}
		work <- repo
	}
	close(work)
	wg.Wait()
	return results
}

func dryRunImages(cfg *config.MigrateConfig, v1s *V1Storage, repos []string, privacy map[string]bool) error {
	fmt.Printf("DRY RUN: %d repositories would be replayed to %s\n\n", len(repos), cfg.Registry)
	broken := 0
	for _, repo := range repos {
		scan := ScanRepo(v1s, repo, cfg.LegacyNS)
		visibility := "public"
		if privacy[repo] {
			visibility = "private"
		}
		fmt.Printf("%-50s -> %-55s %d tag(s), %s\n", repo, scan.Mapped, len(scan.Tags), visibility)
		for _, t := range scan.Tags {
			if issue := t.Issue(); issue != "" {
				fmt.Printf("    !! %s: %s\n", t.Tag, issue)
				broken++
			}
		}
	}
	if broken > 0 {
		fmt.Printf("\n%d tag(s) have problems and would fail to replay\n", broken)
	}
	return nil
}

func summarizeResults(results []TagResult) error {
	var pushed, upToDate, failed int
	for _, r := range results {
		switch r.Status {
		case TagPushed:
			pushed++
		case TagUpToDate:
			upToDate++
		case TagFailed:
			failed++
			target := r.Repo
			if r.Tag != "" {
				target += ":" + r.Tag
			}
			fmt.Fprintf(os.Stderr, "FAILED %s: %v\n", target, r.Err)
		}
	}
	fmt.Printf("\nreplay complete: %d pushed, %d already up-to-date, %d failed\n", pushed, upToDate, failed)
	if failed > 0 {
		return fmt.Errorf("%d tag(s) failed to replay", failed)
	}
	return nil
}

func anyPrivate(m map[string]bool) bool {
	for _, private := range m {
		if private {
			return true
		}
	}
	return false
}
