package services

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.ReleaseServiceHandler = (*ReleaseService)(nil)

type ReleaseService struct {
	store     *stores.Store
	manager   *gitrepo.Manager
	forge     *forge.Manager
	artifacts *artifacts.Manager
	registry  *registry.RegistryAccess
	log       *logger.Logger
}

func NewReleaseService(store *stores.Store, manager *gitrepo.Manager, fm *forge.Manager, am *artifacts.Manager, reg *registry.RegistryAccess, log *logger.Logger) *ReleaseService {
	return &ReleaseService{store: store, manager: manager, forge: fm, artifacts: am, registry: reg, log: log}
}

// Loads a repository with the releases unit at the needed level and the actor
func (s *ReleaseService) repo(ctx context.Context, namespace, name string, need gitrepo.Level) (*auth.AuthenticatedUser, *storage.GitRepository, forge.Actor, error) {
	user := auth.UserFromContext(ctx)
	if need > gitrepo.LevelRead {
		if err := portalAllowsGitWrite(ctx); err != nil {
			return nil, nil, forge.Actor{}, err
		}
	}
	repo, err := gitRepoWithLevel(ctx, s.store, s.manager.Access(), user, namespace, name, storage.GitUnitReleases, need)
	if err != nil {
		return nil, nil, forge.Actor{}, err
	}
	levels := s.manager.Access().Levels(ctx, user, repo)
	actor := forge.Actor{User: user, CanWrite: levels.Unit(storage.GitUnitReleases) >= gitrepo.LevelWrite, CanAdmin: levels.CanAdmin || levels.IsOwner}
	return user, repo, actor, nil
}

func (s *ReleaseService) release(ctx context.Context, namespace, name, tag string, need gitrepo.Level) (*auth.AuthenticatedUser, *storage.GitRepository, forge.Actor, *storage.GitRelease, error) {
	user, repo, actor, err := s.repo(ctx, namespace, name, need)
	if err != nil {
		return nil, nil, forge.Actor{}, nil, err
	}
	rel, err := s.forge.Release(ctx, repo, tag)
	if err != nil {
		return nil, nil, forge.Actor{}, nil, mapForgeErr(err)
	}
	if rel.Draft && !actor.CanWrite {
		return nil, nil, forge.Actor{}, nil, connect.Errorf(connect.CodeNotFound, "release %s not found", tag)
	}
	return user, repo, actor, rel, nil
}

func (s *ReleaseService) assetProto(ctx context.Context, repo *storage.GitRepository, pkg *storage.ArtifactRepository, a *storage.Artifact) *v1.ReleaseAsset {
	base := s.manager.BaseURL(ctx)
	return &v1.ReleaseAsset{
		ArtifactId:  a.ID,
		Name:        a.Path,
		Size:        a.Size,
		Digest:      a.Digest,
		ContentType: a.MimeType,
		DownloadUrl: fmt.Sprintf("%s/api/v1/artifacts/_ns/%s/%s/%s/%s", base, pkg.Namespace, pkg.Name, a.Version, a.Path),
		ArtifactUrl: fmt.Sprintf("%s/artifacts/%s/%s?version=%s", base, pkg.Namespace, pkg.Name, a.Version),
		CreatedAt:   timestamppb.New(a.CreatedAt),
	}
}

// Images of a release, tag matches on linked repos plus pinned references
func (s *ReleaseService) images(ctx context.Context, repo *storage.GitRepository, rel *storage.GitRelease) []*v1.ReleaseImage {
	base := s.manager.BaseURL(ctx)
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	seen := map[string]bool{}
	var out []*v1.ReleaseImage
	add := func(img *storage.Repository, tag, digest string, pinned bool) {
		key := img.ID + ":" + tag
		if seen[key] {
			return
		}
		seen[key] = true
		p := &v1.ReleaseImage{
			Namespace: img.Namespace, Name: img.Name, FullName: img.Namespace + "/" + img.Name, Tag: tag, Digest: digest, Pinned: pinned,
			PullCommand: fmt.Sprintf("docker pull %s/%s/%s:%s", host, img.Namespace, img.Name, tag),
			ImageUrl:    fmt.Sprintf("%s/images/%s/%s", base, img.Namespace, img.Name),
		}
		if s.registry != nil {
			if desc, err := s.registry.ResolveTag(ctx, img.Namespace, img.Name, tag); err == nil && desc != nil {
				if p.Digest == "" {
					p.Digest = desc.Digest
				}
				p.SizeBytes = desc.SizeBytes
				if desc.Platform != nil {
					p.Platforms = append(p.Platforms, desc.Platform)
				}
			}
		}
		out = append(out, p)
	}
	if pinned, err := s.store.ListGitReleaseImages(ctx, rel.ID); err == nil {
		for _, pin := range pinned {
			if img, _ := s.store.GetRepositoryByID(ctx, pin.ImageRepoID); img != nil {
				add(img, pin.Tag, pin.Digest, true)
			}
		}
	}
	if linked, err := s.store.ListRepositoriesByGitRepo(ctx, repo.ID); err == nil && s.registry != nil {
		for _, img := range linked {
			if desc, err := s.registry.ResolveTag(ctx, img.Namespace, img.Name, rel.Tag); err == nil && desc != nil {
				add(img, rel.Tag, desc.Digest, false)
			}
		}
	}
	return out
}

