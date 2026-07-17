package rbac

// Resource constants
const (
	ResourceRepositories  = "repositories"
	ResourceUsers         = "users"
	ResourceRoles         = "roles"
	ResourceSettings      = "settings"
	ResourceTokens        = "tokens"
	ResourceOrganizations = "organizations"
	ResourceWebhooks      = "webhooks"
	ResourceArtifacts     = "artifacts"
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
	{Resource: ResourceWebhooks, Actions: []string{ActionRead, ActionCreate, ActionUpdate, ActionDelete}},
	{Resource: ResourceArtifacts, Actions: []string{ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionPush, ActionPull, ActionManage}},
}
