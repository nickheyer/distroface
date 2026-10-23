package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/agents"
	"github.com/nickheyer/distroface/internal/agents/llm"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.AgentsServiceHandler = (*AgentsService)(nil)

const agentsStreamPoll = 2 * time.Second

type AgentsService struct {
	store   *stores.Store
	agents  *agents.Manager
	manager *gitrepo.Manager
	log     *logger.Logger
}

func NewAgentsService(store *stores.Store, am *agents.Manager, manager *gitrepo.Manager, log *logger.Logger) *AgentsService {
	return &AgentsService{store: store, agents: am, manager: manager, log: log}
}

// ── Providers ────────────────────────────────────────────────────────────

func (s *AgentsService) ListAgentProviders(ctx context.Context, req *v1.ListAgentProvidersRequest) (*v1.ListAgentProvidersResponse, error) {
	sc, err := s.agentsScope(ctx, req.Scope, false)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	if !req.IncludeInherited {
		rows, total, err := s.store.ListAgentProviders(ctx, sc.kind, sc.id, limit, offset)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		out := make([]*v1.AgentProvider, 0, len(rows))
		for _, p := range rows {
			out = append(out, s.providerProto(ctx, p, sc.proto(), false))
		}
		return &v1.ListAgentProvidersResponse{Providers: out, Page: pages.Info(offset, limit, total)}, nil
	}
	rows, err := s.inheritedProviders(ctx, sc)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	total := int64(len(rows))
	rows = pageSlice(rows, limit, offset)
	out := make([]*v1.AgentProvider, 0, len(rows))
	for _, p := range rows {
		out = append(out, s.providerProto(ctx, p, sc.servingScope(p.ScopeKind), p.ScopeKind != sc.kind))
	}
	return &v1.ListAgentProvidersResponse{Providers: out, Page: pages.Info(offset, limit, total)}, nil
}

// Connectors of a scope plus the ones it inherits from above
func (s *AgentsService) inheritedProviders(ctx context.Context, sc *agentsScopeRef) ([]*storage.AgentProvider, error) {
	switch sc.kind {
	case storage.ActionsScopeRepo:
		return s.store.ListAgentProvidersForRepo(ctx, sc.id, sc.orgID)
	case storage.ActionsScopeOrg:
		own, _, err := s.store.ListAgentProviders(ctx, storage.ActionsScopeOrg, sc.id, 0, 0)
		if err != nil {
			return nil, err
		}
		sys, _, err := s.store.ListAgentProviders(ctx, storage.ActionsScopeSystem, "", 0, 0)
		if err != nil {
			return nil, err
		}
		return append(own, sys...), nil
	}
	own, _, err := s.store.ListAgentProviders(ctx, storage.ActionsScopeSystem, "", 0, 0)
	return own, err
}

func pageSlice[T any](rows []T, limit, offset int) []T {
	if offset >= len(rows) {
		return nil
	}
	rows = rows[offset:]
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func (s *AgentsService) GetAgentProvider(ctx context.Context, req *v1.GetAgentProviderRequest) (*v1.GetAgentProviderResponse, error) {
	sc, p, err := s.scopedProvider(ctx, req.Scope, req.Id, false)
	if err != nil {
		return nil, err
	}
	return &v1.GetAgentProviderResponse{Provider: s.providerProto(ctx, p, sc.servingScope(p.ScopeKind), p.ScopeKind != sc.kind)}, nil
}

func (s *AgentsService) CreateAgentProvider(ctx context.Context, req *v1.CreateAgentProviderRequest) (*v1.CreateAgentProviderResponse, error) {
	sc, err := s.agentsScope(ctx, req.Scope, true)
	if err != nil {
		return nil, err
	}
	user := auth.UserFromContext(ctx)
	p, err := s.agents.CreateProvider(ctx, sc.kind, sc.id, providerSpecFromProto(req.Spec), user.ID)
	if err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.CreateAgentProviderResponse{Provider: s.providerProto(ctx, p, sc.proto(), false)}, nil
}

func (s *AgentsService) UpdateAgentProvider(ctx context.Context, req *v1.UpdateAgentProviderRequest) (*v1.UpdateAgentProviderResponse, error) {
	sc, p, err := s.scopedProvider(ctx, req.Scope, req.Id, true)
	if err != nil {
		return nil, err
	}
	if p.ScopeKind != sc.kind || p.ScopeID != sc.id {
		return nil, connect.Errorf(connect.CodePermissionDenied, "inherited connectors are managed at their own scope")
	}
	if err := s.agents.UpdateProvider(ctx, p, providerSpecFromProto(req.Spec)); err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.UpdateAgentProviderResponse{Provider: s.providerProto(ctx, p, sc.proto(), false)}, nil
}

func (s *AgentsService) DeleteAgentProvider(ctx context.Context, req *v1.DeleteAgentProviderRequest) (*v1.DeleteAgentProviderResponse, error) {
	sc, p, err := s.scopedProvider(ctx, req.Scope, req.Id, true)
	if err != nil {
		return nil, err
	}
	if p.ScopeKind != sc.kind || p.ScopeID != sc.id {
		return nil, connect.Errorf(connect.CodePermissionDenied, "inherited connectors are managed at their own scope")
	}
	if err := s.agents.DeleteProvider(ctx, p); err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.DeleteAgentProviderResponse{}, nil
}

func (s *AgentsService) TestAgentProvider(ctx context.Context, req *v1.TestAgentProviderRequest) (*v1.TestAgentProviderResponse, error) {
	var row *storage.AgentProvider
	var client llm.Client
	if req.Id != "" {
		_, p, err := s.scopedProvider(ctx, req.Scope, req.Id, false)
		if err != nil {
			return nil, err
		}
		row = p
		if req.Spec != nil {
			// Unsaved edits of an existing connector, an absent key keeps the stored one
			spec := providerSpecFromProto(req.Spec)
			copyRow := *p
			if err := s.agents.UpdateProviderInMemory(&copyRow, spec); err != nil {
				return nil, mapAgentsErr(err)
			}
			copyRow.ID = ""
			row = &copyRow
		}
		c, err := s.agents.Client(ctx, row)
		if err != nil {
			return &v1.TestAgentProviderResponse{Ok: false, Message: err.Error()}, nil
		}
		client = c
	} else {
		if _, err := s.agentsScope(ctx, req.Scope, true); err != nil {
			return nil, err
		}
		if req.Spec == nil {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "a connector id or spec is required")
		}
		c, p, err := s.agents.ClientForSpec(providerSpecFromProto(req.Spec))
		if err != nil {
			if errors.Is(err, agents.ErrInvalid) {
				return nil, mapAgentsErr(err)
			}
			return &v1.TestAgentProviderResponse{Ok: false, Message: err.Error()}, nil
		}
		client, row = c, p
	}
	res := s.agents.TestProvider(ctx, row, client, req.Model)
	out := &v1.TestAgentProviderResponse{Ok: res.OK, Message: res.Message, LatencyMs: res.Latency.Milliseconds(), Sample: res.Sample, ToolsOk: res.ToolsOK}
	for _, m := range res.Models {
		out.Models = append(out.Models, &v1.AgentModel{Id: m.ID, Name: m.Name, ContextWindow: m.ContextWindow})
	}
	return out, nil
}

