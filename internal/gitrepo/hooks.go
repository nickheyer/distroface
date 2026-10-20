package gitrepo

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/protocol/packp"
	"github.com/go-git/go-git/v6/plumbing/storer"
	"github.com/go-git/go-git/v6/plumbing/transport"
	"github.com/go-git/go-git/v6/storage"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// One applied reference change
type RefUpdate struct {
	Name   plumbing.ReferenceName
	Old    plumbing.Hash
	New    plumbing.Hash
	Forced bool
}

// Kind of a reference change
func (u RefUpdate) Kind() string {
	switch {
	case u.Old.IsZero():
		return "created"
	case u.New.IsZero():
		return "deleted"
	case u.Forced:
		return "forced"
	default:
		return "updated"
	}
}

// Everything a post receive subscriber needs about a push
type RefEvent struct {
	Repo        *db.GitRepository
	Actor       *auth.AuthenticatedUser
	Updates     []RefUpdate
	PushOptions []string
	Wiki        bool
	Mirror      bool
	SkipCI      bool
}

// Receives applied reference changes after every push and web commit
type RefSubscriber func(ctx context.Context, ev RefEvent)

// Flags that relax pre receive policy for server initiated writes
type HookOptions struct {
	Wiki                 bool
	Mirror               bool
	BypassPullRequest    bool
	BypassSignedCommits  bool
	BypassAllowedPushers bool
	// Deleting the old name of a renamed branch, its content lives on
	Rename bool
}

// Hooks bound to one repository and its held write lock
func (m *Manager) Hooks(repo *db.GitRepository, actor *auth.AuthenticatedUser, opts HookOptions, lock *RepoLock) transport.ReceivePackHooks {
	return transport.ReceivePackHooks{
		PreReceive: func(hctx context.Context, info *transport.PreReceiveInfo) error {
			return m.preReceive(hctx, repo, actor, opts, info)
		},
		PostReceive: func(hctx context.Context, info *transport.PostReceiveInfo) error {
			updates := make([]RefUpdate, 0, len(info.Commands))
			for _, c := range info.Commands {
				u := RefUpdate{Name: c.Name, Old: c.Old, New: c.New}
				if !c.Old.IsZero() && !c.New.IsZero() {
					if ok, err := isAncestorIn(info.Storer, c.Old, c.New); err == nil && !ok {
						u.Forced = true
					}
				}
				updates = append(updates, u)
			}
			m.AfterRefUpdate(context.WithoutCancel(hctx), RefEvent{
				Repo: repo, Actor: actor, Updates: updates, PushOptions: info.PushOptions,
				Wiki: opts.Wiki, Mirror: opts.Mirror, SkipCI: hasSkipCI(info.PushOptions),
			}, lock)
			m.pullHint(hctx, repo, actor, opts, updates, info.Progress)
			return nil
		},
	}
}

// Prints the create pull request hint after a push to a topic branch
func (m *Manager) pullHint(ctx context.Context, repo *db.GitRepository, actor *auth.AuthenticatedUser, opts HookOptions, updates []RefUpdate, w io.Writer) {
	if w == nil || opts.Wiki || opts.Mirror || actor == nil || repo.IsArchived || !UnitEnabled(repo, db.GitUnitPulls) {
		return
	}
	for _, u := range updates {
		if !u.Name.IsBranch() || u.New.IsZero() {
			continue
		}
		branch := u.Name.Short()
		if branch == repo.DefaultBranch {
			continue
		}
		if open, err := m.store.ListOpenPullsByHead(ctx, repo.ID, branch); err != nil || len(open) > 0 {
			continue
		}
		fmt.Fprintf(w, "\nCreate a pull request for '%s' at:\n  %s/pulls/new?head=%s\n\n", branch, m.HTMLURL(ctx, repo), url.QueryEscape(branch))
	}
}

func hasSkipCI(options []string) bool {
	for _, o := range options {
		if o == "skip-ci" || o == "ci.skip" {
			return true
		}
	}
	return false
}

// Pending import row with no error, the importer is busy
func (m *Manager) importInProgress(ctx context.Context, repo *db.GitRepository) bool {
	imp, err := m.store.GetGitImport(ctx, repo.ID)
	return err == nil && imp != nil && imp.Phase != "done" && imp.LastError == ""
}

