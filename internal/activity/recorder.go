// Package activity records what people do for the home feed and profile pages
package activity

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Event kinds the feed renders
const (
	KindPush           = "push"
	KindBranch         = "branch"
	KindTag            = "tag"
	KindImagePush      = "image_push"
	KindArtifactUpload = "artifact_upload"
	KindRelease        = "release"
	KindIssueOpened    = "issue_opened"
	KindIssueClosed    = "issue_closed"
	KindIssueReopened  = "issue_reopened"
	KindPullOpened     = "pull_opened"
	KindPullMerged     = "pull_merged"
	KindPullClosed     = "pull_closed"
	KindStar           = "star"
	KindFork           = "fork"
	KindCreated        = "created"
	KindJoined         = "joined"
	KindOrgCreated     = "org_created"
)

// Commits counted per push before the feed says more
const pushCommitCap = 50

// Rows kept, older events are swept on a timer
const keepEvents = 50000

// One thing someone did
type Event struct {
	Actor      *auth.AuthenticatedUser
	Kind       string
	TargetKind string
	TargetID   string
	Namespace  string
	Name       string
	Subject    string
	Summary    string
	Detail     map[string]any
	Public     bool
}

// Writes events off the request path
type Recorder struct {
	store *stores.Store
	git   *gitrepo.Manager
	log   *logger.Logger
	queue chan *db.ActivityEvent
}

func NewRecorder(store *stores.Store, log *logger.Logger) *Recorder {
	return &Recorder{store: store, log: log, queue: make(chan *db.ActivityEvent, 1024)}
}

// Installs the git manager so pushes can read commits
func (r *Recorder) SetGit(m *gitrepo.Manager) { r.git = m }

// Drains the queue until the context ends, prunes daily
func (r *Recorder) Start(ctx context.Context) {
	go func() {
		prune := time.NewTicker(24 * time.Hour)
		defer prune.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-r.queue:
				if err := r.store.CreateActivityEvent(context.Background(), ev); err != nil {
					r.log.Warn("activity: record %s on %s/%s: %v", ev.Kind, ev.Namespace, ev.Name, err)
				}
			case <-prune.C:
				if err := r.store.PruneActivityEvents(context.Background(), keepEvents); err != nil {
					r.log.Warn("activity: prune: %v", err)
				}
			}
		}
	}()
}

// Queues an event, a full queue drops it with a log line
func (r *Recorder) Record(ev Event) {
	if r == nil || ev.Kind == "" {
		return
	}
	row := &db.ActivityEvent{
		ID: uuid.New().String(), Kind: ev.Kind, TargetKind: ev.TargetKind, TargetID: ev.TargetID,
		Namespace: ev.Namespace, Name: ev.Name, Subject: ev.Subject, Summary: truncate(ev.Summary, 500),
		Detail: "{}", IsPublic: ev.Public, CreatedAt: time.Now().UTC(),
	}
	if ev.Actor != nil && !ev.Actor.IsAnonymous() {
		row.ActorID, row.Actor = ev.Actor.ID, ev.Actor.Username
	}
	if len(ev.Detail) > 0 {
		if b, err := json.Marshal(ev.Detail); err == nil {
			row.Detail = string(b)
		}
	}
	select {
	case r.queue <- row:
	default:
		r.log.Warn("activity: queue full, dropped %s on %s/%s", ev.Kind, ev.Namespace, ev.Name)
	}
}

// Git repository events share this shape
func gitEvent(repo *db.GitRepository, actor *auth.AuthenticatedUser, kind string) Event {
	return Event{Actor: actor, Kind: kind, TargetKind: db.ActivityTargetGit, TargetID: repo.ID,
		Namespace: repo.Namespace, Name: repo.Name, Public: !repo.IsPrivate}
}

// GitRepo records a repository level event such as a star, fork, or creation
func (r *Recorder) GitRepo(repo *db.GitRepository, actor *auth.AuthenticatedUser, kind string, detail map[string]any) {
	ev := gitEvent(repo, actor, kind)
	ev.Detail = detail
	r.Record(ev)
}

// Image records an image repository event
func (r *Recorder) Image(repo *db.Repository, actor *auth.AuthenticatedUser, kind, subject string, detail map[string]any) {
	r.Record(Event{Actor: actor, Kind: kind, TargetKind: db.ActivityTargetImage, TargetID: repo.ID,
		Namespace: repo.Namespace, Name: repo.Name, Subject: subject, Detail: detail, Public: !repo.IsPrivate})
}

// Artifact records an artifact repository event
func (r *Recorder) Artifact(repo *db.ArtifactRepository, actor *auth.AuthenticatedUser, kind, subject, summary string) {
	r.Record(Event{Actor: actor, Kind: kind, TargetKind: db.ActivityTargetArtifact, TargetID: strconv.FormatInt(repo.ID, 10),
		Namespace: repo.Namespace, Name: repo.Name, Subject: subject, Summary: summary, Public: !repo.IsPrivate})
}

