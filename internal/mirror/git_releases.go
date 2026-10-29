package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

// Opens one upstream asset, trying the preferred sources first
func (m *Monitor) openAsset(ctx context.Context, a asset) (io.ReadCloser, string, error) {
	var lastErr error
	for _, src := range a.sources {
		body, err := m.openAssetSource(ctx, src)
		if err == nil {
			return body, src.url, nil
		}
		if _, limited := RetryAfter(err); limited {
			return nil, "", err
		}
		lastErr = err
		// Auth shaped failures fall through to the next source
		var ue *upstreamError
		if errors.As(err, &ue) {
			switch ue.status {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
				continue
			}
		}
		return nil, "", err
	}
	if lastErr == nil {
		return nil, "", fmt.Errorf("no download source for %s", a.name)
	}
	return nil, "", lastErr
}

func (m *Monitor) openAssetSource(ctx context.Context, src assetSource) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range src.headers {
		req.Header.Set(k, v)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	if err := classifyResponse(resp, src.url); err != nil {
		resp.Body.Close()
		return nil, err
	}
	// Sign in walls answer anonymous downloads with a 200 html page
	if p := resp.Request.URL.Path; strings.HasSuffix(p, "/user/login") || strings.HasSuffix(p, "/users/sign_in") {
		resp.Body.Close()
		return nil, fmt.Errorf("%s redirected to the sign in page, the upstream did not accept the token for downloads", src.url)
	}
	return resp.Body, nil
}

// Asset lists of every upstream release keyed by tag
func (m *Monitor) upstreamReleaseAssets(ctx context.Context, up gitUpstream, token string) (map[string][]asset, error) {
	drv := driverFor(releaseTypeFor(up.Forge))
	if drv == nil {
		return nil, fmt.Errorf("%s has no release api", up.Host)
	}
	cfg := &v1.MirrorConfig{Upstream: up.releaseUpstream(), IncludePrereleases: true}
	if token != "" {
		cfg.AuthToken = proto.String(token)
	}
	list, err := drv.releases(ctx, m.client, cfg, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]asset, len(list.releases))
	for _, rel := range list.releases {
		out[rel.version] = rel.assets
	}
	return out, nil
}

// Copies upstream files onto a release, skipping present ones
func (m *Monitor) syncReleaseAssets(ctx context.Context, repo *db.GitRepository, rel *db.GitRelease, items []asset, prune bool, note func(string, ...any)) (int, error) {
	existing, err := m.store.ListGitReleaseAssets(ctx, rel.ID)
	if err != nil {
		return 0, err
	}
	present := map[string]*db.GitReleaseAsset{}
	for _, a := range existing {
		present[a.Name] = a
	}
	maxBytes := m.git.MaxReleaseAssetBytes(ctx, repo)
	copied := 0
	wanted := map[string]bool{}
	for _, a := range items {
		wanted[a.name] = true
		if err := gitrepo.ValidateAssetName(a.name); err != nil {
			note("asset %s of %s skipped, unusable name", a.name, rel.Tag)
			continue
		}
		if cur := present[a.name]; cur != nil && (a.size == 0 || cur.Size == a.size) {
			continue
		}
		if maxBytes > 0 && a.size > maxBytes {
			note("asset %s of %s skipped, over the size limit", a.name, rel.Tag)
			continue
		}
		body, src, err := m.openAsset(ctx, a)
		if err != nil {
			if _, limited := RetryAfter(err); limited {
				return copied, err
			}
			note("asset %s of %s failed, %v", a.name, rel.Tag, err)
			continue
		}
		_, err = m.git.StoreReleaseAsset(ctx, repo, rel, gitrepo.ReleaseAssetInput{Name: a.name, Body: body, Replace: true})
		body.Close()
		if err != nil {
			note("asset %s of %s from %s failed, %v", a.name, rel.Tag, src, err)
			continue
		}
		copied++
	}
	if prune {
		for name, a := range present {
			if wanted[name] {
				continue
			}
			if err := m.git.DeleteReleaseAsset(ctx, a); err != nil {
				return copied, err
			}
		}
	}
	return copied, nil
}

