package agents

import (
	"context"
	"encoding/json"
	"fmt"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nickheyer/distroface/internal/db"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/utils"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)

// Directories searched for file defined agents, every hit counts
var AgentDirs = []string{".distroface/agents", ".github/agents"}

// Files whose contents every session reads as repository instructions
var InstructionPaths = []string{"AGENTS.md", ".distroface/AGENTS.md", ".distroface/agents/instructions.md", ".github/copilot-instructions.md"}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

// Definition is an agent as a session sees it, from a row or a file
type Definition struct {
	ID               string            `json:"id"`
	ScopeKind        string            `json:"scope_kind"`
	ScopeID          string            `json:"scope_id"`
	Slug             string            `json:"slug"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Kind             string            `json:"kind"`
	ProviderID       string            `json:"provider_id,omitempty"`
	Model            string            `json:"model,omitempty"`
	Instructions     string            `json:"instructions,omitempty"`
	Tools            []string          `json:"tools,omitempty"`
	Triggers         *v1.AgentTriggers `json:"-"`
	TriggersJSON     string            `json:"triggers,omitempty"`
	MaxTurns         int               `json:"max_turns,omitempty"`
	MaxTokens        int64             `json:"max_tokens,omitempty"`
	Temperature      *float64          `json:"temperature,omitempty"`
	Approval         string            `json:"approval"`
	Enabled          bool              `json:"enabled"`
	Concurrency      int               `json:"concurrency,omitempty"`
	AllowedCallerIDs []string          `json:"allowed_caller_ids,omitempty"`
	Source           string            `json:"source"`
	FilePath         string            `json:"file_path,omitempty"`
	Error            string            `json:"error,omitempty"`
	CreatedBy        string            `json:"created_by,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// Fields of a new or changed agent
type AgentSpec struct {
	Slug             string
	Name             string
	Description      string
	Kind             string
	ProviderID       string
	Model            string
	Instructions     string
	Tools            []string
	Triggers         *v1.AgentTriggers
	MaxTurns         int
	MaxTokens        int64
	Temperature      *float64
	Approval         string
	Enabled          bool
	Concurrency      int
	AllowedCallerIDs []string
}

// Tools the agent may call, the kind default when none are listed
func (d *Definition) ToolNames() []string {
	if len(d.Tools) > 0 {
		return d.Tools
	}
	return DefaultTools(d.Kind)
}

// Snapshot stored on sessions so later edits do not change a running agent
func (d *Definition) marshal() string {
	c := *d
	if d.Triggers != nil {
		b, _ := protojson.Marshal(d.Triggers)
		c.TriggersJSON = string(b)
	}
	raw, _ := json.Marshal(&c)
	return string(raw)
}

func definitionFromJSON(raw string) *Definition {
	var d Definition
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil
	}
	d.Triggers = decodeTriggers(d.TriggersJSON)
	return &d
}

func decodeTriggers(raw string) *v1.AgentTriggers {
	t := &v1.AgentTriggers{}
	if raw != "" && raw != "{}" {
		_ = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(raw), t)
	}
	return t
}

