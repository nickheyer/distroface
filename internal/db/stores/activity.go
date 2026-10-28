package stores

import (
	"context"

	"github.com/nickheyer/distroface/internal/db"
	"gorm.io/gorm"
)

// Which events a feed query collects
type ActivityListOptions struct {
	// Only events flagged public
	PublicOnly bool
	// Events this user performed
	ActorID string
	// Events under these namespaces, the home feed names the viewer and their orgs
	Namespaces []string
	// Events on targets this user starred or watches, the home feed sets it
	FollowedBy string
	// Events under one namespace
	Namespace string
	// Empty keeps every kind
	Kinds []string
	// Private events need the viewer as actor, the service checks the rest
	ViewerID string
	Limit    int
	Offset   int
}

func (s *Store) CreateActivityEvent(ctx context.Context, ev *db.ActivityEvent) error {
	return s.db.WithContext(ctx).Create(ev).Error
}

// Newest first, the home feed unions the actor, namespaces, and followed targets
func (s *Store) ListActivityEvents(ctx context.Context, opts ActivityListOptions) ([]*db.ActivityEvent, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.ActivityEvent{})
	if opts.PublicOnly {
		q = q.Where("is_public = ?", true)
	} else if opts.ViewerID != "" {
		q = q.Where("is_public = ? OR actor_id = ?", true, opts.ViewerID)
	} else {
		q = q.Where("is_public = ?", true)
	}
	if opts.Namespace != "" {
		q = q.Where("namespace = ?", opts.Namespace)
	}
	if len(opts.Kinds) > 0 {
		q = q.Where("kind IN ?", opts.Kinds)
	}
	if opts.ActorID != "" && opts.FollowedBy == "" && len(opts.Namespaces) == 0 {
		q = q.Where("actor_id = ?", opts.ActorID)
	} else if opts.ActorID != "" || opts.FollowedBy != "" || len(opts.Namespaces) > 0 {
		q = q.Where(s.homeScope(opts))
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q = q.Order("created_at DESC").Order("id DESC")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit).Offset(opts.Offset)
	}
	var rows []*db.ActivityEvent
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Own actions, own namespaces, starred targets, and watched repositories
func (s *Store) homeScope(opts ActivityListOptions) *gorm.DB {
	scope := s.db.Where("1 = 0")
	if opts.ActorID != "" {
		scope = scope.Or("actor_id = ?", opts.ActorID)
	}
	if len(opts.Namespaces) > 0 {
		scope = scope.Or("namespace IN ?", opts.Namespaces)
	}
	if opts.FollowedBy != "" {
		scope = scope.Or("(target_kind || ':' || target_id) IN (SELECT kind || ':' || target_id FROM stars WHERE user_id = ?)", opts.FollowedBy)
		scope = scope.Or("(target_kind = ? AND target_id IN (SELECT repo_id FROM git_watches WHERE user_id = ? AND mode = ?))", db.ActivityTargetGit, opts.FollowedBy, "all")
	}
	return scope
}

// Drops events older than the cutoff
func (s *Store) PruneActivityEvents(ctx context.Context, keep int) error {
	if keep <= 0 {
		return nil
	}
	return s.db.WithContext(ctx).Exec(
		`DELETE FROM activity_events WHERE id IN (SELECT id FROM activity_events ORDER BY created_at DESC LIMIT -1 OFFSET ?)`, keep,
	).Error
}