func (s *ReleaseService) toProto(ctx context.Context, repo *storage.GitRepository, rel *storage.GitRelease) *v1.Release {
	users := newGitUserCache(s.store)
	html := s.manager.HTMLURL(ctx, repo)
	out := &v1.Release{
		Id: rel.ID, Tag: rel.Tag, Name: rel.Name, Body: rel.Body, Draft: rel.Draft, Prerelease: rel.Prerelease,
		Author: users.username(ctx, rel.AuthorID), TargetSha: rel.TargetSHA, HtmlUrl: html + "/releases/" + rel.Tag,
		ZipUrl: html + "/-/archive/" + rel.Tag + ".zip", TarUrl: html + "/-/archive/" + rel.Tag + ".tar.gz",
		CreatedAt: timestamppb.New(rel.CreatedAt), UpdatedAt: timestamppb.New(rel.UpdatedAt),
	}
	if rel.PublishedAt != nil {
		out.PublishedAt = timestamppb.New(*rel.PublishedAt)
	}
	if r, err := s.manager.Engine().OpenRepo(repo.Namespace, repo.Name, false); err == nil {
		if c, err := r.Commit(plumbing.NewHash(rel.TargetSHA)); err == nil {
			out.Commit = newGitCommitEnricher(ctx, s.store, s.manager, repo, r).commit(c, false, false)
		}
		r.Close()
	}
	if assets, pkg, err := s.forge.ReleaseAssets(ctx, repo, rel); err == nil && pkg != nil {
		for _, a := range assets {
			out.Assets = append(out.Assets, s.assetProto(ctx, repo, pkg, a))
		}
	}
	out.Images = s.images(ctx, repo, rel)
	return out
}

func releaseInput(title, body *string, draft, prerelease *bool) forge.ReleaseInput {
	return forge.ReleaseInput{Name: title, Body: body, Draft: draft, Prerelease: prerelease}
}

func (s *ReleaseService) CreateRelease(ctx context.Context, req *v1.CreateReleaseRequest) (*v1.CreateReleaseResponse, error) {
	_, repo, actor, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	in := releaseInput(&req.Title, &req.Body, &req.Draft, &req.Prerelease)
	in.Tag, in.Target = req.Tag, req.Target
	rel, err := s.forge.CreateRelease(ctx, repo, actor, in)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.CreateReleaseResponse{Release: s.toProto(ctx, repo, rel)}, nil
}

