package forge

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/nickheyer/distroface/internal/db"
)

// Issue references of the forms #12 and ns/name#12
var refPattern = regexp.MustCompile(`(?i)(?:^|[\s(\[,.:;])(?:([a-z0-9][a-z0-9._-]*)/([a-z0-9][a-z0-9._-]*))?#(\d+)\b`)

// Closing keywords ahead of a reference
var closePattern = regexp.MustCompile(`(?i)\b(close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)[:\s]+(?:([a-z0-9][a-z0-9._-]*)/([a-z0-9][a-z0-9._-]*))?#(\d+)\b`)

// User and team mentions
var mentionPattern = regexp.MustCompile(`(?:^|[^\w@/])@([a-zA-Z0-9][a-zA-Z0-9._-]*(?:/[a-zA-Z0-9][a-zA-Z0-9._-]*)?)`)

// One parsed issue reference
type IssueReference struct {
	Namespace string
	Name      string
	Number    int64
	Closes    bool
}

// Parses references in markdown or a commit message, fenced code skipped
func ParseReferences(text string) []IssueReference {
	text = stripCode(text)
	seen := map[string]bool{}
	var out []IssueReference
	add := func(ns, name, num string, closes bool) {
		n, err := strconv.ParseInt(num, 10, 64)
		if err != nil || n <= 0 {
			return
		}
		key := ns + "/" + name + "#" + num
		if seen[key] {
			if closes {
				for i := range out {
					if out[i].Namespace == ns && out[i].Name == name && out[i].Number == n {
						out[i].Closes = true
					}
				}
			}
			return
		}
		seen[key] = true
		out = append(out, IssueReference{Namespace: ns, Name: name, Number: n, Closes: closes})
	}
	for _, m := range closePattern.FindAllStringSubmatch(text, -1) {
		add(strings.ToLower(m[2]), strings.ToLower(m[3]), m[4], true)
	}
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		add(strings.ToLower(m[1]), strings.ToLower(m[2]), m[3], false)
	}
	return out
}

// Usernames and org/team names mentioned in text
func mentions(text string) []string {
	text = stripCode(text)
	seen := map[string]bool{}
	var out []string
	for _, m := range mentionPattern.FindAllStringSubmatch(text, -1) {
		name := strings.TrimRight(m[1], ".")
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	return out
}

// Removes fenced and inline code so references inside are ignored
func stripCode(text string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			b.WriteByte('\n')
			continue
		}
		if inFence {
			b.WriteByte('\n')
			continue
		}
		// Inline code spans drop out, keeping line structure
		parts := strings.Split(line, "`")
		for i, p := range parts {
			if i%2 == 0 {
				b.WriteString(p)
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Resolves a reference to an issue row when the target is readable by the actor
func (m *Manager) resolveReference(ctx context.Context, repo *db.GitRepository, actor Actor, ref IssueReference) (*db.GitRepository, *db.GitIssue) {
	target := repo
	if ref.Namespace != "" {
		other, err := m.store.GetGitRepository(ctx, ref.Namespace, ref.Name)
		if err != nil || other == nil {
			return nil, nil
		}
		if actor.User != nil && !m.git.Access().CanSee(ctx, actor.User, other) {
			return nil, nil
		}
		target = other
	}
	issue, err := m.store.GetGitIssue(ctx, target.ID, ref.Number)
	if err != nil || issue == nil {
		return nil, nil
	}
	return target, issue
}

// Records referenced events on issues named in a body
func (m *Manager) recordReferences(ctx context.Context, repo *db.GitRepository, source *db.GitIssue, actor Actor, body string) {
	for _, ref := range ParseReferences(body) {
		target, issue := m.resolveReference(ctx, repo, actor, ref)
		if issue == nil || (source != nil && issue.ID == source.ID) {
			continue
		}
		payload := map[string]any{"repo": repo.Namespace + "/" + repo.Name}
		if source != nil {
			payload["number"] = source.Number
			payload["is_pull"] = source.IsPull
			payload["title"] = source.Title
		}
		_ = target
		_, _ = m.AddEvent(ctx, issue, actor.User, EventReferenced, payload)
	}
}