// DefinitionFromRow lifts a stored agent into a definition
func DefinitionFromRow(a *db.Agent) *Definition {
	d := &Definition{
		ID: a.ID, ScopeKind: a.ScopeKind, ScopeID: a.ScopeID, Slug: a.Slug, Name: a.Name, Description: a.Description, Kind: a.Kind,
		ProviderID: a.ProviderID, Model: a.Model, Instructions: a.Instructions, Tools: decodeList(a.Tools), Triggers: decodeTriggers(a.Triggers),
		MaxTurns: a.MaxTurns, MaxTokens: a.MaxTokens, Approval: a.Approval, Enabled: a.Enabled, Concurrency: a.Concurrency,
		AllowedCallerIDs: decodeList(a.AllowedCallerIDs), Source: SourceDatabase, CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
	if a.Temperature >= 0 {
		t := a.Temperature
		d.Temperature = &t
	}
	return d
}

func decodeList(raw string) []string {
	var out []string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func encodeList(list []string) string {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

// ValidSlug reports whether a handle may name an agent
func ValidSlug(slug string) bool {
	return slugPattern.MatchString(slug) && utils.NamespaceAllowed(slug)
}

func validKind(kind string) bool {
	switch kind {
	case KindReview, KindCode, KindChat, KindTriage, KindCustom:
		return true
	}
	return false
}

func validApproval(a string) bool {
	switch a {
	case ApprovalNone, ApprovalCommits, ApprovalAllWrites:
		return true
	}
	return false
}

// Validates a spec against the scope and applies it onto a row
func (m *Manager) applyAgentSpec(ctx context.Context, a *db.Agent, spec AgentSpec) error {
	slug := strings.ToLower(strings.TrimSpace(spec.Slug))
	if !ValidSlug(slug) {
		return fmt.Errorf("%w: handle must be 3 to 40 lower case letters, digits, or hyphens and not a reserved name", ErrInvalid)
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" || len(name) > 100 {
		return fmt.Errorf("%w: a name is required", ErrInvalid)
	}
	if !validKind(spec.Kind) {
		return fmt.Errorf("%w: unknown agent kind %q", ErrInvalid, spec.Kind)
	}
	approval := spec.Approval
	if approval == "" {
		approval = ApprovalNone
	}
	if !validApproval(approval) {
		return fmt.Errorf("%w: unknown approval policy %q", ErrInvalid, approval)
	}
	if spec.MaxTurns < 0 || spec.MaxTokens < 0 || spec.Concurrency < 0 {
		return fmt.Errorf("%w: limits cannot be negative", ErrInvalid)
	}
	if spec.Temperature != nil && (*spec.Temperature < 0 || *spec.Temperature > 2) {
		return fmt.Errorf("%w: temperature must be between 0 and 2", ErrInvalid)
	}
	if len(spec.Instructions) > 64<<10 {
		return fmt.Errorf("%w: instructions are too long", ErrInvalid)
	}
	tools := []string{}
	for _, t := range spec.Tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := toolRegistry[t]; !ok {
			return fmt.Errorf("%w: unknown tool %q", ErrInvalid, t)
		}
		tools = append(tools, t)
	}
	if spec.ProviderID != "" {
		p, err := m.store.GetAgentProvider(ctx, spec.ProviderID)
		if err != nil {
			return err
		}
		if p == nil || !providerVisibleToScope(p, a.ScopeKind, a.ScopeID, m.repoOrgOf(ctx, a)) {
			return fmt.Errorf("%w: connector is not available at this scope", ErrNotFound)
		}
	}
	if existing, _ := m.store.GetAgentBySlug(ctx, a.ScopeKind, a.ScopeID, slug); existing != nil && existing.ID != a.ID {
		return fmt.Errorf("%w: an agent named %s already exists at this scope", ErrConflict, slug)
	}
	// The handle doubles as a bot username, a human account must not own it
	if a.Slug != slug {
		if ident, _ := m.store.GetAgentIdentity(ctx, slug); ident == nil {
			if u, _ := m.store.GetUserByUsername(ctx, slug); u != nil && u.AuthProvider != db.AuthProviderAgent {
				return fmt.Errorf("%w: %s is taken by a user account", ErrConflict, slug)
			}
			if org, _ := m.store.GetOrganization(ctx, slug); org != nil {
				return fmt.Errorf("%w: %s is taken by an organization", ErrConflict, slug)
			}
		}
	}
	triggers := spec.Triggers
	if triggers == nil {
		triggers = &v1.AgentTriggers{}
	}
	trig, _ := protojson.Marshal(triggers)
	a.Slug, a.Name, a.Description, a.Kind, a.ProviderID, a.Model = slug, name, strings.TrimSpace(spec.Description), spec.Kind, spec.ProviderID, strings.TrimSpace(spec.Model)
	a.Instructions, a.Tools, a.Triggers, a.MaxTurns, a.MaxTokens = spec.Instructions, encodeList(tools), string(trig), spec.MaxTurns, spec.MaxTokens
	a.Temperature = -1
	if spec.Temperature != nil {
		a.Temperature = *spec.Temperature
	}
	a.Approval, a.Enabled, a.Concurrency, a.AllowedCallerIDs = approval, spec.Enabled, spec.Concurrency, encodeList(spec.AllowedCallerIDs)
	return nil
}

// Org behind an agent's scope, for connector visibility
func (m *Manager) repoOrgOf(ctx context.Context, a *db.Agent) string {
	switch a.ScopeKind {
	case db.ActionsScopeOrg:
		return a.ScopeID
	case db.ActionsScopeRepo:
		repo, _ := m.store.GetGitRepositoryByID(ctx, a.ScopeID)
		return m.repoOrgID(ctx, repo)
	}
	return ""
}

// A connector serves a scope when it sits at that scope or above it
func providerVisibleToScope(p *db.AgentProvider, scopeKind, scopeID, orgID string) bool {
	switch p.ScopeKind {
	case db.ActionsScopeSystem:
		return true
	case db.ActionsScopeOrg:
		return (scopeKind == db.ActionsScopeOrg && scopeID == p.ScopeID) || (scopeKind == db.ActionsScopeRepo && orgID == p.ScopeID)
	case db.ActionsScopeRepo:
		return scopeKind == db.ActionsScopeRepo && scopeID == p.ScopeID
	}
	return false
}

// CreateAgent defines an agent and provisions its bot identity
func (m *Manager) CreateAgent(ctx context.Context, scopeKind, scopeID string, spec AgentSpec, createdBy string) (*db.Agent, error) {
	if err := ValidScope(scopeKind, scopeID); err != nil {
		return nil, err
	}
	if scopeKind == db.ActionsScopeRepo && !m.ScopeSettings(ctx, scopeKind, scopeID).GetAllowRepoAgents() {
		return nil, fmt.Errorf("%w: repository agents are disabled by policy", ErrDenied)
	}
	a := &db.Agent{ScopeKind: scopeKind, ScopeID: scopeID, CreatedBy: createdBy}
	if err := m.applyAgentSpec(ctx, a, spec); err != nil {
		return nil, err
	}
	if err := m.store.CreateAgent(ctx, a); err != nil {
		return nil, err
	}
	if _, err := m.Identity(ctx, DefinitionFromRow(a)); err != nil {
		m.log.Error("agents: identity for %s: %v", a.Slug, err)
	}
	return a, nil
}

// UpdateAgent replaces an agent's fields, renaming its bot when the handle changes
func (m *Manager) UpdateAgent(ctx context.Context, a *db.Agent, spec AgentSpec) error {
	if err := m.applyAgentSpec(ctx, a, spec); err != nil {
		return err
	}
	if err := m.store.UpdateAgent(ctx, a); err != nil {
		return err
	}
	if _, err := m.Identity(ctx, DefinitionFromRow(a)); err != nil {
		m.log.Error("agents: identity for %s: %v", a.Slug, err)
	}
	return nil
}

// DeleteAgent removes a definition, sessions keep their snapshot
func (m *Manager) DeleteAgent(ctx context.Context, a *db.Agent) error {
	if err := m.store.DeleteAgent(ctx, a.ID); err != nil {
		return err
	}
	m.retireIdentity(ctx, a.Slug)
	return nil
}

// AgentsForRepo lists agents usable by a repository, nearest scope wins per handle
func (m *Manager) AgentsForRepo(ctx context.Context, repo *db.GitRepository) ([]*Definition, error) {
	rows, err := m.store.ListAgentsForRepo(ctx, repo.ID, m.repoOrgID(ctx, repo))
	if err != nil {
		return nil, err
	}
	cfg := m.Settings(ctx, repo)
	var out []*Definition
	seen := map[string]bool{}
	add := func(d *Definition) {
		if seen[d.Slug] {
			return
		}
		seen[d.Slug] = true
		out = append(out, d)
	}
	for _, a := range rows {
		if a.ScopeKind == db.ActionsScopeRepo && !cfg.GetAllowRepoAgents() {
			continue
		}
		if a.ScopeKind == db.ActionsScopeRepo {
			add(DefinitionFromRow(a))
		}
	}
	if cfg.GetAllowFileAgents() && cfg.GetAllowRepoAgents() {
		for _, d := range m.loadFileAgents(ctx, repo) {
			add(d)
		}
	}
	for _, a := range rows {
		if a.ScopeKind != db.ActionsScopeRepo {
			add(DefinitionFromRow(a))
		}
	}
	return out, nil
}

// FindAgentForRepo resolves an agent by id or handle among those usable by a repository
func (m *Manager) FindAgentForRepo(ctx context.Context, repo *db.GitRepository, idOrSlug string) (*Definition, error) {
	idOrSlug = strings.TrimSpace(strings.TrimPrefix(idOrSlug, "@"))
	if idOrSlug == "" {
		return nil, fmt.Errorf("%w: an agent is required", ErrInvalid)
	}
	defs, err := m.AgentsForRepo(ctx, repo)
	if err != nil {
		return nil, err
	}
	for _, d := range defs {
		if d.ID == idOrSlug || strings.EqualFold(d.Slug, idOrSlug) {
			return d, nil
		}
	}
	return nil, fmt.Errorf("%w: agent %s", ErrNotFound, idOrSlug)
}

// FileAgents lists the agents declared in repository files
func (m *Manager) FileAgents(ctx context.Context, repo *db.GitRepository) []*Definition {
	return m.loadFileAgents(ctx, repo)
}

// Reads every agent file on the default branch
func (m *Manager) loadFileAgents(ctx context.Context, repo *db.GitRepository) []*Definition {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil
	}
	defer r.Close()
	commit, err := r.Resolve(repo.DefaultBranch)
	if err != nil {
		return nil
	}
	var providers []*db.AgentProvider
	var out []*Definition
	for _, dir := range AgentDirs {
		entries, _, err := r.Tree(ctx, commit, dir, false)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Mode.IsFile() || !strings.HasSuffix(e.Name, ".md") {
				continue
			}
			p := pathpkg.Join(dir, e.Name)
			blob, err := r.Blob(commit, p, 0)
			if err != nil || blob.Truncated || blob.IsBinary {
				continue
			}
			d, err := ParseAgentFile(p, string(blob.Content))
			if d == nil {
				continue
			}
			if err != nil {
				d.Error = err.Error()
			}
			d.ScopeKind, d.ScopeID, d.CreatedAt, d.UpdatedAt = db.ActionsScopeRepo, repo.ID, repo.UpdatedAt, repo.UpdatedAt
			d.ID = "file:" + d.Slug
			// Provider names resolve against what the repository can reach
			if ref := d.ProviderID; ref != "" {
				if providers == nil {
					providers, _ = m.ProvidersForRepo(ctx, repo)
				}
				d.ProviderID = ""
				for _, prov := range providers {
					if strings.EqualFold(prov.Name, ref) || prov.ID == ref {
						d.ProviderID = prov.ID
						break
					}
				}
				if d.ProviderID == "" && d.Error == "" {
					d.Error = fmt.Sprintf("connector %q not found, the default connector is used", ref)
				}
			}
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// Front matter of an agent file
type agentFile struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Kind        string            `yaml:"kind"`
	Provider    string            `yaml:"provider"`
	Model       string            `yaml:"model"`
	Tools       []string          `yaml:"tools"`
	Approval    string            `yaml:"approval"`
	MaxTurns    int               `yaml:"max_turns"`
	MaxTokens   int64             `yaml:"max_tokens"`
	Temperature *float64          `yaml:"temperature"`
	Enabled     *bool             `yaml:"enabled"`
	Concurrency int               `yaml:"concurrency"`
	Triggers    agentFileTriggers `yaml:"triggers"`
}

type agentFileTriggers struct {
	PullRequest     []string `yaml:"pull_request"`
	ReviewRequested bool     `yaml:"review_requested"`
	IssueAssigned   bool     `yaml:"issue_assigned"`
	Labels          []string `yaml:"labels"`
	Mention         bool     `yaml:"mention"`
	ReviewFeedback  bool     `yaml:"review_feedback"`
	WorkflowFailure bool     `yaml:"workflow_failure"`
	BaseBranches    []string `yaml:"base_branches"`
	Paths           []string `yaml:"paths"`
	PathsIgnore     []string `yaml:"paths_ignore"`
	IncludeDrafts   bool     `yaml:"include_drafts"`
	RespondToBots   bool     `yaml:"respond_to_bots"`
}

// ParseAgentFile reads a markdown agent file with yaml front matter, the body is the instructions
func ParseAgentFile(path, source string) (*Definition, error) {
	base := strings.TrimSuffix(strings.TrimSuffix(pathpkg.Base(path), ".md"), ".agent")
	slug := utils.SlugUsername(base)
	d := &Definition{Slug: slug, Name: base, Kind: KindCustom, Approval: ApprovalCommits, Enabled: true, Source: SourceFile, FilePath: path, Triggers: &v1.AgentTriggers{}}
	front, body, hasFront := splitFrontMatter(source)
	d.Instructions = strings.TrimSpace(body)
	if !ValidSlug(slug) {
		return d, fmt.Errorf("file name %q does not make a valid agent handle", base)
	}
	if !hasFront {
		if d.Instructions == "" {
			return nil, nil
		}
		return d, nil
	}
	var meta agentFile
	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		return d, fmt.Errorf("front matter: %v", err)
	}
	if n := strings.TrimSpace(meta.Name); n != "" {
		d.Name = n
	}
	d.Description = strings.TrimSpace(meta.Description)
	if k := strings.ToLower(strings.TrimSpace(meta.Kind)); k != "" {
		if !validKind(k) {
			return d, fmt.Errorf("unknown kind %q", k)
		}
		d.Kind = k
	}
	d.ProviderID = strings.TrimSpace(meta.Provider)
	d.Model = strings.TrimSpace(meta.Model)
	for _, t := range meta.Tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := toolRegistry[t]; !ok {
			return d, fmt.Errorf("unknown tool %q", t)
		}
		d.Tools = append(d.Tools, t)
	}
	if a := strings.ToLower(strings.TrimSpace(meta.Approval)); a != "" {
		if !validApproval(a) {
			return d, fmt.Errorf("unknown approval policy %q", a)
		}
		d.Approval = a
	}
	d.MaxTurns, d.MaxTokens, d.Temperature, d.Concurrency = meta.MaxTurns, meta.MaxTokens, meta.Temperature, meta.Concurrency
	if meta.Enabled != nil {
		d.Enabled = *meta.Enabled
	}
	t := meta.Triggers
	for _, action := range t.PullRequest {
		switch strings.ToLower(strings.TrimSpace(action)) {
		case "opened":
			d.Triggers.PullRequestOpened = true
		case "synchronize":
			d.Triggers.PullRequestSynchronize = true
		case "ready_for_review":
			d.Triggers.PullRequestReadyForReview = true
		}
	}
	d.Triggers.ReviewRequested, d.Triggers.IssueAssigned, d.Triggers.IssueLabels = t.ReviewRequested, t.IssueAssigned, t.Labels
	d.Triggers.Mention, d.Triggers.ReviewFeedback, d.Triggers.WorkflowFailure = t.Mention, t.ReviewFeedback, t.WorkflowFailure
	d.Triggers.BaseBranches, d.Triggers.Paths, d.Triggers.PathsIgnore = t.BaseBranches, t.Paths, t.PathsIgnore
	d.Triggers.IncludeDrafts, d.Triggers.RespondToBots = t.IncludeDrafts, t.RespondToBots
	return d, nil
}