// Policy gate run with the new objects already unpacked
func (m *Manager) preReceive(ctx context.Context, repo *db.GitRepository, actor *auth.AuthenticatedUser, opts HookOptions, info *transport.PreReceiveInfo) error {
	say := func(format string, args ...any) {
		fmt.Fprintf(info.Progress, "remote: "+format+"\n", args...)
	}
	if repo.IsArchived {
		say("repository is archived and read only")
		return fmt.Errorf("repository is archived")
	}
	if repo.Type == v1.GitRepoType_GIT_REPO_TYPE_MIRROR && !opts.Mirror {
		say("repository is a mirror, push to the upstream instead")
		return fmt.Errorf("repository is a mirror")
	}
	if !opts.Mirror && !opts.Wiki && m.importInProgress(ctx, repo) {
		say("repository import is still running, push again once it finishes")
		return ErrImporting
	}
	for _, c := range info.Commands {
		name := c.Name.String()
		switch {
		case strings.HasPrefix(name, "refs/pull/"), strings.HasPrefix(name, "refs/distroface/"):
			say("%s is owned by the server", name)
			return fmt.Errorf("refusing to update server owned ref %s", name)
		case !c.Name.IsBranch() && !c.Name.IsTag() && !c.Name.IsNote():
			say("%s is outside refs/heads, refs/tags, and refs/notes", name)
			return fmt.Errorf("refusing to update %s", name)
		}
	}
	if opts.Wiki {
		return nil
	}

	// Quota against the objects already on disk
	settings := m.gitSettings(ctx, repo)
	if maxMB := settings.GetMaxRepoSizeMb(); maxMB > 0 {
		size, err := m.engine.Size(repo.Namespace, repo.Name, false)
		if err == nil && size > maxMB*1024*1024 {
			say("repository exceeds its %d MB quota", maxMB)
			return fmt.Errorf("repository size quota exceeded")
		}
	}

	protections, err := m.store.ListGitBranchProtections(ctx, repo.ID)
	if err != nil {
		return err
	}
	tagRules, err := m.store.ListGitTagProtections(ctx, repo.ID)
	if err != nil {
		return err
	}
	for _, c := range info.Commands {
		if c.Name.IsBranch() {
			branch := c.Name.Short()
			if c.New.IsZero() && branch == repo.DefaultBranch {
				say("cannot delete the default branch %s", branch)
				return fmt.Errorf("default branch cannot be deleted")
			}
			rule := MatchProtection(protections, branch)
			if rule == nil {
				continue
			}
			if err := m.checkBranchRule(ctx, repo, actor, opts, info.Storer, rule, c, say); err != nil {
				return err
			}
		}
		if c.Name.IsTag() {
			tag := c.Name.Short()
			for _, rule := range tagRules {
				if !globMatch(rule.Pattern, tag) {
					continue
				}
				if c.New.IsZero() && rule.BlockDeletion {
					say("tag %s is protected from deletion", tag)
					return fmt.Errorf("protected tag")
				}
				if pushers := DecodeIDs(rule.AllowedPushers); len(pushers) > 0 && !opts.BypassAllowedPushers && !m.access.InAllowList(ctx, actor, repo, pushers) {
					say("you are not allowed to push tag %s", tag)
					return fmt.Errorf("protected tag")
				}
			}
		}
	}
	return nil
}

// New commits one push may carry past a signed or linear history rule
const maxVerifiedCommits = 10000