func (s *ReleaseService) GetRelease(ctx context.Context, req *v1.GetReleaseRequest) (*v1.GetReleaseResponse, error) {
	_, repo, _, rel, err := s.release(ctx, req.Namespace, req.Name, req.Tag, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	return &v1.GetReleaseResponse{Release: s.toProto(ctx, repo, rel)}, nil
}

func (s *ReleaseService) ListReleases(ctx context.Context, req *v1.ListReleasesRequest) (*v1.ListReleasesResponse, error) {
	_, repo, actor, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	q := pages.ParseQuery(req.Page)
	if err := stores.ReleasesQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	rows, total, err := s.store.ListGitReleases(ctx, repo.ID, req.IncludeDrafts && actor.CanWrite, req.IncludePrereleases, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.ListReleasesResponse{Page: pages.Info(offset, limit, total)}
	for _, rel := range rows {
		out.Releases = append(out.Releases, s.toProto(ctx, repo, rel))
	}
	return out, nil
}

func (s *ReleaseService) UpdateRelease(ctx context.Context, req *v1.UpdateReleaseRequest) (*v1.UpdateReleaseResponse, error) {
	_, repo, actor, rel, err := s.release(ctx, req.Namespace, req.Name, req.Tag, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.forge.UpdateRelease(ctx, repo, actor, rel, releaseInput(req.Title, req.Body, req.Draft, req.Prerelease)); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.UpdateReleaseResponse{Release: s.toProto(ctx, repo, rel)}, nil
}

func (s *ReleaseService) DeleteRelease(ctx context.Context, req *v1.DeleteReleaseRequest) (*v1.DeleteReleaseResponse, error) {
	_, repo, actor, rel, err := s.release(ctx, req.Namespace, req.Name, req.Tag, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.forge.DeleteRelease(ctx, repo, actor, rel, req.DeleteTag); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DeleteReleaseResponse{}, nil
}

func (s *ReleaseService) PublishRelease(ctx context.Context, req *v1.PublishReleaseRequest) (*v1.PublishReleaseResponse, error) {
	_, repo, actor, rel, err := s.release(ctx, req.Namespace, req.Name, req.Tag, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.forge.PublishRelease(ctx, repo, actor, rel); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.PublishReleaseResponse{Release: s.toProto(ctx, repo, rel)}, nil
}

func (s *ReleaseService) AttachReleaseAsset(ctx context.Context, req *v1.AttachReleaseAssetRequest) (*v1.AttachReleaseAssetResponse, error) {
	_, repo, actor, rel, err := s.release(ctx, req.Namespace, req.Name, req.Tag, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	art, err := s.forge.AttachReleaseAsset(ctx, repo, actor, rel, req.ArtifactId)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	pkg, _ := s.forge.ReleasePackage(ctx, repo, false)
	return &v1.AttachReleaseAssetResponse{Asset: s.assetProto(ctx, repo, pkg, art)}, nil
}

func (s *ReleaseService) DetachReleaseAsset(ctx context.Context, req *v1.DetachReleaseAssetRequest) (*v1.DetachReleaseAssetResponse, error) {
	_, repo, actor, rel, err := s.release(ctx, req.Namespace, req.Name, req.Tag, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.forge.DetachReleaseAsset(ctx, repo, actor, rel, req.ArtifactId); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DetachReleaseAssetResponse{}, nil
}

func (s *ReleaseService) AttachReleaseImage(ctx context.Context, req *v1.AttachReleaseImageRequest) (*v1.AttachReleaseImageResponse, error) {
	_, repo, actor, rel, err := s.release(ctx, req.Namespace, req.Name, req.Tag, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	digest := ""
	if s.registry != nil {
		desc, err := s.registry.ResolveTag(ctx, repo.Namespace, req.ImageName, req.ImageTag)
		if err != nil || desc == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "image %s/%s:%s not found", repo.Namespace, req.ImageName, req.ImageTag)
		}
		digest = desc.Digest
	}
	if _, err := s.forge.AttachReleaseImage(ctx, repo, actor, rel, req.ImageName, req.ImageTag, digest); err != nil {
		return nil, mapForgeErr(err)
	}
	for _, img := range s.images(ctx, repo, rel) {
		if img.Name == req.ImageName && img.Tag == req.ImageTag {
			return &v1.AttachReleaseImageResponse{Image: img}, nil
		}
	}
	return &v1.AttachReleaseImageResponse{}, nil
}

func (s *ReleaseService) DetachReleaseImage(ctx context.Context, req *v1.DetachReleaseImageRequest) (*v1.DetachReleaseImageResponse, error) {
	_, repo, actor, rel, err := s.release(ctx, req.Namespace, req.Name, req.Tag, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.forge.DetachReleaseImage(ctx, repo, actor, rel, req.ImageName, req.ImageTag); err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.DetachReleaseImageResponse{}, nil
}

func (s *ReleaseService) GetLatestRelease(ctx context.Context, req *v1.GetLatestReleaseRequest) (*v1.GetLatestReleaseResponse, error) {
	_, repo, _, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	rel, err := s.store.LatestGitRelease(ctx, repo.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if rel == nil {
		return &v1.GetLatestReleaseResponse{}, nil
	}
	return &v1.GetLatestReleaseResponse{Release: s.toProto(ctx, repo, rel)}, nil
}

func (s *ReleaseService) GenerateReleaseNotes(ctx context.Context, req *v1.GenerateReleaseNotesRequest) (*v1.GenerateReleaseNotesResponse, error) {
	_, repo, _, err := s.repo(ctx, req.Namespace, req.Name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	title, body, err := s.forge.GenerateReleaseNotes(ctx, repo, req.Tag, req.Target, req.PreviousTag)
	if err != nil {
		return nil, mapForgeErr(err)
	}
	return &v1.GenerateReleaseNotesResponse{Title: title, Body: body}, nil
}
