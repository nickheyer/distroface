package forge

import (
	"context"
	"path"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gitrepo"
)

// One codeowners rule
type OwnerRule struct {
	Pattern string
	Owners  []string
}

// Owners resolved for a pattern that matched changed paths
type OwnerGroup struct {
	Pattern string
	UserIDs []string
	TeamIDs []string
}

// Parses CODEOWNERS text, last matching rule wins like git
func ParseCodeowners(content string) []OwnerRule {
	var rules []OwnerRule
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		rules = append(rules, OwnerRule{Pattern: fields[0], Owners: fields[1:]})
	}
	return rules
}

// Reports whether a codeowners pattern covers a path
func ownerPatternMatches(pattern, p string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "*" {
		return true
	}
	anchored := strings.HasPrefix(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "/")
	dirOnly := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	if dirOnly || !strings.ContainsAny(pattern, "*?[") {
		if p == pattern || strings.HasPrefix(p, pattern+"/") {
			return true
		}
		if !anchored && !strings.Contains(pattern, "/") {
			for _, seg := range strings.Split(p, "/") {
				if seg == pattern {
					return true
				}
			}
			return strings.Contains("/"+p+"/", "/"+pattern+"/")
		}
		return false
	}
	candidates := []string{p}
	if !anchored && !strings.Contains(pattern, "/") {
		candidates = append(candidates, path.Base(p))
	}
	for _, c := range candidates {
		if ok, _ := path.Match(pattern, c); ok {
			return true
		}
		if strings.Contains(pattern, "**") {
			if globStarMatch(pattern, c) {
				return true
			}
		}
	}
	if !anchored {
		for i, seg := range strings.Split(p, "/") {
			_ = seg
			sub := strings.Join(strings.Split(p, "/")[i:], "/")
			if ok, _ := path.Match(pattern, sub); ok {
				return true
			}
		}
	}
	return false
}

// Matches patterns containing ** against a path
func globStarMatch(pattern, p string) bool {
	parts := strings.Split(pattern, "**")
	if len(parts) == 1 {
		ok, _ := path.Match(pattern, p)
		return ok
	}
	rest := p
	for i, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		if i == len(parts)-1 {
			if ok, _ := path.Match(part, path.Base(rest)); ok {
				return true
			}
			return strings.HasSuffix(rest, part)
		}
		idx := strings.Index(rest, part)
		if idx < 0 {
			return false
		}
		rest = rest[idx+len(part):]
	}
	return true
}

// Owners of the paths a pull request changes, grouped by rule
func (m *Manager) ownersForPull(ctx context.Context, repo *db.GitRepository, pr *db.GitPullRequest) []OwnerGroup {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil
	}
	defer r.Close()
	baseTip := r.BranchHash(pr.BaseBranch)
	content := ""
	for _, p := range []string{"CODEOWNERS", ".distroface/CODEOWNERS", ".gitea/CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		if b, err := r.Blob(baseTip, p, gitrepo.MaxInlineBlob); err == nil {
			content = string(b.Content)
			break
		}
	}
	if content == "" {
		return nil
	}
	rules := ParseCodeowners(content)
	mergeBase := plumbing.ZeroHash
	if pr.MergeBaseSHA != "" {
		mergeBase = plumbing.NewHash(pr.MergeBaseSHA)
	}
	diff, err := r.Diff(ctx, mergeBase, plumbing.NewHash(pr.HeadSHA), gitrepo.DiffOptions{})
	if err != nil {
		return nil
	}
	matched := map[string]bool{}
	var groups []OwnerGroup
	for _, f := range diff.Files {
		for _, p := range []string{f.NewPath, f.OldPath} {
			if p == "" {
				continue
			}
			var winner *OwnerRule
			for i := range rules {
				if ownerPatternMatches(rules[i].Pattern, p) {
					winner = &rules[i]
				}
			}
			if winner == nil || matched[winner.Pattern] {
				continue
			}
			matched[winner.Pattern] = true
			users, teams := m.resolveOwners(ctx, repo, winner.Owners)
			groups = append(groups, OwnerGroup{Pattern: winner.Pattern, UserIDs: users, TeamIDs: teams})
		}
	}
	return groups
}

// Resolves owner tokens to user ids and team ids
func (m *Manager) resolveOwners(ctx context.Context, repo *db.GitRepository, owners []string) ([]string, []string) {
	var users, teams []string
	for _, o := range owners {
		o = strings.TrimPrefix(o, "@")
		switch {
		case strings.Contains(o, "@"):
			if u, _ := m.store.GetUserByEmail(ctx, o); u != nil {
				users = append(users, u.ID)
			}
		case strings.Contains(o, "/"):
			orgName, teamName, _ := strings.Cut(o, "/")
			if orgName != repo.Namespace || !repo.IsOrgNamespace {
				continue
			}
			org, _ := m.store.GetOrganization(ctx, orgName)
			if org == nil {
				continue
			}
			if team, _ := m.store.GetOrgTeamByName(ctx, org.ID, teamName); team != nil {
				teams = append(teams, team.ID)
				ids, _ := m.store.TeamUserIDs(ctx, team)
				users = append(users, ids...)
			}
		default:
			if u, _ := m.store.GetUserByUsername(ctx, o); u != nil {
				users = append(users, u.ID)
			}
		}
	}
	return users, teams
}

// Reviewers to request automatically from codeowners
func (m *Manager) codeownerReviewers(ctx context.Context, repo *db.GitRepository, pr *db.GitPullRequest) ([]string, []string) {
	var users, teams []string
	for _, g := range m.ownersForPull(ctx, repo, pr) {
		if len(g.TeamIDs) > 0 {
			teams = append(teams, g.TeamIDs...)
			continue
		}
		users = append(users, g.UserIDs...)
	}
	return users, teams
}