func (s *AgentsService) ListAgentProviderModels(ctx context.Context, req *v1.ListAgentProviderModelsRequest) (*v1.ListAgentProviderModelsResponse, error) {
	_, p, err := s.scopedProvider(ctx, req.Scope, req.Id, false)
	if err != nil {
		return nil, err
	}
	client, err := s.agents.Client(ctx, p)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	}
	ctx, cancel := agents.ProviderContext(ctx, p)
	defer cancel()
	models, err := client.ListModels(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
	}
	out := &v1.ListAgentProviderModelsResponse{}
	for _, m := range models {
		out.Models = append(out.Models, &v1.AgentModel{Id: m.ID, Name: m.Name, ContextWindow: m.ContextWindow})
	}
	return out, nil
}

func providerSpecFromProto(in *v1.AgentProviderSpec) agents.ProviderSpec {
	if in == nil {
		return agents.ProviderSpec{}
	}
	out := agents.ProviderSpec{
		Name: in.Name, Kind: agents.ProviderKindFromProto(in.Kind), BaseURL: in.BaseUrl, APIKey: in.ApiKey, Headers: in.Headers, SetHeaders: in.SetHeaders,
		APIVersion: in.ApiVersion, DefaultModel: in.DefaultModel, Models: in.Models, MaxTokens: int(in.MaxTokens), ContextWindow: int(in.ContextWindow),
		TimeoutSeconds: int(in.TimeoutSeconds), SkipTLSVerify: in.SkipTlsVerify, ToolsSupported: in.ToolsSupported, RequestsPerMinute: int(in.RequestsPerMinute),
		TokensPerDay: in.TokensPerDay, Enabled: in.Enabled, IsDefault: in.IsDefault,
	}
	return out
}