// Enforces one branch protection rule for one command
func (m *Manager) checkBranchRule(ctx context.Context, repo *db.GitRepository, actor *auth.AuthenticatedUser, opts HookOptions, st storage.Storer, rule *db.GitBranchProtection, c *packp.Command, say func(string, ...any)) error {
	branch := c.Name.Short()
	bypass := m.access.InAllowList(ctx, actor, repo, DecodeIDs(rule.BypassIDs))
	if bypass {
		return nil
	}
	if c.New.IsZero() {
		if rule.BlockDeletion && !opts.Rename {
			say("branch %s is protected from deletion", branch)
			return fmt.Errorf("protected branch")
		}
		return nil
	}
	if rule.RequirePullRequest && !opts.BypassPullRequest {
		say("branch %s only accepts changes through pull requests", branch)
		return fmt.Errorf("protected branch requires a pull request")
	}
	if pushers := DecodeIDs(rule.AllowedPushers); len(pushers) > 0 && !opts.BypassAllowedPushers && !m.access.InAllowList(ctx, actor, repo, pushers) {
		say("you are not allowed to push to %s", branch)
		return fmt.Errorf("protected branch")
	}
	if !c.Old.IsZero() && rule.BlockForcePush {
		ok, err := isAncestorIn(st, c.Old, c.New)
		if err != nil {
			// Unverifiable history is refused, never waved through
			say("cannot verify that %s moves forward: %v", branch, err)
			return fmt.Errorf("force push check failed: %w", err)
		}
		if !ok {
			say("force pushes to %s are blocked", branch)
			return fmt.Errorf("force push blocked")
		}
	}
	if rule.RequireLinearHistory || (rule.RequireSignedCommits && !opts.BypassSignedCommits) {
		commits, truncated, err := newCommits(st, c.Old, c.New, maxVerifiedCommits)
		if err != nil {
			return err
		}
		if truncated {
			say("%s receives more than %d new commits, push them in smaller batches", branch, maxVerifiedCommits)
			return fmt.Errorf("too many new commits to verify")
		}
		for _, commit := range commits {
			if rule.RequireLinearHistory && commit.NumParents() > 1 {
				say("merge commit %s violates the linear history rule on %s", commit.Hash.String()[:8], branch)
				return fmt.Errorf("linear history required")
			}
			if rule.RequireSignedCommits && !opts.BypassSignedCommits {
				v := m.signing.VerifyCommit(ctx, commit)
				if v.State != v1.GitVerificationState_GIT_VERIFICATION_STATE_VERIFIED && v.State != v1.GitVerificationState_GIT_VERIFICATION_STATE_INSTANCE {
					say("commit %s is not signed by a verified key", commit.Hash.String()[:8])
					return fmt.Errorf("signed commits required")
				}
			}
		}
	}
	return nil
}

// First protection whose pattern matches the branch, by priority
func MatchProtection(rules []*db.GitBranchProtection, branch string) *db.GitBranchProtection {
	for _, r := range rules {
		if globMatch(r.Pattern, branch) {
			return r
		}
	}
	return nil
}

// Glob match where a double star crosses slashes
func globMatch(pattern, name string) bool {
	if pattern == name {
		return true
	}
	if strings.Contains(pattern, "**") {
		re := globToRegexp(pattern)
		matched, _ := regexpMatch(re, name)
		return matched
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

func isAncestorIn(st storer.EncodedObjectStorer, ancestor, descendant plumbing.Hash) (bool, error) {
	a, err := object.GetCommit(st, ancestor)
	if err != nil {
		return false, err
	}
	d, err := object.GetCommit(st, descendant)
	if err != nil {
		return false, err
	}
	return a.IsAncestor(d)
}

// Commits reachable from new but not from old, or for a new branch from any
// existing ref, like rev-list new --not old. Newest first, truncated past limit.
func newCommits(st storage.Storer, old, new plumbing.Hash, limit int) ([]*object.Commit, bool, error) {
	head, err := object.GetCommit(st, new)
	if err != nil {
		return nil, false, err
	}
	var excludes []plumbing.Hash
	if !old.IsZero() {
		excludes = append(excludes, old)
	} else if refs, err := st.IterReferences(); err == nil {
		_ = refs.ForEach(func(ref *plumbing.Reference) error {
			if ref.Type() == plumbing.HashReference && (ref.Name().IsBranch() || ref.Name().IsTag()) && ref.Hash() != new {
				excludes = append(excludes, ref.Hash())
			}
			return nil
		})
	}
	seen := map[plumbing.Hash]bool{}
	for _, h := range excludes {
		c, err := object.GetCommit(st, h)
		if err != nil {
			if tag, terr := object.GetTag(st, h); terr == nil {
				c, err = tag.Commit()
			}
			if err != nil {
				continue
			}
		}
		_ = object.NewCommitPreorderIter(c, seen, nil).ForEach(func(c *object.Commit) error {
			seen[c.Hash] = true
			return nil
		})
	}
	var out []*object.Commit
	truncated := false
	err = object.NewCommitPreorderIter(head, seen, nil).ForEach(func(c *object.Commit) error {
		if limit > 0 && len(out) >= limit {
			truncated = true
			return storer.ErrStop
		}
		out = append(out, c)
		return nil
	})
	return out, truncated, err
}
