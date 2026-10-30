package services

import (
	"context"
	"strconv"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.ActivityServiceHandler = (*ActivityService)(nil)

// Private events read in one page before the service stops filling it
const activityFillRounds = 4

type ActivityService struct {
	store     *stores.Store
	git       *gitrepo.Manager
	images    *auth.RepoAccess
	artifacts *artifacts.Access
	enforcer  *rbac.Enforcer
	log       *logger.Logger
}

func NewActivityService(store *stores.Store, git *gitrepo.Manager, enforcer *rbac.Enforcer, log *logger.Logger) *ActivityService {
	images := auth.NewRepoAccess(store, enforcer)
	arts := artifacts.NewAccess(store, enforcer)
	if git != nil {
		images.SetLinkedAccess(git.Access())
		arts.SetLinkedAccess(git.Access())
	}
	return &ActivityService{store: store, git: git, images: images, artifacts: arts, enforcer: enforcer, log: log}
}

func (s *ActivityService) ListActivity(ctx context.Context, req *v1.ListActivityRequest) (*v1.ListActivityResponse, error) {
	user := auth.UserFromContext(ctx)
	signedIn := user != nil && !user.IsAnonymous()
	limit, offset := pages.Parse(req.Page)
	opts := stores.ActivityListOptions{Kinds: req.Kinds, Limit: limit, Offset: offset}
	if signedIn {
		opts.ViewerID = user.ID
	}

	feed := req.Feed
	if !signedIn && (feed == v1.ActivityFeed_ACTIVITY_FEED_HOME || feed == v1.ActivityFeed_ACTIVITY_FEED_MINE) {
		feed = v1.ActivityFeed_ACTIVITY_FEED_PUBLIC
	}
	switch feed {
	case v1.ActivityFeed_ACTIVITY_FEED_HOME:
		names, err := s.store.ListUserOrgNames(ctx, user.ID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		opts.ActorID = user.ID
		opts.Namespaces = append([]string{user.Username}, names...)
		opts.FollowedBy = user.ID
	case v1.ActivityFeed_ACTIVITY_FEED_MINE:
		opts.ActorID = user.ID
	case v1.ActivityFeed_ACTIVITY_FEED_ACTOR:
		if req.Actor == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "actor is required")
		}
		target, err := s.store.GetUserByUsername(ctx, req.Actor)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if target == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "user not found")
		}
		opts.ActorID = target.ID
		opts.PublicOnly = !signedIn || target.ID != user.ID
	case v1.ActivityFeed_ACTIVITY_FEED_NAMESPACE:
		if req.Namespace == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "namespace is required")
		}
		opts.Namespace = req.Namespace
		opts.PublicOnly = !signedIn
	default:
		opts.PublicOnly = true
	}
	// Portal hosts only ever see their own organization
	if p := portal.FromContext(ctx); p != nil {
		opts.Namespace = p.OrgName
		opts.Namespaces = nil
		opts.FollowedBy = ""
	}

	// Private rows need a visibility check, keep reading until the page fills
	var kept []*storage.ActivityEvent
	var total int64
	seen := newVisibilityCache()
	for round := 0; round < activityFillRounds && len(kept) < limit; round++ {
		rows, n, err := s.store.ListActivityEvents(ctx, opts)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if round == 0 {
			total = n
		}
		for _, row := range rows {
			if row.IsPublic || s.visible(ctx, user, row, seen) {
				kept = append(kept, row)
			}
		}
		if len(rows) < opts.Limit {
			break
		}
		opts.Offset += len(rows)
	}
	if len(kept) > limit {
		kept = kept[:limit]
	}
	return &v1.ListActivityResponse{
		Events: s.toProto(ctx, kept),
		Page:   pages.Info(offset, limit, total),
	}, nil
}

// Per request memo of target visibility
type visibilityCache struct{ seen map[string]bool }

func newVisibilityCache() *visibilityCache { return &visibilityCache{seen: map[string]bool{}} }

// Whether the viewer may see the target behind a private event
func (s *ActivityService) visible(ctx context.Context, user *auth.AuthenticatedUser, row *storage.ActivityEvent, cache *visibilityCache) bool {
	if user != nil && row.ActorID != "" && row.ActorID == user.ID {
		return true
	}
	key := row.TargetKind + ":" + row.TargetID
	if ok, hit := cache.seen[key]; hit {
		return ok
	}
	ok := false
	switch row.TargetKind {
	case storage.ActivityTargetGit:
		if s.git != nil {
			if repo, _ := s.store.GetGitRepositoryByID(ctx, row.TargetID); repo != nil {
				ok = s.git.Access().CanSee(ctx, user, repo)
			}
		}
	case storage.ActivityTargetImage:
		if repo, _ := s.store.GetRepositoryByID(ctx, row.TargetID); repo != nil {
			ok = s.images.CanSee(ctx, user, repo)
		}
	case storage.ActivityTargetArtifact:
		if id, err := strconv.ParseInt(row.TargetID, 10, 64); err == nil {
			if repo, _ := s.store.GetArtifactRepositoryByID(ctx, id); repo != nil {
				ok = s.artifacts.CanSee(ctx, user, repo)
			}
		}
	case storage.ActivityTargetOrg:
		ok = true
	}
	cache.seen[key] = ok
	return ok
}

func (s *ActivityService) toProto(ctx context.Context, rows []*storage.ActivityEvent) []*v1.ActivityEvent {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ActorID != "" {
			ids = append(ids, r.ActorID)
		}
	}
	users := map[string]*storage.User{}
	if len(ids) > 0 {
		if found, err := s.store.GetUsersByIDs(ctx, ids); err == nil {
			users = found
		}
	}
	out := make([]*v1.ActivityEvent, len(rows))
	for i, r := range rows {
		ev := &v1.ActivityEvent{
			Id: r.ID, Kind: r.Kind, Actor: r.Actor, TargetKind: activityTargetKind(r.TargetKind),
			Namespace: r.Namespace, Name: r.Name, Subject: r.Subject, Summary: r.Summary, Detail: r.Detail,
			IsPublic: r.IsPublic, CreatedAt: timestamppb.New(r.CreatedAt),
		}
		if u := users[r.ActorID]; u != nil {
			ev.Actor = u.Username
			ev.ActorDisplayName = u.DisplayName
			ev.ActorAvatarUrl = u.AvatarURL
			ev.ActorIsAgent = isAgentUser(u)
		}
		out[i] = ev
	}
	return out
}

func activityTargetKind(kind string) v1.ActivityTargetKind {
	switch kind {
	case storage.ActivityTargetGit:
		return v1.ActivityTargetKind_ACTIVITY_TARGET_KIND_GIT
	case storage.ActivityTargetImage:
		return v1.ActivityTargetKind_ACTIVITY_TARGET_KIND_IMAGE
	case storage.ActivityTargetArtifact:
		return v1.ActivityTargetKind_ACTIVITY_TARGET_KIND_ARTIFACT
	case storage.ActivityTargetOrg:
		return v1.ActivityTargetKind_ACTIVITY_TARGET_KIND_ORGANIZATION
	}
	return v1.ActivityTargetKind_ACTIVITY_TARGET_KIND_UNSPECIFIED
}