// Splits leading yaml front matter from a markdown body
func splitFrontMatter(src string) (front, body string, ok bool) {
	src = strings.TrimPrefix(src, "\xef\xbb\xbf")
	if !strings.HasPrefix(src, "---") {
		return "", src, false
	}
	rest := src[3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		return "", src, false
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		if strings.HasPrefix(rest, "---") {
			return "", rest[3:], true
		}
		return "", src, false
	}
	front = rest[:end]
	body = rest[end+4:]
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}
	return front, body, true
}

// InstructionFiles lists repository instruction files present on the default branch
func (m *Manager) InstructionFiles(ctx context.Context, repo *db.GitRepository) []string {
	files, _ := m.instructions(repo)
	return files
}

// Repository instructions concatenated for the system prompt
func (m *Manager) instructions(repo *db.GitRepository) ([]string, string) {
	r, err := m.git.Engine().OpenRepo(repo.Namespace, repo.Name, false)
	if err != nil {
		return nil, ""
	}
	defer r.Close()
	commit, err := r.Resolve(repo.DefaultBranch)
	if err != nil {
		return nil, ""
	}
	var files []string
	var b strings.Builder
	for _, p := range InstructionPaths {
		blob, err := r.Blob(commit, p, 0)
		if err != nil || blob.IsBinary {
			continue
		}
		files = append(files, p)
		fmt.Fprintf(&b, "\n## %s\n%s\n", p, strings.TrimSpace(string(blob.Content)))
	}
	return files, b.String()
}