func (s *AgentsService) providerProto(ctx context.Context, p *storage.AgentProvider, scope *v1.ActionsScope, inherited bool) *v1.AgentProvider {
	out := &v1.AgentProvider{
		Id: p.ID, Scope: scope, Name: p.Name, Kind: agents.ProtoProviderKind(p.Kind), BaseUrl: p.BaseURL, ApiKeySet: len(p.APIKey) > 0, HeadersSet: len(p.Headers) > 0,
		ApiVersion: p.APIVersion, DefaultModel: p.DefaultModel, Models: agents.ProviderModels(p), MaxTokens: int32(p.MaxTokens), TimeoutSeconds: int32(p.TimeoutSeconds),
		SkipTlsVerify: p.SkipTLSVerify, ToolsSupported: p.ToolsSupported, RequestsPerMinute: int32(p.RequestsPerMinute), TokensPerDay: p.TokensPerDay,
		Enabled: p.Enabled, IsDefault: p.IsDefault, LastTestError: p.LastTestError, CreatedBy: p.CreatedBy, Inherited: inherited, ContextWindow: int32(p.ContextWindow),
		CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
	if p.BaseURL == "" {
		out.BaseUrl = llm.DefaultBaseURL(p.Kind)
	}
	if p.LastTestedAt != nil {
		out.LastTestedAt = timestamppb.New(*p.LastTestedAt)
	}
	out.TokensToday = s.agents.ProviderTokensToday(ctx, p.ID)
	return out
}

// ── Agents ───────────────────────────────────────────────────────────────

func (s *AgentsService) ListAgents(ctx context.Context, req *v1.ListAgentsRequest) (*v1.ListAgentsResponse, error) {
	sc, err := s.agentsScope(ctx, req.Scope, false)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	var defs []*agents.Definition
	if !req.IncludeInherited {
		rows, _, err := s.store.ListAgents(ctx, sc.kind, sc.id, 0, 0)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		for _, a := range rows {
			defs = append(defs, agents.DefinitionFromRow(a))
		}
		if sc.kind == storage.ActionsScopeRepo && sc.repo != nil {
			defs = append(defs, s.agents.FileAgents(ctx, sc.repo)...)
		}
	} else {
		defs, err = s.inheritedAgents(ctx, sc)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	}
	total := int64(len(defs))
	defs = pageSlice(defs, limit, offset)
	out := make([]*v1.Agent, 0, len(defs))
	for _, d := range defs {
		out = append(out, s.agentProto(ctx, d, sc.servingScope(d.ScopeKind), d.ScopeKind != sc.kind || d.ScopeID != sc.id))
	}
	return &v1.ListAgentsResponse{Agents: out, Page: pages.Info(offset, limit, total)}, nil
}

func (s *AgentsService) inheritedAgents(ctx context.Context, sc *agentsScopeRef) ([]*agents.Definition, error) {
	var out []*agents.Definition
	switch sc.kind {
	case storage.ActionsScopeRepo:
		rows, err := s.store.ListAgentsForRepo(ctx, sc.id, sc.orgID)
		if err != nil {
			return nil, err
		}
		for _, a := range rows {
			if a.ScopeKind == storage.ActionsScopeRepo {
				out = append(out, agents.DefinitionFromRow(a))
			}
		}
		if sc.repo != nil {
			out = append(out, s.agents.FileAgents(ctx, sc.repo)...)
		}
		for _, a := range rows {
			if a.ScopeKind != storage.ActionsScopeRepo {
				out = append(out, agents.DefinitionFromRow(a))
			}
		}
	case storage.ActionsScopeOrg:
		own, _, err := s.store.ListAgents(ctx, storage.ActionsScopeOrg, sc.id, 0, 0)
		if err != nil {
			return nil, err
		}
		sys, _, err := s.store.ListAgents(ctx, storage.ActionsScopeSystem, "", 0, 0)
		if err != nil {
			return nil, err
		}
		for _, a := range append(own, sys...) {
			out = append(out, agents.DefinitionFromRow(a))
		}
	default:
		sys, _, err := s.store.ListAgents(ctx, storage.ActionsScopeSystem, "", 0, 0)
		if err != nil {
			return nil, err
		}
		for _, a := range sys {
			out = append(out, agents.DefinitionFromRow(a))
		}
	}
	return out, nil
}

func (s *AgentsService) GetAgent(ctx context.Context, req *v1.GetAgentRequest) (*v1.GetAgentResponse, error) {
	sc, err := s.agentsScope(ctx, req.Scope, false)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(req.Id, "file:") && sc.repo != nil {
		for _, d := range s.agents.FileAgents(ctx, sc.repo) {
			if d.ID == req.Id {
				return &v1.GetAgentResponse{Agent: s.agentProto(ctx, d, sc.proto(), false)}, nil
			}
		}
		return nil, connect.Errorf(connect.CodeNotFound, "agent not found")
	}
	a, err := s.store.GetAgent(ctx, req.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if a == nil || !sc.covers(a.ScopeKind, a.ScopeID) {
		return nil, connect.Errorf(connect.CodeNotFound, "agent not found")
	}
	d := agents.DefinitionFromRow(a)
	return &v1.GetAgentResponse{Agent: s.agentProto(ctx, d, sc.servingScope(a.ScopeKind), a.ScopeKind != sc.kind || a.ScopeID != sc.id)}, nil
}

func (s *AgentsService) CreateAgent(ctx context.Context, req *v1.CreateAgentRequest) (*v1.CreateAgentResponse, error) {
	sc, err := s.agentsScope(ctx, req.Scope, true)
	if err != nil {
		return nil, err
	}
	user := auth.UserFromContext(ctx)
	a, err := s.agents.CreateAgent(ctx, sc.kind, sc.id, agentSpecFromProto(req.Spec), user.ID)
	if err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.CreateAgentResponse{Agent: s.agentProto(ctx, agents.DefinitionFromRow(a), sc.proto(), false)}, nil
}

func (s *AgentsService) UpdateAgent(ctx context.Context, req *v1.UpdateAgentRequest) (*v1.UpdateAgentResponse, error) {
	sc, a, err := s.scopedAgent(ctx, req.Scope, req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.agents.UpdateAgent(ctx, a, agentSpecFromProto(req.Spec)); err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.UpdateAgentResponse{Agent: s.agentProto(ctx, agents.DefinitionFromRow(a), sc.proto(), false)}, nil
}

func (s *AgentsService) DeleteAgent(ctx context.Context, req *v1.DeleteAgentRequest) (*v1.DeleteAgentResponse, error) {
	_, a, err := s.scopedAgent(ctx, req.Scope, req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.agents.DeleteAgent(ctx, a); err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.DeleteAgentResponse{}, nil
}

func agentSpecFromProto(in *v1.AgentSpec) agents.AgentSpec {
	if in == nil {
		return agents.AgentSpec{}
	}
	return agents.AgentSpec{
		Slug: in.Slug, Name: in.Name, Description: in.Description, Kind: agents.KindFromProto(in.Kind), ProviderID: in.ProviderId, Model: in.Model,
		Instructions: in.Instructions, Tools: in.Tools, Triggers: in.Triggers, MaxTurns: int(in.MaxTurns), MaxTokens: in.MaxTokens, Temperature: in.Temperature,
		Approval: agents.ApprovalFromProto(in.Approval), Enabled: in.Enabled, Concurrency: int(in.Concurrency), AllowedCallerIDs: in.AllowedCallerIds,
	}
}

func (s *AgentsService) agentProto(ctx context.Context, d *agents.Definition, scope *v1.ActionsScope, inherited bool) *v1.Agent {
	out := &v1.Agent{
		Id: d.ID, Scope: scope, Slug: d.Slug, Name: d.Name, Description: d.Description, Kind: agents.ProtoKind(d.Kind), ProviderId: d.ProviderID, Model: d.Model,
		Instructions: d.Instructions, Tools: d.Tools, Triggers: d.Triggers, MaxTurns: int32(d.MaxTurns), MaxTokens: d.MaxTokens, Temperature: d.Temperature,
		Approval: agents.ProtoApproval(d.Approval), Enabled: d.Enabled, Concurrency: int32(d.Concurrency), AllowedCallerIds: d.AllowedCallerIDs,
		Source: agents.ProtoSource(d.Source), FilePath: d.FilePath, Inherited: inherited, Error: d.Error, CreatedBy: d.CreatedBy,
		CreatedAt: timestamppb.New(d.CreatedAt), UpdatedAt: timestamppb.New(d.UpdatedAt),
	}
	if out.Tools == nil {
		out.Tools = []string{}
	}
	if out.AllowedCallerIds == nil {
		out.AllowedCallerIds = []string{}
	}
	if out.Triggers == nil {
		out.Triggers = &v1.AgentTriggers{}
	}
	if d.ProviderID != "" {
		if p, _ := s.store.GetAgentProvider(ctx, d.ProviderID); p != nil {
			out.ProviderName = p.Name
		}
	}
	if ident, _ := s.store.GetAgentIdentity(ctx, d.Slug); ident != nil {
		out.BotUserId = ident.UserID
		if u, _ := s.store.GetUserByID(ctx, ident.UserID); u != nil {
			out.BotUsername = u.Username
		}
	}
	if out.BotUsername == "" {
		out.BotUsername = d.Slug
	}
	if d.Source == agents.SourceDatabase && d.ID != "" {
		counts, last, _ := s.store.AgentSessionStats(ctx, []string{d.ID})
		out.SessionCount = counts[d.ID]
		if t, ok := last[d.ID]; ok && !t.IsZero() {
			out.LastSessionAt = timestamppb.New(t)
		}
	}
	return out
}

// ── Repository overview ──────────────────────────────────────────────────

func (s *AgentsService) GetRepoAgents(ctx context.Context, req *v1.GetRepoAgentsRequest) (*v1.GetRepoAgentsResponse, error) {
	user := auth.UserFromContext(ctx)
	repo, err := visibleGitRepo(ctx, s.store, s.manager.Access(), user, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	levels := s.manager.Access().Levels(ctx, user, repo)
	cfg := s.agents.Settings(ctx, repo)
	out := &v1.GetRepoAgentsResponse{
		Enabled: s.agents.PolicyEnabled(ctx, repo), UnitEnabled: gitrepo.UnitEnabled(repo, storage.GitUnitAgents),
		AllowCommands: cfg.GetAllowCommands(), MaxTurns: cfg.GetMaxTurns(), TokenBudget: cfg.GetTokenBudgetPerSession(), MaxSessionMinutes: cfg.GetMaxSessionMinutes(),
		MaxSessionsPerDay: cfg.GetMaxSessionsPerDay(), DefaultBranch: repo.DefaultBranch, InstructionFiles: []string{},
	}
	level := levels.Unit(storage.GitUnitAgents)
	if !out.UnitEnabled && !levels.IsOwner && !levels.CanAdmin {
		return out, nil
	}
	if level < gitrepo.LevelRead {
		return nil, connect.Errorf(connect.CodeNotFound, "repository not found")
	}
	out.CanStart = level >= gitrepo.LevelWrite && !repo.IsArchived && (user != nil && !user.IsAnonymous())
	out.CanApprove = out.CanStart
	if providers, err := s.agents.ProvidersForRepo(ctx, repo); err == nil {
		for _, p := range providers {
			if p.Enabled {
				out.HasProvider = true
				break
			}
		}
	}
	defs, err := s.agents.AgentsForRepo(ctx, repo)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	ref := &agentsScopeRef{kind: storage.ActionsScopeRepo, id: repo.ID, display: repo.Namespace + "/" + repo.Name, repo: repo}
	for _, d := range defs {
		out.Agents = append(out.Agents, s.agentProto(ctx, d, ref.servingScope(d.ScopeKind), d.ScopeKind != storage.ActionsScopeRepo))
	}
	if files := s.agents.InstructionFiles(ctx, repo); files != nil {
		out.InstructionFiles = files
	}
	out.ActiveSessions, _ = s.store.CountActiveAgentSessions(ctx, repo.ID, "")
	out.SessionsToday, _ = s.store.CountAgentSessionsSince(ctx, repo.ID, time.Now().UTC().Truncate(24*time.Hour))
	return out, nil
}

// ── Sessions ─────────────────────────────────────────────────────────────

func (s *AgentsService) StartAgentSession(ctx context.Context, req *v1.StartAgentSessionRequest) (*v1.StartAgentSessionResponse, error) {
	repo, user, err := s.agentsRepo(ctx, req.Namespace, req.Name, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	def, err := s.agents.FindAgentForRepo(ctx, repo, req.Agent)
	if err != nil {
		return nil, mapAgentsErr(err)
	}
	sess, err := s.agents.StartSession(ctx, agents.StartParams{Repo: repo, Def: def, Prompt: req.Prompt, Ref: req.Ref, IssueNumber: req.IssueNumber, Model: req.Model, Trigger: agents.TriggerManual, Actor: user})
	if err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.StartAgentSessionResponse{Session: s.sessionProto(ctx, repo, sess)}, nil
}

func (s *AgentsService) ListAgentSessions(ctx context.Context, req *v1.ListAgentSessionsRequest) (*v1.ListAgentSessionsResponse, error) {
	repo, _, err := s.agentsRepo(ctx, req.Namespace, req.Name, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	opts := stores.AgentSessionListOptions{
		RepoID: repo.ID, Status: agents.StatusFromProto(req.Status), Conclusion: agents.ConclusionFromProto(req.Conclusion),
		IssueNumber: req.IssueNumber, Actor: strings.TrimSpace(req.Actor), Trigger: strings.TrimSpace(req.Trigger), Limit: limit, Offset: offset,
	}
	if a := strings.TrimSpace(strings.TrimPrefix(req.Agent, "@")); a != "" {
		if def, err := s.agents.FindAgentForRepo(ctx, repo, a); err == nil {
			opts.AgentSlug = def.Slug
		} else {
			opts.AgentSlug = a
		}
	}
	rows, total, err := s.store.ListAgentSessions(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &v1.ListAgentSessionsResponse{Page: pages.Info(offset, limit, total)}
	for _, sess := range rows {
		out.Sessions = append(out.Sessions, s.sessionProto(ctx, repo, sess))
	}
	out.ActiveCount, _ = s.store.CountActiveAgentSessions(ctx, repo.ID, "")
	return out, nil
}

func (s *AgentsService) GetAgentSession(ctx context.Context, req *v1.GetAgentSessionRequest) (*v1.GetAgentSessionResponse, error) {
	repo, sess, _, err := s.agentsSession(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	return &v1.GetAgentSessionResponse{Session: s.sessionProto(ctx, repo, sess)}, nil
}

func (s *AgentsService) CancelAgentSession(ctx context.Context, req *v1.CancelAgentSessionRequest) (*v1.CancelAgentSessionResponse, error) {
	repo, sess, user, err := s.agentsSession(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.agents.Cancel(ctx, sess, user); err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.CancelAgentSessionResponse{Session: s.reloadSession(ctx, repo, sess)}, nil
}

func (s *AgentsService) RerunAgentSession(ctx context.Context, req *v1.RerunAgentSessionRequest) (*v1.RerunAgentSessionResponse, error) {
	repo, sess, user, err := s.agentsSession(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	fresh, err := s.agents.Rerun(ctx, repo, sess, user)
	if err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.RerunAgentSessionResponse{Session: s.sessionProto(ctx, repo, fresh)}, nil
}

func (s *AgentsService) ContinueAgentSession(ctx context.Context, req *v1.ContinueAgentSessionRequest) (*v1.ContinueAgentSessionResponse, error) {
	repo, sess, user, err := s.agentsSession(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	if err := s.agents.Continue(ctx, sess, req.Prompt, user); err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.ContinueAgentSessionResponse{Session: s.reloadSession(ctx, repo, sess)}, nil
}

func (s *AgentsService) ResolveAgentApproval(ctx context.Context, req *v1.ResolveAgentApprovalRequest) (*v1.ResolveAgentApprovalResponse, error) {
	repo, sess, user, err := s.agentsSession(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelWrite)
	if err != nil {
		return nil, err
	}
	a, err := s.store.GetAgentApproval(ctx, sess.ID, req.ApprovalId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if a == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "approval not found")
	}
	if err := s.agents.ResolveApproval(ctx, sess, a, req.Approve, req.Note, user); err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.ResolveAgentApprovalResponse{Session: s.reloadSession(ctx, repo, sess)}, nil
}

func (s *AgentsService) ListAgentSessionEvents(ctx context.Context, req *v1.ListAgentSessionEventsRequest) (*v1.ListAgentSessionEventsResponse, error) {
	_, sess, _, err := s.agentsSession(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelRead)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Page)
	events, err := s.store.ListAgentSessionEvents(ctx, sess.ID, int64(offset), limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	total, _ := s.store.CountAgentSessionEvents(ctx, sess.ID)
	out := &v1.ListAgentSessionEventsResponse{Page: pages.Info(offset, limit, total)}
	for _, ev := range events {
		out.Events = append(out.Events, eventProto(ev, sess.Status, false))
	}
	return out, nil
}

func (s *AgentsService) StreamAgentSessionEvents(ctx context.Context, req *v1.StreamAgentSessionEventsRequest, stream distrofacev1connect.AgentsServiceStreamAgentSessionEventsServerStream) error {
	_, sess, _, err := s.agentsSession(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelRead)
	if err != nil {
		return err
	}
	after := req.AfterSeq
	if after < 0 {
		after = 0
	}
	live, unsubscribe := s.agents.Subscribe(sess.ID)
	defer unsubscribe()
	flush := func() error {
		events, err := s.store.ListAgentSessionEvents(ctx, sess.ID, after, 0)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		for _, ev := range events {
			if err := stream.Send(eventProto(ev, sess.Status, false)); err != nil {
				return err
			}
			after = ev.Seq
		}
		return nil
	}
	if err := flush(); err != nil {
		return err
	}
	ticker := time.NewTicker(agentsStreamPoll)
	defer ticker.Stop()
	for {
		if sess.Status == agents.StatusCompleted {
			if err := flush(); err != nil {
				return err
			}
			return stream.Send(&v1.AgentSessionEvent{Seq: after, Kind: v1.AgentEventKind_AGENT_EVENT_KIND_STATUS, Status: agents.ProtoStatus(sess.Status), Done: true, CreatedAt: timestamppb.Now()})
		}
		select {
		case <-ctx.Done():
			return nil
		case ev := <-live:
			if ev != nil && ev.Seq > after {
				if err := flush(); err != nil {
					return err
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				return err
			}
		}
		fresh, err := s.store.GetAgentSessionByID(ctx, sess.ID)
		if err != nil || fresh == nil {
			return stream.Send(&v1.AgentSessionEvent{Seq: after, Kind: v1.AgentEventKind_AGENT_EVENT_KIND_STATUS, Done: true, CreatedAt: timestamppb.Now()})
		}
		sess = fresh
	}
}

func (s *AgentsService) DeleteAgentSession(ctx context.Context, req *v1.DeleteAgentSessionRequest) (*v1.DeleteAgentSessionResponse, error) {
	_, sess, _, err := s.agentsSession(ctx, req.Namespace, req.Name, req.Id, gitrepo.LevelAdmin)
	if err != nil {
		return nil, err
	}
	if err := s.agents.Delete(ctx, sess); err != nil {
		return nil, mapAgentsErr(err)
	}
	return &v1.DeleteAgentSessionResponse{}, nil
}

func (s *AgentsService) GetAgentUsage(ctx context.Context, req *v1.GetAgentUsageRequest) (*v1.GetAgentUsageResponse, error) {
	sc, err := s.agentsScope(ctx, req.Scope, false)
	if err != nil {
		return nil, err
	}
	repoIDs, err := s.agents.ScopeRepoIDs(ctx, sc.kind, sc.id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out, err := s.agents.Usage(ctx, repoIDs, int(req.Days))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return out, nil
}

// ── Access helpers ───────────────────────────────────────────────────────

// Repository at a level on the agents unit, anything above read is a mutation
func (s *AgentsService) agentsRepo(ctx context.Context, ns, name string, need gitrepo.Level) (*storage.GitRepository, *auth.AuthenticatedUser, error) {
	user := auth.UserFromContext(ctx)
	repo, err := visibleGitRepo(ctx, s.store, s.manager.Access(), user, ns, name)
	if err != nil {
		return nil, nil, err
	}
	if need > gitrepo.LevelRead && (user == nil || user.IsAnonymous()) {
		return nil, nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if need > gitrepo.LevelRead {
		if err := portalAllowsGitWrite(ctx); err != nil {
			return nil, nil, err
		}
	}
	levels := s.manager.Access().Levels(ctx, user, repo)
	if !gitrepo.UnitEnabled(repo, storage.GitUnitAgents) && !levels.IsOwner && !levels.CanAdmin {
		return nil, nil, connect.Errorf(connect.CodeFailedPrecondition, "agents are disabled on this repository")
	}
	if levels.Unit(storage.GitUnitAgents) < need {
		if need <= gitrepo.LevelRead {
			return nil, nil, connect.Errorf(connect.CodeNotFound, "repository not found")
		}
		return nil, nil, connect.Errorf(connect.CodePermissionDenied, "access denied")
	}
	if need > gitrepo.LevelRead && repo.IsArchived {
		return nil, nil, connect.Errorf(connect.CodeFailedPrecondition, "repository is archived")
	}
	return repo, user, nil
}

func (s *AgentsService) agentsSession(ctx context.Context, ns, name, id string, need gitrepo.Level) (*storage.GitRepository, *storage.AgentSession, *auth.AuthenticatedUser, error) {
	repo, user, err := s.agentsRepo(ctx, ns, name, need)
	if err != nil {
		return nil, nil, nil, err
	}
	if id == "" {
		return nil, nil, nil, connect.Errorf(connect.CodeInvalidArgument, "session id is required")
	}
	sess, err := s.store.GetAgentSession(ctx, repo.ID, id)
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if sess == nil {
		return nil, nil, nil, connect.Errorf(connect.CodeNotFound, "session not found")
	}
	return repo, sess, user, nil
}

// Git manage permission stands in for system administration
func (s *AgentsService) systemAdmin(user *auth.AuthenticatedUser) bool {
	if user == nil || user.IsAnonymous() || user.IsScoped() {
		return false
	}
	return s.manager.Access().ListOptions(user, "").IncludePrivate
}

// Resolved connector or agent scope with the caller admitted
type agentsScopeRef struct {
	kind    string
	id      string
	display string
	repo    *storage.GitRepository
	orgID   string
}

func (r *agentsScopeRef) proto() *v1.ActionsScope {
	return &v1.ActionsScope{Kind: r.kind, Id: r.display}
}

// Scope shown for a row serving this scope from any level
func (r *agentsScopeRef) servingScope(kind string) *v1.ActionsScope {
	switch kind {
	case storage.ActionsScopeRepo:
		return r.proto()
	case storage.ActionsScopeOrg:
		if r.kind == storage.ActionsScopeRepo && r.repo != nil {
			return &v1.ActionsScope{Kind: kind, Id: r.repo.Namespace}
		}
		return &v1.ActionsScope{Kind: kind, Id: r.display}
	}
	return &v1.ActionsScope{Kind: storage.ActionsScopeSystem}
}

// Whether a row at a scope is visible from this one
func (r *agentsScopeRef) covers(kind, id string) bool {
	switch kind {
	case storage.ActionsScopeSystem:
		return true
	case storage.ActionsScopeOrg:
		return (r.kind == storage.ActionsScopeOrg && r.id == id) || (r.kind == storage.ActionsScopeRepo && r.orgID == id)
	case storage.ActionsScopeRepo:
		return r.kind == storage.ActionsScopeRepo && r.id == id
	}
	return false
}

// Requires administration of the scope the request names
func (s *AgentsService) agentsScope(ctx context.Context, scope *v1.ActionsScope, mutate bool) (*agentsScopeRef, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || user.IsAnonymous() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if mutate {
		if err := portalAllowsGitWrite(ctx); err != nil {
			return nil, err
		}
	}
	kind := strings.ToLower(strings.TrimSpace(scope.GetKind()))
	id := strings.TrimSpace(scope.GetId())
	if kind == "" {
		kind = storage.ActionsScopeSystem
	}
	switch kind {
	case storage.ActionsScopeSystem:
		if !s.systemAdmin(user) {
			return nil, connect.Errorf(connect.CodePermissionDenied, "system administration required")
		}
		return &agentsScopeRef{kind: kind}, nil
	case storage.ActionsScopeOrg:
		if id == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "organization is required")
		}
		org, err := s.store.GetOrganization(ctx, id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		if org == nil {
			org, err = s.store.GetOrganizationByID(ctx, id)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
			}
		}
		if org == nil {
			return nil, connect.Errorf(connect.CodeNotFound, "organization not found")
		}
		if !s.systemAdmin(user) {
			member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
			if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
				return nil, connect.Errorf(connect.CodePermissionDenied, "organization administration required")
			}
		}
		return &agentsScopeRef{kind: kind, id: org.ID, display: org.ID, orgID: org.ID}, nil
	case storage.ActionsScopeRepo:
		ns, name, ok := strings.Cut(id, "/")
		if !ok || ns == "" || name == "" {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "repository scope id must be namespace/name")
		}
		repo, err := visibleGitRepo(ctx, s.store, s.manager.Access(), user, ns, name)
		if err != nil {
			return nil, err
		}
		levels := s.manager.Access().Levels(ctx, user, repo)
		if !levels.IsOwner && !levels.CanAdmin {
			return nil, connect.Errorf(connect.CodePermissionDenied, "repository administration required")
		}
		if mutate && repo.IsArchived {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "repository is archived")
		}
		orgID := ""
		if repo.IsOrgNamespace {
			if org, _ := s.store.GetOrganization(ctx, repo.Namespace); org != nil {
				orgID = org.ID
			}
		}
		return &agentsScopeRef{kind: kind, id: repo.ID, display: repo.Namespace + "/" + repo.Name, repo: repo, orgID: orgID}, nil
	}
	return nil, connect.Errorf(connect.CodeInvalidArgument, "unknown scope kind %q", kind)
}

func (s *AgentsService) scopedProvider(ctx context.Context, scope *v1.ActionsScope, id string, mutate bool) (*agentsScopeRef, *storage.AgentProvider, error) {
	sc, err := s.agentsScope(ctx, scope, mutate)
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		return nil, nil, connect.Errorf(connect.CodeInvalidArgument, "connector id is required")
	}
	p, err := s.store.GetAgentProvider(ctx, id)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if p == nil || !sc.covers(p.ScopeKind, p.ScopeID) {
		return nil, nil, connect.Errorf(connect.CodeNotFound, "connector not found")
	}
	return sc, p, nil
}

func (s *AgentsService) scopedAgent(ctx context.Context, scope *v1.ActionsScope, id string) (*agentsScopeRef, *storage.Agent, error) {
	sc, err := s.agentsScope(ctx, scope, true)
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		return nil, nil, connect.Errorf(connect.CodeInvalidArgument, "agent id is required")
	}
	if strings.HasPrefix(id, "file:") {
		return nil, nil, connect.Errorf(connect.CodeFailedPrecondition, "file defined agents are edited in the repository")
	}
	a, err := s.store.GetAgent(ctx, id)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if a == nil || a.ScopeKind != sc.kind || a.ScopeID != sc.id {
		return nil, nil, connect.Errorf(connect.CodeNotFound, "agent not found")
	}
	return sc, a, nil
}

// ── Proto mapping ────────────────────────────────────────────────────────

func (s *AgentsService) reloadSession(ctx context.Context, repo *storage.GitRepository, sess *storage.AgentSession) *v1.AgentSession {
	if fresh, _ := s.store.GetAgentSession(ctx, repo.ID, sess.ID); fresh != nil {
		sess = fresh
	}
	return s.sessionProto(ctx, repo, sess)
}

func (s *AgentsService) sessionProto(ctx context.Context, repo *storage.GitRepository, sess *storage.AgentSession) *v1.AgentSession {
	out := &v1.AgentSession{
		Id: sess.ID, RepoFullName: repo.Namespace + "/" + repo.Name, Number: sess.Number, AgentId: sess.AgentID, AgentSlug: sess.AgentSlug, AgentName: sess.AgentName,
		AgentKind: agents.ProtoKind(sess.AgentKind), ProviderName: sess.ProviderName, Model: sess.Model, Trigger: sess.Trigger, Prompt: sess.Prompt,
		IssueNumber: sess.IssueNumber, IsPull: sess.IsPull, Ref: sess.Ref, Sha: sess.SHA, Branch: sess.Branch,
		Status: agents.ProtoStatus(sess.Status), Conclusion: agents.ProtoConclusion(sess.Conclusion), Summary: sess.Summary, Error: sess.Error,
		Turns: int32(sess.Turns), ToolCalls: int32(sess.ToolCalls), InputTokens: sess.InputTokens, OutputTokens: sess.OutputTokens,
		PromptTokens: sess.PromptTokens, ContextTokens: sess.ContextTokens, CacheReadTokens: sess.CacheReadTokens, CacheWriteTokens: sess.CacheWriteTokens,
		ResultPullNumber: sess.ResultPullNumber, ResultReviewId: sess.ResultReviewID, ResultCommits: decodeStringList(sess.ResultCommits),
		HtmlUrl: s.manager.HTMLURL(ctx, repo) + "/agents/sessions/" + sess.ID, MaxTurns: int32(sess.MaxTurns), MaxTokens: sess.MaxTokens, Title: sess.Title,
		EventCount: sess.EventCount, CreatedAt: timestamppb.New(sess.CreatedAt), UpdatedAt: timestamppb.New(sess.UpdatedAt),
	}
	if out.ResultCommits == nil {
		out.ResultCommits = []string{}
	}
	if sess.TriggerUserID != "" {
		if u, _ := s.store.GetUserByID(ctx, sess.TriggerUserID); u != nil {
			out.TriggerUser = u.Username
		}
	}
	if sess.BotUserID != "" {
		if u, _ := s.store.GetUserByID(ctx, sess.BotUserID); u != nil {
			out.BotUsername = u.Username
		}
	}
	if sess.StartedAt != nil {
		out.StartedAt = timestamppb.New(*sess.StartedAt)
	}
	if sess.FinishedAt != nil {
		out.FinishedAt = timestamppb.New(*sess.FinishedAt)
	}
	if sess.Status == agents.StatusWaitingApproval {
		if a, _ := s.store.GetPendingAgentApproval(ctx, sess.ID); a != nil {
			out.PendingApproval = s.approvalProto(ctx, a)
		}
	}
	return out
}

func (s *AgentsService) approvalProto(ctx context.Context, a *storage.AgentApproval) *v1.AgentApproval {
	out := &v1.AgentApproval{Id: a.ID, Tool: a.Tool, Title: a.Title, Arguments: a.Arguments, Status: agents.ProtoApprovalStatus(a.Status), Note: a.Note, EventSeq: a.EventSeq, CreatedAt: timestamppb.New(a.CreatedAt)}
	if a.DecidedBy != "" {
		if u, _ := s.store.GetUserByID(ctx, a.DecidedBy); u != nil {
			out.DecidedBy = u.Username
		}
	}
	if a.DecidedAt != nil {
		out.DecidedAt = timestamppb.New(*a.DecidedAt)
	}
	return out
}

func eventProto(ev *storage.AgentSessionEvent, status string, done bool) *v1.AgentSessionEvent {
	return &v1.AgentSessionEvent{
		Seq: ev.Seq, Kind: agents.ProtoEventKind(ev.Kind), Text: ev.Text, Tool: ev.Tool, ToolCallId: ev.ToolCallID, Data: ev.Data,
		CreatedAt: timestamppb.New(ev.CreatedAt), Status: agents.ProtoStatus(status), Done: done,
	}
}

// Maps agents domain errors onto connect codes
func mapAgentsErr(err error) error {
	if err == nil {
		return nil
	}
	var cerr *connect.Error
	switch {
	case errors.As(err, &cerr):
		return err
	case errors.Is(err, agents.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, agents.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, agents.ErrDenied):
		return connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	case errors.Is(err, agents.ErrConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, agents.ErrDisabled):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, agents.ErrBudget):
		return connect.NewError(connect.CodeResourceExhausted, err.Error()).WithCause(err)
	}
	return mapForgeErr(err)
}