// Org records an organization event, always public
func (r *Recorder) Org(org *db.Organization, actor *auth.AuthenticatedUser, kind string) {
	r.Record(Event{Actor: actor, Kind: kind, TargetKind: db.ActivityTargetOrg, TargetID: org.ID,
		Namespace: org.Name, Name: org.Name, Public: true})
}

// ImagePushed satisfies the registry listener hook
func (r *Recorder) ImagePushed(ctx context.Context, repo *db.Repository, actor, tag, digest string) {
	if tag == "" {
		return
	}
	var user *auth.AuthenticatedUser
	if actor != "" {
		if u, _ := r.store.GetUserByUsername(ctx, actor); u != nil {
			user = &auth.AuthenticatedUser{ID: u.ID, Username: u.Username}
		}
	}
	r.Image(repo, user, KindImagePush, tag, map[string]any{"digest": digest})
}

// OnRefUpdate turns applied reference changes into push, branch, and tag events
func (r *Recorder) OnRefUpdate(ctx context.Context, ev gitrepo.RefEvent) {
	if ev.Wiki || ev.Mirror || ev.Repo == nil {
		return
	}
	for _, u := range ev.Updates {
		switch {
		case u.Name.IsBranch():
			branch := u.Name.Short()
			switch u.Kind() {
			case "created":
				e := gitEvent(ev.Repo, ev.Actor, KindBranch)
				e.Subject, e.Detail = branch, map[string]any{"action": "created"}
				r.Record(e)
			case "deleted":
				e := gitEvent(ev.Repo, ev.Actor, KindBranch)
				e.Subject, e.Detail = branch, map[string]any{"action": "deleted"}
				r.Record(e)
			default:
				e := gitEvent(ev.Repo, ev.Actor, KindPush)
				e.Subject = branch
				e.Summary, e.Detail = r.describePush(ctx, ev.Repo, u)
				r.Record(e)
			}
		case u.Name.IsTag():
			if u.Kind() == "deleted" {
				continue
			}
			e := gitEvent(ev.Repo, ev.Actor, KindTag)
			e.Subject = u.Name.Short()
			e.Detail = map[string]any{"sha": u.New.String()}
			r.Record(e)
		}
	}
}

// Head commit line plus how many commits arrived
func (r *Recorder) describePush(ctx context.Context, repo *db.GitRepository, u gitrepo.RefUpdate) (string, map[string]any) {
	detail := map[string]any{"before": u.Old.String(), "after": u.New.String(), "forced": u.Forced}
	if r.git == nil {
		return "", detail
	}
	repoObj, err := r.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return "", detail
	}
	defer repoObj.Close()
	summary := ""
	if c, err := repoObj.Commit(u.New); err == nil {
		summary = firstLine(c.Message)
		detail["sha"] = c.Hash.String()
	}
	if !u.Old.IsZero() && !u.Forced {
		commits, more, err := repoObj.CommitsBetween(ctx, u.Old, u.New, pushCommitCap)
		if err == nil {
			detail["commits"] = len(commits)
			detail["more"] = more
		}
	} else if u.Old.IsZero() {
		detail["commits"] = 1
	}
	return summary, detail
}

// ForgeHook adapts forge events, issues, pulls, and releases
func (r *Recorder) ForgeHook() forge.ActivityHook {
	return func(ctx context.Context, fe forge.ActivityEvent) {
		if fe.Repo == nil {
			return
		}
		switch {
		case fe.Release != nil:
			if fe.Action != "published" {
				return
			}
			e := gitEvent(fe.Repo, fe.Actor, KindRelease)
			e.Subject, e.Summary = fe.Release.Tag, fe.Release.Name
			e.Detail = map[string]any{"prerelease": fe.Release.Prerelease}
			r.Record(e)
		case fe.Issue != nil:
			kind := issueKind(fe.Issue.IsPull, fe.Action)
			if kind == "" {
				return
			}
			e := gitEvent(fe.Repo, fe.Actor, kind)
			e.Subject, e.Summary = strconv.FormatInt(fe.Issue.Number, 10), fe.Issue.Title
			r.Record(e)
		}
	}
}

func issueKind(pull bool, action string) string {
	switch action {
	case "opened":
		if pull {
			return KindPullOpened
		}
		return KindIssueOpened
	case "closed":
		if pull {
			return KindPullClosed
		}
		return KindIssueClosed
	case "reopened":
		if pull {
			return ""
		}
		return KindIssueReopened
	case "merged":
		if pull {
			return KindPullMerged
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.TrimSpace(s), 200)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