// Whether a kind may reach a tool by default
func DefaultTools(kind string) []string {
	switch kind {
	case KindReview:
		return []string{"get_repository_info", "list_files", "read_file", "search_code", "get_diff", "get_pull_request", "list_commits", "get_checks", "get_workflow_run_log", "post_comment", "submit_review"}
	case KindCode:
		return []string{"get_repository_info", "list_files", "read_file", "search_code", "get_diff", "get_pull_request", "get_issue", "list_commits", "get_checks", "get_workflow_run_log", "search_issues", "write_file", "edit_file", "delete_file", "rename_file", "commit", "open_pull_request", "run_command", "post_comment", "request_review", "update_issue"}
	case KindChat:
		return []string{"get_repository_info", "list_files", "read_file", "search_code", "get_diff", "get_pull_request", "get_issue", "list_commits", "get_checks", "get_workflow_run_log", "search_issues", "post_comment"}
	case KindTriage:
		return []string{"get_repository_info", "read_file", "search_code", "get_issue", "search_issues", "list_commits", "update_issue", "post_comment"}
	}
	return []string{"get_repository_info", "list_files", "read_file", "search_code", "get_diff", "get_pull_request", "get_issue", "list_commits", "search_issues", "post_comment"}
}

// AllToolNames lists every registered tool, sorted
func AllToolNames() []string {
	out := make([]string, 0, len(toolRegistry))
	for name := range toolRegistry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
