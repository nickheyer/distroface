package gitrepo

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Level is the effective access on a unit or on the whole repository
type Level int

const (
	LevelNone Level = iota
	LevelRead
	LevelWrite
	LevelAdmin
	LevelOwner
)

// Every repository unit in display order
var AllUnits = []string{
	db.GitUnitCode, db.GitUnitIssues, db.GitUnitPulls, db.GitUnitReleases,
	db.GitUnitWiki, db.GitUnitProjects, db.GitUnitPackages, db.GitUnitActions, db.GitUnitAgents,
}

// Proto enum for a level
func (l Level) Proto() v1.GitAccessLevel {
	switch l {
	case LevelRead:
		return v1.GitAccessLevel_GIT_ACCESS_LEVEL_READ
	case LevelWrite:
		return v1.GitAccessLevel_GIT_ACCESS_LEVEL_WRITE
	case LevelAdmin:
		return v1.GitAccessLevel_GIT_ACCESS_LEVEL_ADMIN
	case LevelOwner:
		return v1.GitAccessLevel_GIT_ACCESS_LEVEL_OWNER
	default:
		return v1.GitAccessLevel_GIT_ACCESS_LEVEL_NONE
	}
}

// Level from a stored collaborator or team string
func LevelFromString(s string) Level {
	switch s {
	case db.GitLevelRead:
		return LevelRead
	case db.GitLevelWrite:
		return LevelWrite
	case db.GitLevelAdmin:
		return LevelAdmin
	default:
		return LevelNone
	}
}

// Level from the proto enum
func LevelFromProto(l v1.GitAccessLevel) Level {
	switch l {
	case v1.GitAccessLevel_GIT_ACCESS_LEVEL_READ:
		return LevelRead
	case v1.GitAccessLevel_GIT_ACCESS_LEVEL_WRITE:
		return LevelWrite
	case v1.GitAccessLevel_GIT_ACCESS_LEVEL_ADMIN:
		return LevelAdmin
	case v1.GitAccessLevel_GIT_ACCESS_LEVEL_OWNER:
		return LevelOwner
	default:
		return LevelNone
	}
}

// Stored string for a level
func (l Level) String() string {
	switch l {
	case LevelRead:
		return db.GitLevelRead
	case LevelWrite:
		return db.GitLevelWrite
	case LevelAdmin:
		return db.GitLevelAdmin
	case LevelOwner:
		return "owner"
	default:
		return db.GitLevelNone
	}
}

// Minimum level an rbac style action implies
func levelForAction(action string) Level {
	switch action {
	case rbac.ActionRead, rbac.ActionPull:
		return LevelRead
	case rbac.ActionPush, rbac.ActionCreate:
		return LevelWrite
	case rbac.ActionUpdate:
		return LevelAdmin
	case rbac.ActionDelete, rbac.ActionManage:
		return LevelOwner
	default:
		return LevelOwner
	}
}

// Units enabled on a repository, every unit on when nothing is stored
func UnitsOf(repo *db.GitRepository) *v1.GitRepoUnits {
	units := &v1.GitRepoUnits{Issues: true, Pulls: true, Releases: true, Wiki: true, Projects: true, Packages: true, Actions: false, Agents: false}
	if repo != nil && repo.Units != "" && repo.Units != "{}" {
		_ = protojson.Unmarshal([]byte(repo.Units), units)
	}
	return units
}

