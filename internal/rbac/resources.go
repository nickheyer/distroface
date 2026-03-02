package rbac

// Resource constants
const (
	ResourceRepositories  = "repositories"
	ResourceUsers         = "users"
	ResourceRoles         = "roles"
	ResourceSettings      = "settings"
	ResourceTokens        = "tokens"
	ResourceOrganizations = "organizations"
)

// Action constants
const (
	ActionRead   = "read"
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionDelete = "delete"
	ActionPush   = "push"
	ActionPull   = "pull"
	ActionManage = "manage"
)

// All actions
var AllActions = []string{
	ActionRead, ActionCreate, ActionUpdate, ActionDelete,
	ActionPush, ActionPull, ActionManage,
}

// All resources
var AllResources = []string{
	ResourceRepositories, ResourceUsers, ResourceRoles,
	ResourceSettings, ResourceTokens, ResourceOrganizations,
}

// Maps scopeable resources to the source they derive their object list from.
// Only repos and orgs support per-object scoping.
var ResourceScopeSource = map[string]string{
	ResourceRepositories:  ResourceRepositories,
	ResourceOrganizations: ResourceOrganizations,
}

// Pairs a resource with its valid actions
type ResourceActionEntry struct {
	Resource string
	Actions  []string
}

// Valid actions for each resource
var ResourceActions = []ResourceActionEntry{
	{Resource: ResourceRepositories, Actions: []string{ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionPush, ActionPull, ActionManage}},
	{Resource: ResourceUsers, Actions: []string{ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionManage}},
	{Resource: ResourceRoles, Actions: []string{ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionManage}},
	{Resource: ResourceSettings, Actions: []string{ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionManage}},
	{Resource: ResourceTokens, Actions: []string{ActionRead, ActionCreate, ActionDelete, ActionManage}},
	{Resource: ResourceOrganizations, Actions: []string{ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionManage}},
}