// Mirrors upstream releases and their files onto a git mirror repository
func (m *Monitor) syncGitReleases(ctx context.Context, repo *db.GitRepository, cfg *v1.MirrorConfig, up gitUpstream) error {
	if up.Forge == "" {
		forge, err := m.detectForge(ctx, up)
		if err != nil {
			return fmt.Errorf("releases: cannot tell which forge hosts %s: %w", up.Host, err)
		}
		up.Forge = forge
	}
	drv := forgeDriverFor(up.Forge)
	if drv == nil {
		return fmt.Errorf("releases: %s has no release api", up.Host)
	}
	assets, err := m.upstreamReleaseAssets(ctx, up, cfg.GetAuthToken())
	if err != nil {
		return fmt.Errorf("releases: %w", err)
	}
	f := forgeCtx{client: m.client, up: up, token: cfg.GetAuthToken()}
	var notes []string
	note := func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) }
	seen := map[string]bool{}
	copied := 0
	for page := 1; page <= maxReleasePages; page++ {
		items, more, err := drv.releases(ctx, f, page)
		if err != nil {
			return fmt.Errorf("releases: %w", err)
		}
		for _, rel := range items {
			if rel.Tag == "" {
				continue
			}
			seen[rel.Tag] = true
			row, err := m.upsertMirroredRelease(ctx, repo, rel)
			if err != nil {
				return err
			}
			n, err := m.syncReleaseAssets(ctx, repo, row, assets[rel.Tag], true, note)
			copied += n
			if err != nil {
				return err
			}
		}
		if !more {
			break
		}
	}
	// Releases the upstream dropped go too, the mirror has no local authors
	rows, _, err := m.store.ListGitReleases(ctx, repo.ID, true, true, pages.Query{}, 0, 0)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if seen[row.Tag] {
			continue
		}
		if err := m.git.DeleteReleaseAssets(ctx, row); err != nil {
			return err
		}
		if err := m.store.DeleteGitRelease(ctx, row.ID); err != nil {
			return err
		}
	}
	if copied > 0 {
		m.log.Info("mirror copied %d release assets onto %s/%s from %s", copied, repo.Namespace, repo.Name, cfg.GetUpstream())
	}
	if len(notes) > 0 {
		return fmt.Errorf("releases synced with notes: %s", strings.Join(notes, "; "))
	}
	return nil
}

// Creates or refreshes the local row of an upstream release
func (m *Monitor) upsertMirroredRelease(ctx context.Context, repo *db.GitRepository, rel forgeRelease) (*db.GitRelease, error) {
	existing, err := m.store.GetGitRelease(ctx, repo.ID, rel.Tag)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		row := releaseRow(repo.ID, rel, "", m.tagCommit(repo, rel.Tag, rel.TargetSHA))
		if row.AuthorID == "" {
			row.AuthorID = repo.OwnerID
		}
		if err := m.store.CreateGitRelease(ctx, row); err != nil {
			return nil, err
		}
		return row, nil
	}
	name := rel.Name
	if name == "" {
		name = rel.Tag
	}
	target := m.tagCommit(repo, rel.Tag, rel.TargetSHA)
	if existing.Name == name && existing.Body == rel.Body && existing.Draft == rel.Draft && existing.Prerelease == rel.Prerelease && (target == "" || existing.TargetSHA == target) {
		return existing, nil
	}
	existing.Name, existing.Body, existing.Draft, existing.Prerelease = name, rel.Body, rel.Draft, rel.Prerelease
	if target != "" {
		existing.TargetSHA = target
	}
	if rel.PublishedAt != nil {
		existing.PublishedAt = rel.PublishedAt
	}
	if err := m.store.UpdateGitRelease(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// Turns on release mirroring for a git mirror repository
func (m *Monitor) EnableReleaseMirroring(ctx context.Context, repo *db.GitRepository) error {
	cfg, err := m.Config(repo.MirrorConfig)
	if err != nil {
		return err
	}
	if cfg.GetMirrorReleases() {
		return nil
	}
	cfg.MirrorReleases = true
	raw, err := EncodeConfig(cfg)
	if err != nil {
		return err
	}
	if err := m.store.UpdateGitRepositoryColumns(ctx, repo.ID, map[string]any{"mirror_config": raw}); err != nil {
		return err
	}
	repo.MirrorConfig = raw
	return nil
}

var adoptOnce sync.Once

// Legacy release packages fold into release mirroring
func (m *Monitor) adoptReleasePackages(ctx context.Context) {
	adoptOnce.Do(func() {
		if m.git == nil {
			return
		}
		pkgs, err := m.store.ListMirrorArtifactRepositories(ctx, MirrorArtifactTypes)
		if err != nil {
			m.log.Error("release package adoption: %v", err)
			return
		}
		for _, pkg := range pkgs {
			if pkg.GitRepoID == nil || *pkg.GitRepoID == "" {
				continue
			}
			repo, err := m.store.GetGitRepositoryByID(ctx, *pkg.GitRepoID)
			if err != nil || repo == nil || repo.Type != v1.GitRepoType_GIT_REPO_TYPE_MIRROR {
				continue
			}
			if err := m.EnableReleaseMirroring(ctx, repo); err != nil {
				m.log.Error("release package adoption for %s/%s: %v", repo.Namespace, repo.Name, err)
				continue
			}
			if err := m.artifacts.DeleteRepository(ctx, pkg); err != nil {
				m.log.Error("release package adoption for %s/%s: %v", repo.Namespace, repo.Name, err)
				continue
			}
			m.log.Info("release package %s/%s folded into release mirroring of its git repository", pkg.Namespace, pkg.Name)
		}
	})
}