// Serializes units for storage
func EncodeUnits(u *v1.GitRepoUnits) string {
	b, err := protojson.Marshal(u)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Reports whether a unit is switched on for the repository
func UnitEnabled(repo *db.GitRepository, unit string) bool {
	return unitOn(UnitsOf(repo), unit)
}

// Same answer from units already decoded, loops decode once
func unitOn(u *v1.GitRepoUnits, unit string) bool {
	switch unit {
	case db.GitUnitCode:
		return true
	case db.GitUnitIssues:
		return u.Issues && u.ExternalIssuesUrl == ""
	case db.GitUnitPulls:
		return u.Pulls
	case db.GitUnitReleases:
		return u.Releases
	case db.GitUnitWiki:
		return u.Wiki && u.ExternalWikiUrl == ""
	case db.GitUnitProjects:
		return u.Projects
	case db.GitUnitPackages:
		return u.Packages
	case db.GitUnitActions:
		return u.Actions
	case db.GitUnitAgents:
		return u.Agents
	}
	return false
}

// Merge settings of a repository with defaults applied
func MergeSettingsOf(repo *db.GitRepository) *v1.GitMergeSettings {
	m := &v1.GitMergeSettings{
		AllowMerge: true, AllowSquash: true, AllowRebase: true, AllowRebaseMerge: true, AllowFastForward: true,
		DefaultMethod: v1.GitMergeMethod_GIT_MERGE_METHOD_MERGE, AllowForks: true,
	}
	if repo != nil && repo.MergeSettings != "" && repo.MergeSettings != "{}" {
		_ = protojson.Unmarshal([]byte(repo.MergeSettings), m)
	}
	return m
}

// Serializes merge settings for storage
func EncodeMergeSettings(m *v1.GitMergeSettings) string {
	b, err := protojson.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Access resolves repository levels from relationships and rbac grants
type Access struct {
	store    *stores.Store
	enforcer *rbac.Enforcer
}

func NewAccess(store *stores.Store, enforcer *rbac.Enforcer) *Access {
	return &Access{store: store, enforcer: enforcer}
}

var _ auth.LinkedAccess = (*Access)(nil)

// Effective levels per unit plus repository wide flags
type Levels struct {
	Units    map[string]Level
	CanAdmin bool
	IsOwner  bool
}

// Highest level the unit holds
func (l Levels) Unit(unit string) Level {
	if l.IsOwner {
		return LevelOwner
	}
	if l.CanAdmin {
		return LevelAdmin
	}
	return l.Units[unit]
}

// Proto view of the levels
func (l Levels) Proto() *v1.GitViewerAccess {
	out := &v1.GitViewerAccess{CanAdmin: l.CanAdmin || l.IsOwner, IsOwner: l.IsOwner}
	for _, u := range AllUnits {
		out.Units = append(out.Units, &v1.GitUnitLevel{Unit: unitProto(u), Level: l.Unit(u).Proto()})
	}
	return out
}

func unitProto(u string) v1.GitUnit {
	switch u {
	case db.GitUnitCode:
		return v1.GitUnit_GIT_UNIT_CODE
	case db.GitUnitIssues:
		return v1.GitUnit_GIT_UNIT_ISSUES
	case db.GitUnitPulls:
		return v1.GitUnit_GIT_UNIT_PULLS
	case db.GitUnitReleases:
		return v1.GitUnit_GIT_UNIT_RELEASES
	case db.GitUnitWiki:
		return v1.GitUnit_GIT_UNIT_WIKI
	case db.GitUnitProjects:
		return v1.GitUnit_GIT_UNIT_PROJECTS
	case db.GitUnitPackages:
		return v1.GitUnit_GIT_UNIT_PACKAGES
	case db.GitUnitActions:
		return v1.GitUnit_GIT_UNIT_ACTIONS
	case db.GitUnitAgents:
		return v1.GitUnit_GIT_UNIT_AGENTS
	}
	return v1.GitUnit_GIT_UNIT_UNSPECIFIED
}

// Computes every source of access for one user and repository
func (a *Access) Levels(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository) Levels {
	return a.levels(ctx, user, repo, true)
}

// Levels with or without the read every public repository hands out
func (a *Access) levels(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, public bool) Levels {
	out := Levels{Units: map[string]Level{}}
	if repo == nil {
		return out
	}
	anonymous := user == nil || user.IsAnonymous()
	units := UnitsOf(repo)

	// Public repositories admit anyone holding the read capability
	if public && !repo.IsPrivate {
		roles := []string{"anonymous"}
		if user != nil {
			roles = user.Roles
		}
		if a.enforcer.HasPermission(roles, rbac.ResourceGit, rbac.ActionRead) {
			for _, u := range AllUnits {
				if unitOn(units, u) {
					out.Units[u] = LevelRead
				}
			}
		}
	}
	if anonymous {
		return out
	}

	// Agent bots never sign in, the agents manager drives their writes
	if user.Provider == db.AuthProviderAgent {
		for _, u := range AllUnits {
			if unitOn(units, u) {
				out.Units[u] = max(out.Units[u], LevelRead)
			}
		}
		return out
	}

	// Deploy keys and scoped tokens reach only their object
	if user.IsScoped() {
		full := repo.Namespace + "/" + repo.Name
		if !auth.ScopeAllows(user, rbac.ActionRead, full, repo.Namespace) {
			return Levels{Units: map[string]Level{}}
		}
		level := LevelWrite
		if user.Scope.ReadOnly {
			level = LevelRead
		}
		if user.IsDeployKey() {
			for _, u := range []string{db.GitUnitCode, db.GitUnitWiki, db.GitUnitPackages} {
				out.Units[u] = max(out.Units[u], level)
			}
			return out
		}
		scoped := a.identityLevels(ctx, user, repo, units)
		if scoped.IsOwner || scoped.CanAdmin {
			// Owners and admins hold every unit, the scope caps them at write
			for _, u := range AllUnits {
				if unitOn(units, u) {
					out.Units[u] = max(out.Units[u], level)
				}
			}
			return out
		}
		for u, l := range scoped.Units {
			out.Units[u] = max(out.Units[u], min(l, level))
		}
		return out
	}

	ident := a.identityLevels(ctx, user, repo, units)
	out.IsOwner = ident.IsOwner
	out.CanAdmin = ident.CanAdmin
	for u, l := range ident.Units {
		out.Units[u] = max(out.Units[u], l)
	}
	return out
}

// Levels an account holds through ownership, membership, teams, collaborators, grants
func (a *Access) identityLevels(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, units *v1.GitRepoUnits) Levels {
	out := Levels{Units: map[string]Level{}}
	full := repo.Namespace + "/" + repo.Name

	if (repo.OwnerID != "" && repo.OwnerID == user.ID) || repo.Namespace == user.Username {
		out.IsOwner = true
		return out
	}
	if a.enforcer.HasPermission(user.Roles, rbac.ResourceGit, rbac.ActionManage) {
		out.IsOwner = true
		return out
	}
	if repo.IsOrgNamespace {
		org, _ := a.store.GetOrganization(ctx, repo.Namespace)
		if org != nil {
			member, _ := a.store.GetOrgMember(ctx, org.ID, user.ID)
			if member != nil {
				if member.Role == db.OrgRoleOwner || member.Role == db.OrgRoleAdmin {
					out.IsOwner = true
					return out
				}
				teams, _ := a.store.TeamsForUserRepo(ctx, org.ID, user.ID, repo.ID)
				for _, t := range teams {
					if t.AdminAccess {
						out.CanAdmin = true
					}
					granted := stores.DecodeTeamUnits(t.Units)
					for _, u := range AllUnits {
						if unitOn(units, u) {
							out.Units[u] = max(out.Units[u], LevelFromString(granted[u]))
						}
					}
				}
			}
		}
	}
	if c, _ := a.store.GetGitCollaborator(ctx, repo.ID, user.ID); c != nil && c.Accepted {
		lvl := LevelFromString(c.Permission)
		if lvl >= LevelAdmin {
			out.CanAdmin = true
		}
		for _, u := range AllUnits {
			if unitOn(units, u) {
				out.Units[u] = max(out.Units[u], min(lvl, LevelWrite))
			}
		}
	}
	// Scoped rbac grants name the repo or its namespace
	for _, action := range []string{rbac.ActionManage, rbac.ActionDelete, rbac.ActionUpdate, rbac.ActionPush, rbac.ActionRead, rbac.ActionPull} {
		if a.enforcer.HasScopedGrant(user.Roles, rbac.ResourceGit, action, full, repo.Namespace) {
			lvl := levelForAction(action)
			switch {
			case lvl >= LevelOwner:
				out.IsOwner = true
				return out
			case lvl == LevelAdmin:
				out.CanAdmin = true
			}
			for _, u := range AllUnits {
				if unitOn(units, u) {
					out.Units[u] = max(out.Units[u], min(lvl, LevelWrite))
				}
			}
		}
	}
	return out
}

// Level on one unit
func (a *Access) Level(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, unit string) Level {
	lv := a.Levels(ctx, user, repo)
	if !UnitEnabled(repo, unit) && !lv.IsOwner && !lv.CanAdmin {
		return LevelNone
	}
	return lv.Unit(unit)
}

// Reports whether the user may perform an action on a unit
func (a *Access) Can(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, unit, action string) bool {
	need := levelForAction(action)
	if user != nil && user.IsScoped() && !auth.ScopeAllows(user, action, repo.Namespace+"/"+repo.Name, repo.Namespace) {
		return false
	}
	if user == nil || user.IsAnonymous() {
		return need == LevelRead && a.Level(ctx, user, repo, unit) >= LevelRead
	}
	return a.Level(ctx, user, repo, unit) >= need
}

// Public repos or any read relationship
func (a *Access) CanSee(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository) bool {
	if repo == nil {
		return false
	}
	// Public repositories need no identity lookups for readers
	if !repo.IsPrivate && (user == nil || !user.IsScoped()) {
		roles := []string{"anonymous"}
		if user != nil {
			roles = user.Roles
		}
		if a.enforcer.HasPermission(roles, rbac.ResourceGit, rbac.ActionRead) {
			return true
		}
	}
	return a.Level(ctx, user, repo, db.GitUnitCode) >= LevelRead
}

// Visibility of many users, membership rows read once
func (a *Access) CanSeeMany(ctx context.Context, users []*auth.AuthenticatedUser, repo *db.GitRepository) map[string]bool {
	out := make(map[string]bool, len(users))
	if repo == nil {
		return out
	}
	var pending []*auth.AuthenticatedUser
	for _, u := range users {
		if u == nil {
			continue
		}
		switch {
		case u.IsScoped():
			out[u.ID] = a.CanSee(ctx, u, repo)
		case !repo.IsPrivate && a.enforcer.HasPermission(u.Roles, rbac.ResourceGit, rbac.ActionRead):
			out[u.ID] = true
		case u.IsAnonymous():
			out[u.ID] = false
		case u.Provider == db.AuthProviderAgent:
			out[u.ID] = true
		case (repo.OwnerID != "" && repo.OwnerID == u.ID) || repo.Namespace == u.Username:
			out[u.ID] = true
		case a.enforcer.HasPermission(u.Roles, rbac.ResourceGit, rbac.ActionManage):
			out[u.ID] = true
		case a.hasAnyScopedGrant(u, repo):
			out[u.ID] = true
		default:
			out[u.ID] = false
			pending = append(pending, u)
		}
	}
	if len(pending) == 0 {
		return out
	}
	ids := make([]string, 0, len(pending))
	for _, u := range pending {
		ids = append(ids, u.ID)
	}
	if repo.IsOrgNamespace {
		if org, _ := a.store.GetOrganization(ctx, repo.Namespace); org != nil {
			roles, _ := a.store.OrgMemberRoles(ctx, org.ID, ids)
			teams, _ := a.store.TeamsCoveringRepo(ctx, org.ID, repo.ID)
			var readTeams []string
			defaultReads := false
			for _, t := range teams {
				if !t.AdminAccess && LevelFromString(stores.DecodeTeamUnits(t.Units)[db.GitUnitCode]) < LevelRead {
					continue
				}
				if t.IsDefault {
					defaultReads = true
					continue
				}
				readTeams = append(readTeams, t.ID)
			}
			onTeams, _ := a.store.UsersOnTeams(ctx, readTeams, ids)
			for id, role := range roles {
				if role == db.OrgRoleOwner || role == db.OrgRoleAdmin || defaultReads || onTeams[id] {
					out[id] = true
				}
			}
		}
	}
	collaborators, _ := a.store.AcceptedGitCollaborators(ctx, repo.ID, ids)
	for id, perm := range collaborators {
		if LevelFromString(perm) >= LevelRead {
			out[id] = true
		}
	}
	return out
}

// Any scoped rbac grant on the repository or its namespace
func (a *Access) hasAnyScopedGrant(user *auth.AuthenticatedUser, repo *db.GitRepository) bool {
	full := repo.Namespace + "/" + repo.Name
	for _, action := range []string{rbac.ActionManage, rbac.ActionDelete, rbac.ActionUpdate, rbac.ActionPush, rbac.ActionRead, rbac.ActionPull} {
		if a.enforcer.HasScopedGrant(user.Roles, rbac.ResourceGit, action, full, repo.Namespace) {
			return true
		}
	}
	return false
}

// Read on an enabled unit, public repos skip the identity lookups
func (a *Access) UnitReadable(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, unit string) bool {
	if repo == nil || !UnitEnabled(repo, unit) {
		return false
	}
	if !repo.IsPrivate && (user == nil || !user.IsScoped()) {
		roles := []string{"anonymous"}
		if user != nil {
			roles = user.Roles
		}
		if a.enforcer.HasPermission(roles, rbac.ResourceGit, rbac.ActionRead) {
			return true
		}
	}
	return a.Level(ctx, user, repo, unit) >= LevelRead
}

// Owner username, org team allowing creation, or manage into an existing namespace
func (a *Access) CanCreateInNamespace(ctx context.Context, user *auth.AuthenticatedUser, namespace string) bool {
	if user == nil || user.IsAnonymous() || user.IsScoped() {
		return false
	}
	if namespace == user.Username {
		return true
	}
	if a.enforcer.HasPermission(user.Roles, rbac.ResourceGit, rbac.ActionManage) {
		if u, _ := a.store.GetUserByUsername(ctx, namespace); u != nil {
			return true
		}
		if org, _ := a.store.GetOrganization(ctx, namespace); org != nil {
			return true
		}
		return false
	}
	org, _ := a.store.GetOrganization(ctx, namespace)
	if org == nil {
		return false
	}
	member, _ := a.store.GetOrgMember(ctx, org.ID, user.ID)
	if member == nil {
		return false
	}
	if member.Role == db.OrgRoleOwner || member.Role == db.OrgRoleAdmin {
		return true
	}
	teams, _ := a.store.TeamsForUser(ctx, org.ID, user.ID)
	return slices.ContainsFunc(teams, func(t *db.OrgTeam) bool { return t.CanCreateRepos })
}

// Repo list options honoring viewer visibility
func (a *Access) ListOptions(user *auth.AuthenticatedUser, namespace string) stores.GitRepoListOptions {
	opts := stores.GitRepoListOptions{Namespace: namespace}
	if user != nil && !user.IsAnonymous() {
		opts.ViewerID = user.ID
		opts.IncludePrivate = a.enforcer.HasPermission(user.Roles, rbac.ResourceGit, rbac.ActionManage)
		if !opts.IncludePrivate {
			opts.GrantedRepos = a.enforcer.GetGrantedObjects(user.Roles, rbac.ResourceGit, rbac.ActionRead)
		}
		opts.OnlyRepo, opts.OnlyNamespace = user.ListingScope()
	}
	return opts
}

// Packages inherit the source repository's relationships, never its public visibility
func (a *Access) LinkedRepoAllows(ctx context.Context, user *auth.AuthenticatedUser, gitRepoID string, action string) bool {
	if user == nil || user.IsAnonymous() {
		return false
	}
	repo, err := a.store.GetGitRepositoryByID(ctx, gitRepoID)
	if err != nil || repo == nil {
		return false
	}
	if user.IsScoped() && !auth.ScopeAllows(user, action, repo.Namespace+"/"+repo.Name, repo.Namespace) {
		return false
	}
	lv := a.levels(ctx, user, repo, false)
	if !UnitEnabled(repo, db.GitUnitPackages) && !lv.IsOwner && !lv.CanAdmin {
		return false
	}
	return lv.Unit(db.GitUnitPackages) >= levelForAction(action)
}

// Reports whether a user id or any of their team ids appears in a list
func (a *Access) InAllowList(ctx context.Context, user *auth.AuthenticatedUser, repo *db.GitRepository, ids []string) bool {
	if len(ids) == 0 || user == nil {
		return false
	}
	if slices.Contains(ids, user.ID) {
		return true
	}
	if !repo.IsOrgNamespace {
		return false
	}
	org, _ := a.store.GetOrganization(ctx, repo.Namespace)
	if org == nil {
		return false
	}
	teams, _ := a.store.TeamsForUser(ctx, org.ID, user.ID)
	for _, t := range teams {
		if slices.Contains(ids, t.ID) {
			return true
		}
	}
	return false
}

// Decodes a json string list stored on protections
func DecodeIDs(raw string) []string {
	var out []string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// Encodes a string list for protections
func EncodeIDs(ids []string) string {
	if ids == nil {
		ids = []string{}
	}
	b, _ := json.Marshal(ids)
	return string(b)
}
