package agents

import (
	"context"
	"sort"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

// Usage aggregates sessions of the given repositories over a window, nil means every repository
func (m *Manager) Usage(ctx context.Context, repoIDs []string, days int) (*v1.GetAgentUsageResponse, error) {
	if days <= 0 {
		days = 30
	}
	since := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	rows, err := m.store.AgentUsageRows(ctx, repoIDs, since)
	if err != nil {
		return nil, err
	}
	out := &v1.GetAgentUsageResponse{Totals: &v1.AgentUsageBucket{Key: "total", Label: "Total"}}
	byDay := map[string]*v1.AgentUsageBucket{}
	for i := 0; i < days; i++ {
		day := since.AddDate(0, 0, i).Format("2006-01-02")
		byDay[day] = &v1.AgentUsageBucket{Key: day, Label: day}
	}
	agents := map[string]*v1.AgentUsageBucket{}
	repos := map[string]*v1.AgentUsageBucket{}
	providers := map[string]*v1.AgentUsageBucket{}
	models := map[string]*v1.AgentUsageBucket{}
	var repoIDSet []string
	seenRepo := map[string]bool{}
	for _, r := range rows {
		add := func(b *v1.AgentUsageBucket) {
			b.Sessions++
			b.InputTokens += r.InputTokens
			b.OutputTokens += r.OutputTokens
			b.ToolCalls += r.ToolCalls
			if r.Status == StatusCompleted {
				if r.Conclusion == ConclusionSuccess {
					b.Succeeded++
				} else {
					b.Failed++
				}
			}
		}
		add(out.Totals)
		if b := byDay[r.CreatedAt.UTC().Format("2006-01-02")]; b != nil {
			add(b)
		}
		key := r.AgentSlug
		if key == "" {
			key = r.AgentID
		}
		b := agents[key]
		if b == nil {
			b = &v1.AgentUsageBucket{Key: key, Label: r.AgentName}
			if b.Label == "" {
				b.Label = key
			}
			agents[key] = b
		}
		add(b)
		rb := repos[r.RepoID]
		if rb == nil {
			rb = &v1.AgentUsageBucket{Key: r.RepoID, Label: r.RepoID}
			repos[r.RepoID] = rb
			if !seenRepo[r.RepoID] {
				seenRepo[r.RepoID] = true
				repoIDSet = append(repoIDSet, r.RepoID)
			}
		}
		add(rb)
		pk := r.ProviderID
		if pk == "" {
			pk = r.ProviderName
		}
		pb := providers[pk]
		if pb == nil {
			pb = &v1.AgentUsageBucket{Key: pk, Label: r.ProviderName}
			if pb.Label == "" {
				pb.Label = "unknown"
			}
			providers[pk] = pb
		}
		add(pb)
		mb := models[r.Model]
		if mb == nil {
			mb = &v1.AgentUsageBucket{Key: r.Model, Label: r.Model}
			if mb.Label == "" {
				mb.Label = "unknown"
			}
			models[r.Model] = mb
		}
		add(mb)
	}
	if names, err := m.store.GetGitRepositoriesByIDs(ctx, repoIDSet); err == nil {
		for id, b := range repos {
			if repo := names[id]; repo != nil {
				b.Label = repo.Namespace + "/" + repo.Name
			}
		}
	}
	for i := 0; i < days; i++ {
		out.Days = append(out.Days, byDay[since.AddDate(0, 0, i).Format("2006-01-02")])
	}
	out.Agents = sortedBuckets(agents)
	out.Repos = sortedBuckets(repos)
	out.Providers = sortedBuckets(providers)
	out.Models = sortedBuckets(models)
	return out, nil
}

// Buckets by sessions descending
func sortedBuckets(in map[string]*v1.AgentUsageBucket) []*v1.AgentUsageBucket {
	out := make([]*v1.AgentUsageBucket, 0, len(in))
	for _, b := range in {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// Repository ids a scope covers for usage reports, nil for the instance
func (m *Manager) ScopeRepoIDs(ctx context.Context, scopeKind, scopeID string) ([]string, error) {
	switch scopeKind {
	case db.ActionsScopeRepo:
		return []string{scopeID}, nil
	case db.ActionsScopeOrg:
		org, err := m.store.GetOrganizationByID(ctx, scopeID)
		if err != nil {
			return nil, err
		}
		if org == nil {
			return []string{}, nil
		}
		repos, err := m.store.ListGitRepositoriesByNamespace(ctx, org.Name)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(repos))
		for _, r := range repos {
			ids = append(ids, r.ID)
		}
		return ids, nil
	}
	return nil, nil
}

// Keeps the stores import used by callers that pass list options through
var _ = stores.AgentSessionListOptions{}
