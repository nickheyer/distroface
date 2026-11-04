package settings

import (
	"testing"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

func TestAgentsScopePaths(t *testing.T) {
	org := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG
	repo := v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_REPO
	if !AllowedPath(org, "git.agents.enabled") || !AllowedPath(org, "git.agents.allow_repo_providers") {
		t.Fatal("orgs decide whether agents run and who may add connectors")
	}
	if AllowedPath(repo, "git.agents.enabled") || AllowedPath(repo, "git.agents.allow_repo_providers") {
		t.Fatal("repositories cannot switch agents on or widen connector policy")
	}
	if !AllowedPath(repo, "git.agents.max_turns") || !AllowedPath(repo, "git.agents.allow_commands") {
		t.Fatal("repositories tighten limits")
	}
	if AllowedPath(org, "git.agents.allow_private_networks") {
		t.Fatal("private network reach is instance only")
	}
	if AllowedPath(org, "git.agents.debug_tool_calls") || AllowedPath(repo, "git.agents.debug_tool_calls") {
		t.Fatal("tool call debugging is instance only")
	}
}

func TestAgentsClampAgainstInstance(t *testing.T) {
	r := NewResolver(newMemStore(), nil)
	ctx := t.Context()
	sys := &v1.Settings{Git: &v1.GitSettings{Agents: &v1.GitAgentsSettings{
		MaxTurns: proto.Int32(20), TokenBudgetPerSession: proto.Int64(1000), AllowCommands: proto.Bool(false), AllowRepoAgents: proto.Bool(false),
	}}}
	if _, err := r.Update(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_SYSTEM, "", sys, []string{
		"git.agents.max_turns", "git.agents.token_budget_per_session", "git.agents.allow_commands", "git.agents.allow_repo_agents",
	}); err != nil {
		t.Fatal(err)
	}
	org := &v1.Settings{Git: &v1.GitSettings{Agents: &v1.GitAgentsSettings{
		Enabled: proto.Bool(true), MaxTurns: proto.Int32(100), TokenBudgetPerSession: proto.Int64(0), AllowCommands: proto.Bool(true), AllowRepoAgents: proto.Bool(true),
	}}}
	if _, err := r.Update(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG, "org1", org, []string{
		"git.agents.enabled", "git.agents.max_turns", "git.agents.token_budget_per_session", "git.agents.allow_commands", "git.agents.allow_repo_agents",
	}); err != nil {
		t.Fatal(err)
	}
	eff, prov, err := r.Effective(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_ORG, "org1")
	if err != nil {
		t.Fatal(err)
	}
	a := eff.GetGit().GetAgents()
	if !a.GetEnabled() {
		t.Fatal("org may enable agents")
	}
	if a.GetMaxTurns() != 20 || provFor(prov, "git.agents.max_turns") != v1.SettingsTier_SETTINGS_TIER_SYSTEM {
		t.Fatalf("turns must clamp to the instance cap, got %d", a.GetMaxTurns())
	}
	if a.GetTokenBudgetPerSession() != 1000 {
		t.Fatalf("unlimited budget below a capped instance falls back, got %d", a.GetTokenBudgetPerSession())
	}
	if a.GetAllowCommands() || a.GetAllowRepoAgents() {
		t.Fatal("orgs cannot switch on what the instance forbids")
	}
	if provFor(prov, "git.agents.allow_commands") != v1.SettingsTier_SETTINGS_TIER_SYSTEM {
		t.Fatal("provenance must point at the instance for forbidden toggles")
	}
}
