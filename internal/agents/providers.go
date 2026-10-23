package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nickheyer/distroface/internal/agents/llm"
	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

const sealPurpose = "agents"

// Fields of a new or changed connector
type ProviderSpec struct {
	Name              string
	Kind              string
	BaseURL           string
	APIKey            *string
	Headers           map[string]string
	SetHeaders        bool
	APIVersion        string
	DefaultModel      string
	Models            []string
	MaxTokens         int
	ContextWindow     int
	TimeoutSeconds    int
	SkipTLSVerify     bool
	ToolsSupported    bool
	RequestsPerMinute int
	TokensPerDay      int64
	Enabled           bool
	IsDefault         bool
}

// Sliding window request limiter per connector
type rateLimiter struct {
	mu    sync.Mutex
	times []time.Time
}

// Blocks until a request fits under the per minute cap
func (l *rateLimiter) wait(ctx context.Context, rpm int) error {
	if rpm <= 0 {
		return nil
	}
	for {
		l.mu.Lock()
		cutoff := time.Now().Add(-time.Minute)
		kept := l.times[:0]
		for _, t := range l.times {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		l.times = kept
		if len(l.times) < rpm {
			l.times = append(l.times, time.Now())
			l.mu.Unlock()
			return nil
		}
		wait := time.Until(l.times[0].Add(time.Minute))
		l.mu.Unlock()
		if wait < 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (m *Manager) limiter(providerID string) *rateLimiter {
	m.limMu.Lock()
	defer m.limMu.Unlock()
	l := m.limiters[providerID]
	if l == nil {
		l = &rateLimiter{}
		m.limiters[providerID] = l
	}
	return l
}

// Seals a secret for storage
func (m *Manager) seal(plain string) ([]byte, error) {
	if plain == "" {
		return nil, nil
	}
	out, err := m.auth.Sealer(sealPurpose).Seal(plain)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// Opens a stored secret, empty when none
func (m *Manager) open(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	return m.auth.Sealer(sealPurpose).Open(string(sealed))
}

// Decrypted extra headers of a connector
func (m *Manager) providerHeaders(p *db.AgentProvider) map[string]string {
	raw, err := m.open(p.Headers)
	if err != nil || raw == "" {
		return nil
	}
	var out map[string]string
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

// Allowed models of a connector, empty allows any
func ProviderModels(p *db.AgentProvider) []string {
	var out []string
	if p.Models != "" {
		_ = json.Unmarshal([]byte(p.Models), &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// Validates a spec and applies it onto a row
func (m *Manager) applyProviderSpec(p *db.AgentProvider, spec ProviderSpec) error {
	name := strings.TrimSpace(spec.Name)
	if name == "" || len(name) > 100 {
		return fmt.Errorf("%w: a connector name is required", ErrInvalid)
	}
	if !llm.ValidKind(spec.Kind) {
		return fmt.Errorf("%w: unknown connector kind %q", ErrInvalid, spec.Kind)
	}
	base := strings.TrimRight(strings.TrimSpace(spec.BaseURL), "/")
	if base == "" && llm.DefaultBaseURL(spec.Kind) == "" {
		return fmt.Errorf("%w: %s connectors need a base url", ErrInvalid, spec.Kind)
	}
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: base url must be an http or https url", ErrInvalid)
		}
	}
	if spec.TimeoutSeconds < 0 || spec.MaxTokens < 0 || spec.RequestsPerMinute < 0 || spec.TokensPerDay < 0 || spec.ContextWindow < 0 {
		return fmt.Errorf("%w: limits cannot be negative", ErrInvalid)
	}
	models := []string{}
	seen := map[string]bool{}
	for _, mdl := range spec.Models {
		mdl = strings.TrimSpace(mdl)
		if mdl == "" || seen[mdl] {
			continue
		}
		seen[mdl] = true
		models = append(models, mdl)
	}
	defaultModel := strings.TrimSpace(spec.DefaultModel)
	if len(models) > 0 && defaultModel != "" && !seen[defaultModel] {
		models = append(models, defaultModel)
	}
	if spec.APIKey != nil {
		sealed, err := m.seal(strings.TrimSpace(*spec.APIKey))
		if err != nil {
			return err
		}
		p.APIKey = sealed
	}
	if spec.SetHeaders {
		clean := map[string]string{}
		for k, v := range spec.Headers {
			if k = strings.TrimSpace(k); k != "" {
				clean[k] = v
			}
		}
		if len(clean) == 0 {
			p.Headers = nil
		} else {
			raw, _ := json.Marshal(clean)
			sealed, err := m.seal(string(raw))
			if err != nil {
				return err
			}
			p.Headers = sealed
		}
	}
	modelsRaw, _ := json.Marshal(models)
	p.Name, p.Kind, p.BaseURL, p.APIVersion, p.DefaultModel = name, spec.Kind, base, strings.TrimSpace(spec.APIVersion), defaultModel
	p.Models, p.MaxTokens, p.ContextWindow, p.TimeoutSeconds = string(modelsRaw), spec.MaxTokens, spec.ContextWindow, spec.TimeoutSeconds
	p.SkipTLSVerify, p.ToolsSupported, p.RequestsPerMinute, p.TokensPerDay = spec.SkipTLSVerify, spec.ToolsSupported, spec.RequestsPerMinute, spec.TokensPerDay
	p.Enabled, p.IsDefault = spec.Enabled, spec.IsDefault
	return nil
}

// CreateProvider registers a connector at a scope
func (m *Manager) CreateProvider(ctx context.Context, scopeKind, scopeID string, spec ProviderSpec, createdBy string) (*db.AgentProvider, error) {
	if err := ValidScope(scopeKind, scopeID); err != nil {
		return nil, err
	}
	if scopeKind == db.ActionsScopeRepo && !m.ScopeSettings(ctx, scopeKind, scopeID).GetAllowRepoProviders() {
		return nil, fmt.Errorf("%w: repository connectors are disabled by policy", ErrDenied)
	}
	p := &db.AgentProvider{ScopeKind: scopeKind, ScopeID: scopeID, CreatedBy: createdBy}
	if err := m.applyProviderSpec(p, spec); err != nil {
		return nil, err
	}
	// The first connector of a scope is the default
	if existing, _, err := m.store.ListAgentProviders(ctx, scopeKind, scopeID, 1, 0); err == nil && len(existing) == 0 {
		p.IsDefault = true
	}
	if err := m.store.CreateAgentProvider(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateProviderInMemory applies a spec onto a copy without saving, for tests of unsaved edits
func (m *Manager) UpdateProviderInMemory(p *db.AgentProvider, spec ProviderSpec) error {
	return m.applyProviderSpec(p, spec)
}

// UpdateProvider replaces a connector's fields
func (m *Manager) UpdateProvider(ctx context.Context, p *db.AgentProvider, spec ProviderSpec) error {
	if err := m.applyProviderSpec(p, spec); err != nil {
		return err
	}
	return m.store.UpdateAgentProvider(ctx, p)
}

// DeleteProvider removes a connector and unbinds its agents
func (m *Manager) DeleteProvider(ctx context.Context, p *db.AgentProvider) error {
	return m.store.DeleteAgentProvider(ctx, p.ID)
}

// ValidScope checks a scope kind and id pair
func ValidScope(kind, id string) error {
	switch kind {
	case db.ActionsScopeSystem:
		if id != "" {
			return fmt.Errorf("%w: system scope takes no id", ErrInvalid)
		}
	case db.ActionsScopeOrg, db.ActionsScopeRepo:
		if id == "" {
			return fmt.Errorf("%w: %s scope needs an id", ErrInvalid, kind)
		}
	default:
		return fmt.Errorf("%w: unknown scope kind %q", ErrInvalid, kind)
	}
	return nil
}

// Scope precedence when picking defaults, repo wins
var scopeRank = map[string]int{db.ActionsScopeRepo: 0, db.ActionsScopeOrg: 1, db.ActionsScopeSystem: 2}

// ProvidersForRepo lists connectors a repository may use honoring the repo connector policy
func (m *Manager) ProvidersForRepo(ctx context.Context, repo *db.GitRepository) ([]*db.AgentProvider, error) {
	rows, err := m.store.ListAgentProvidersForRepo(ctx, repo.ID, m.repoOrgID(ctx, repo))
	if err != nil {
		return nil, err
	}
	allowRepo := m.Settings(ctx, repo).GetAllowRepoProviders()
	out := rows[:0]
	for _, p := range rows {
		if p.ScopeKind == db.ActionsScopeRepo && !allowRepo {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// Connector and model a session will speak through
type ResolvedProvider struct {
	Row    *db.AgentProvider
	Client llm.Client
	Model  string
}

// ResolveProvider picks the named connector or the nearest scope default and validates the model
func (m *Manager) ResolveProvider(ctx context.Context, repo *db.GitRepository, providerID, model string) (*ResolvedProvider, error) {
	rows, err := m.ProvidersForRepo(ctx, repo)
	if err != nil {
		return nil, err
	}
	var chosen *db.AgentProvider
	if providerID != "" {
		for _, p := range rows {
			if p.ID == providerID {
				chosen = p
				break
			}
		}
		if chosen == nil {
			return nil, fmt.Errorf("%w: connector is not available to this repository", ErrNotFound)
		}
		if !chosen.Enabled {
			return nil, fmt.Errorf("%w: connector %s is disabled", ErrDisabled, chosen.Name)
		}
	} else {
		enabled := make([]*db.AgentProvider, 0, len(rows))
		for _, p := range rows {
			if p.Enabled {
				enabled = append(enabled, p)
			}
		}
		if len(enabled) == 0 {
			return nil, fmt.Errorf("%w: no inference connector is configured for this repository", ErrDisabled)
		}
		sort.SliceStable(enabled, func(i, j int) bool {
			if scopeRank[enabled[i].ScopeKind] != scopeRank[enabled[j].ScopeKind] {
				return scopeRank[enabled[i].ScopeKind] < scopeRank[enabled[j].ScopeKind]
			}
			return enabled[i].IsDefault && !enabled[j].IsDefault
		})
		chosen = enabled[0]
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = chosen.DefaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("%w: connector %s has no default model, pick one on the agent", ErrInvalid, chosen.Name)
	}
	if allowed := ProviderModels(chosen); len(allowed) > 0 {
		ok := false
		for _, a := range allowed {
			if a == model {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("%w: model %s is not allowed on connector %s", ErrDenied, model, chosen.Name)
		}
	}
	client, err := m.Client(ctx, chosen)
	if err != nil {
		return nil, err
	}
	return &ResolvedProvider{Row: chosen, Client: client, Model: model}, nil
}

// Client builds a speaking client for a connector row
func (m *Manager) Client(ctx context.Context, p *db.AgentProvider) (llm.Client, error) {
	key, err := m.open(p.APIKey)
	if err != nil {
		return nil, fmt.Errorf("connector %s: api key cannot be decrypted, re-enter it", p.Name)
	}
	return llm.New(llm.Config{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: key, Headers: m.providerHeaders(p), APIVersion: p.APIVersion,
		TimeoutSeconds: p.TimeoutSeconds, SkipTLSVerify: p.SkipTLSVerify, ToolsSupported: p.ToolsSupported,
		AllowPrivate: func() bool {
			return m.res.System(context.Background()).GetGit().GetAgents().GetAllowPrivateNetworks()
		},
		UserAgent: "distroface-agents",
	})
}

// Unsaved connector built from a spec, for tests before saving
func (m *Manager) ClientForSpec(spec ProviderSpec) (llm.Client, *db.AgentProvider, error) {
	p := &db.AgentProvider{}
	if err := m.applyProviderSpec(p, spec); err != nil {
		return nil, nil, err
	}
	c, err := m.Client(context.Background(), p)
	return c, p, err
}

// Tokens a connector has spent today
func (m *Manager) ProviderTokensToday(ctx context.Context, providerID string) int64 {
	if providerID == "" {
		return 0
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	n, _ := m.store.AgentProviderTokensSince(ctx, providerID, start)
	return n
}

// Refuses work once the connector's daily token cap is spent
func (m *Manager) checkProviderBudget(ctx context.Context, p *db.AgentProvider) error {
	if p.TokensPerDay <= 0 {
		return nil
	}
	if used := m.ProviderTokensToday(ctx, p.ID); used >= p.TokensPerDay {
		return fmt.Errorf("%w: connector %s spent its daily token budget", ErrBudget, p.Name)
	}
	return nil
}

// Outcome of a connector probe
type TestResult struct {
	OK      bool
	Message string
	Models  []llm.Model
	Latency time.Duration
	Sample  string
	ToolsOK bool
}

// ProviderContext bounds a call by the connector timeout, zero waits
func ProviderContext(ctx context.Context, p *db.AgentProvider) (context.Context, context.CancelFunc) {
	if p != nil && p.TimeoutSeconds > 0 {
		return context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	}
	return context.WithCancel(ctx)
}

// TestProvider lists models and runs a tiny completion, recording the outcome on saved rows
func (m *Manager) TestProvider(ctx context.Context, p *db.AgentProvider, client llm.Client, model string) *TestResult {
	out := &TestResult{}
	ctx, cancel := ProviderContext(ctx, p)
	defer cancel()
	models, err := client.ListModels(ctx)
	if err == nil {
		out.Models = models
	}
	if model == "" {
		model = p.DefaultModel
	}
	if model == "" && len(models) > 0 {
		model = models[0].ID
	}
	if model == "" {
		out.Message = "no model to test with, set a default model"
		if err != nil {
			out.Message = fmt.Sprintf("model listing failed: %v", err)
		}
		m.recordTest(ctx, p, out)
		return out
	}
	start := time.Now()
	req := &llm.Request{
		Model:     model,
		System:    "You are a connectivity probe. Follow instructions exactly.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "Call the tool named ping with {\"value\":\"pong\"}. If you cannot call tools, reply with the single word OK."}},
		MaxTokens: 64,
	}
	if p.ToolsSupported {
		req.Tools = []llm.Tool{{Name: "ping", Description: "Connectivity probe", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`)}}
	}
	resp, err := client.Complete(ctx, req)
	out.Latency = time.Since(start)
	if err != nil {
		out.Message = err.Error()
		m.recordTest(ctx, p, out)
		return out
	}
	out.OK = true
	out.Sample = strings.TrimSpace(resp.Content)
	for _, tc := range resp.ToolCalls {
		if tc.Name == "ping" {
			out.ToolsOK = true
		}
	}
	switch {
	case p.ToolsSupported && out.ToolsOK:
		out.Message = fmt.Sprintf("%s answered in %s and called tools natively", model, out.Latency.Round(time.Millisecond))
	case p.ToolsSupported:
		out.Message = fmt.Sprintf("%s answered in %s but did not call the tool, consider turning native tools off", model, out.Latency.Round(time.Millisecond))
	default:
		out.Message = fmt.Sprintf("%s answered in %s", model, out.Latency.Round(time.Millisecond))
	}
	m.recordTest(ctx, p, out)
	return out
}

func (m *Manager) recordTest(ctx context.Context, p *db.AgentProvider, out *TestResult) {
	if p.ID == "" {
		return
	}
	errText := ""
	if !out.OK {
		errText = out.Message
	}
	_ = m.store.UpdateAgentProviderColumns(context.WithoutCancel(ctx), p.ID, map[string]any{"last_tested_at": time.Now().UTC(), "last_test_error": errText})
}

// ProtoProviderKind maps a stored kind onto the enum
func ProtoProviderKind(k string) v1.AgentProviderKind {
	switch k {
	case llm.KindOpenAI:
		return v1.AgentProviderKind_AGENT_PROVIDER_KIND_OPENAI
	case llm.KindAnthropic:
		return v1.AgentProviderKind_AGENT_PROVIDER_KIND_ANTHROPIC
	case llm.KindAzureOpenAI:
		return v1.AgentProviderKind_AGENT_PROVIDER_KIND_AZURE_OPENAI
	case llm.KindOllama:
		return v1.AgentProviderKind_AGENT_PROVIDER_KIND_OLLAMA
	case llm.KindGemini:
		return v1.AgentProviderKind_AGENT_PROVIDER_KIND_GEMINI
	case llm.KindBedrock:
		return v1.AgentProviderKind_AGENT_PROVIDER_KIND_BEDROCK
	}
	return v1.AgentProviderKind_AGENT_PROVIDER_KIND_UNSPECIFIED
}

// ProviderKindFromProto maps the enum onto a stored kind
func ProviderKindFromProto(k v1.AgentProviderKind) string {
	switch k {
	case v1.AgentProviderKind_AGENT_PROVIDER_KIND_OPENAI:
		return llm.KindOpenAI
	case v1.AgentProviderKind_AGENT_PROVIDER_KIND_ANTHROPIC:
		return llm.KindAnthropic
	case v1.AgentProviderKind_AGENT_PROVIDER_KIND_AZURE_OPENAI:
		return llm.KindAzureOpenAI
	case v1.AgentProviderKind_AGENT_PROVIDER_KIND_OLLAMA:
		return llm.KindOllama
	case v1.AgentProviderKind_AGENT_PROVIDER_KIND_GEMINI:
		return llm.KindGemini
	case v1.AgentProviderKind_AGENT_PROVIDER_KIND_BEDROCK:
		return llm.KindBedrock
	}
	return ""
}
