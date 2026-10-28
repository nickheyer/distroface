package rbac

import (
	"strings"

	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ProcedurePermission maps an RPC procedure to a resource and action.
type ProcedurePermission struct {
	Resource      string
	Action        string
	ObjectIDField string // Protobuf field name to extract for per-object RBAC (empty = "*")
	AnyGrant      bool   // Wildcard or scoped grant passes, the service filters visibility
}

// Reachable with no identity at all, the pre login surface
var PublicProcedures = map[string]bool{
	distrofacev1connect.AuthServiceRegisterProcedure:        true,
	distrofacev1connect.AuthServiceLoginProcedure:           true,
	distrofacev1connect.AuthServiceGetAuthStatusProcedure:   true,
	distrofacev1connect.AuthServiceGetOIDCLoginURLProcedure: true,
	distrofacev1connect.HealthServiceHealthCheckProcedure:   true,
	// Signed out callers receive the redacted public subset only
	distrofacev1connect.SettingsServiceGetEffectiveSettingsProcedure: true,
	// Invite validation is public (used during registration)
	distrofacev1connect.AuthServiceValidateInviteProcedure: true,
	// Portal identity for the serving host, needed pre-login
	distrofacev1connect.PortalServiceResolvePortalProcedure: true,
	// Mailed tokens authenticate these flows themselves
	distrofacev1connect.AuthServiceRequestPasswordResetProcedure:            true,
	distrofacev1connect.AuthServiceResetPasswordProcedure:                   true,
	distrofacev1connect.AuthServiceVerifyEmailProcedure:                     true,
	distrofacev1connect.NotificationServiceUpdateNotificationPrefsProcedure: true,
	distrofacev1connect.NotificationServiceGetNotificationPrefsProcedure:    true,
	// Runners authenticate with their own token header
	distrofacev1connect.RunnerServiceRegisterRunnerProcedure:     true,
	distrofacev1connect.RunnerServiceHeartbeatProcedure:          true,
	distrofacev1connect.RunnerServiceFetchTaskProcedure:          true,
	distrofacev1connect.RunnerServiceUpdateTaskProcedure:         true,
	distrofacev1connect.RunnerServiceAppendTaskLogProcedure:      true,
	distrofacev1connect.RunnerServiceCompleteTaskProcedure:       true,
	distrofacev1connect.RunnerServiceUploadTaskArtifactProcedure: true,
	distrofacev1connect.RunnerServiceFinishTaskArtifactProcedure: true,
	distrofacev1connect.RunnerServiceListTaskArtifactsProcedure:  true,
	distrofacev1connect.RunnerServiceFetchExecProcedure:          true,
	distrofacev1connect.RunnerServiceCompleteExecProcedure:       true,
}

// Mutations outside the resource action heuristic that read only portals refuse
var PortalWriteProcedures = map[string]bool{
	// Stars, watches, and subscriptions change state through the portal host
	distrofacev1connect.RepositoryServiceStarRepositoryProcedure:         true,
	distrofacev1connect.RepositoryServiceUnstarRepositoryProcedure:       true,
	distrofacev1connect.GitServiceStarGitRepositoryProcedure:             true,
	distrofacev1connect.GitServiceUnstarGitRepositoryProcedure:           true,
	distrofacev1connect.ArtifactServiceStarArtifactRepositoryProcedure:   true,
	distrofacev1connect.ArtifactServiceUnstarArtifactRepositoryProcedure: true,
	distrofacev1connect.GitServiceWatchGitRepositoryProcedure:            true,
	distrofacev1connect.IssueServiceSubscribeIssueProcedure:              true,
	distrofacev1connect.IssueServiceUnsubscribeIssueProcedure:            true,
	// Ownership and membership changes
	distrofacev1connect.GitServiceAcceptTransferProcedure:      true,
	distrofacev1connect.GitServiceRejectTransferProcedure:      true,
	distrofacev1connect.GitServiceAcceptCollaborationProcedure: true,
	// Agent sessions are mapped as reads for rbac, they still mutate
	distrofacev1connect.AgentsServiceStartAgentSessionProcedure:    true,
	distrofacev1connect.AgentsServiceCancelAgentSessionProcedure:   true,
	distrofacev1connect.AgentsServiceRerunAgentSessionProcedure:    true,
	distrofacev1connect.AgentsServiceContinueAgentSessionProcedure: true,
	distrofacev1connect.AgentsServiceResolveAgentApprovalProcedure: true,
	distrofacev1connect.AgentsServiceDeleteAgentSessionProcedure:   true,
	distrofacev1connect.AgentsServiceCreateAgentProviderProcedure:  true,
	distrofacev1connect.AgentsServiceUpdateAgentProviderProcedure:  true,
	distrofacev1connect.AgentsServiceDeleteAgentProviderProcedure:  true,
	distrofacev1connect.AgentsServiceCreateAgentProcedure:          true,
	distrofacev1connect.AgentsServiceUpdateAgentProcedure:          true,
	distrofacev1connect.AgentsServiceDeleteAgentProcedure:          true,
	// Workflow controls are mapped as reads for rbac, they still mutate
	distrofacev1connect.ActionsServiceEnableWorkflowProcedure:    true,
	distrofacev1connect.ActionsServiceDisableWorkflowProcedure:   true,
	distrofacev1connect.ActionsServiceDispatchWorkflowProcedure:  true,
	distrofacev1connect.ActionsServiceCancelRunProcedure:         true,
	distrofacev1connect.ActionsServiceRerunRunProcedure:          true,
	distrofacev1connect.ActionsServiceApproveRunProcedure:        true,
	distrofacev1connect.ActionsServiceSetSecretProcedure:         true,
	distrofacev1connect.ActionsServiceDeleteSecretProcedure:      true,
	distrofacev1connect.ActionsServiceSetVariableProcedure:       true,
	distrofacev1connect.ActionsServiceDeleteVariableProcedure:    true,
	distrofacev1connect.ActionsServiceCreateRunnerTokenProcedure: true,
	distrofacev1connect.ActionsServiceDeleteRunnerProcedure:      true,
	distrofacev1connect.ActionsServiceUpdateRunnerProcedure:      true,
	// Boards
	distrofacev1connect.ProjectServiceCreateProjectProcedure:          true,
	distrofacev1connect.ProjectServiceUpdateProjectProcedure:          true,
	distrofacev1connect.ProjectServiceDeleteProjectProcedure:          true,
	distrofacev1connect.ProjectServiceCloseProjectProcedure:           true,
	distrofacev1connect.ProjectServiceCreateColumnProcedure:           true,
	distrofacev1connect.ProjectServiceUpdateColumnProcedure:           true,
	distrofacev1connect.ProjectServiceDeleteColumnProcedure:           true,
	distrofacev1connect.ProjectServiceMoveColumnProcedure:             true,
	distrofacev1connect.ProjectServiceAddIssueToProjectProcedure:      true,
	distrofacev1connect.ProjectServiceMoveIssueProcedure:              true,
	distrofacev1connect.ProjectServiceRemoveIssueFromProjectProcedure: true,
}

// Reads that need a write grant for rbac, read only portals still serve them
var PortalReadProcedures = map[string]bool{
	distrofacev1connect.GitServiceListPushMirrorsProcedure: true,
}

// Any resolved identity including anonymous, no resource grant needed
var IdentityProcedures = map[string]bool{
	distrofacev1connect.MirrorServiceWatchSyncsProcedure: true,
	// Public profile fields, private fields gated in-service
	distrofacev1connect.UserServiceGetUserProcedure: true,
	// Feed rows carry their own visibility, private targets are checked in-service
	distrofacev1connect.ActivityServiceListActivityProcedure: true,
	// Boards and labels are read through their repo or org in-service
	distrofacev1connect.ProjectServiceGetProjectProcedure:   true,
	distrofacev1connect.ProjectServiceListProjectsProcedure: true,
	distrofacev1connect.IssueServiceListLabelsProcedure:     true,
}

// Real accounts only, the anonymous identity is refused
var AuthenticatedOnlyProcedures = map[string]bool{
	// Auth - user operations
	distrofacev1connect.AuthServiceGetCurrentUserProcedure: true,
	distrofacev1connect.AuthServiceLogoutProcedure:         true,
	distrofacev1connect.AuthServiceRefreshSessionProcedure: true,

	// User - self-service
	distrofacev1connect.UserServiceUpdateUserProcedure:     true,
	distrofacev1connect.UserServiceChangePasswordProcedure: true,
	distrofacev1connect.UserServiceSetAvatarProcedure:      true,
	distrofacev1connect.UserServiceRemoveAvatarProcedure:   true,
	// Public profile subset only, powers member pickers
	distrofacev1connect.UserServiceSearchUsersProcedure: true,

	// Stars - read access enforced in-service
	distrofacev1connect.RepositoryServiceStarRepositoryProcedure:          true,
	distrofacev1connect.RepositoryServiceUnstarRepositoryProcedure:        true,
	distrofacev1connect.RepositoryServiceListStarredRepositoriesProcedure: true,
	distrofacev1connect.ArtifactServiceStarArtifactRepositoryProcedure:    true,
	distrofacev1connect.ArtifactServiceUnstarArtifactRepositoryProcedure:  true,

	// Org slug resolution, object scoped read enforced in-service
	distrofacev1connect.OrganizationServiceGetOrganizationProcedure: true,

	// Git repository transfers and collaborations addressed to the caller
	distrofacev1connect.GitServiceAcceptTransferProcedure:      true,
	distrofacev1connect.GitServiceRejectTransferProcedure:      true,
	distrofacev1connect.GitServiceListTransfersProcedure:       true,
	distrofacev1connect.GitServiceAcceptCollaborationProcedure: true,
	distrofacev1connect.GitServiceStarGitRepositoryProcedure:   true,
	distrofacev1connect.GitServiceUnstarGitRepositoryProcedure: true,
	distrofacev1connect.GitServiceWatchGitRepositoryProcedure:  true,

	// Notifications and keys belong to the caller, repo checks in-service
	distrofacev1connect.NotificationServiceListNotificationsProcedure:  true,
	distrofacev1connect.NotificationServiceGetUnreadCountProcedure:     true,
	distrofacev1connect.NotificationServiceMarkReadProcedure:           true,
	distrofacev1connect.NotificationServiceMarkAllReadProcedure:        true,
	distrofacev1connect.NotificationServicePinNotificationProcedure:    true,
	distrofacev1connect.NotificationServiceWatchNotificationsProcedure: true,
	distrofacev1connect.KeyServiceAddSSHKeyProcedure:                   true,
	distrofacev1connect.KeyServiceListSSHKeysProcedure:                 true,
	distrofacev1connect.KeyServiceDeleteSSHKeyProcedure:                true,
	distrofacev1connect.KeyServiceAddGPGKeyProcedure:                   true,
	distrofacev1connect.KeyServiceListGPGKeysProcedure:                 true,
	distrofacev1connect.KeyServiceDeleteGPGKeyProcedure:                true,
	distrofacev1connect.KeyServiceVerifyGPGKeyProcedure:                true,
	distrofacev1connect.AuthServiceRequestEmailVerificationProcedure:   true,

	// Mail scope admin checks happen in-service per tier
	distrofacev1connect.MailServiceSendTestEmailProcedure:      true,
	distrofacev1connect.MailServiceListMailDeliveriesProcedure: true,
	distrofacev1connect.MailServiceRetryMailDeliveryProcedure:  true,

	// Actions scopes resolve to system, org, or repo admins in-service
	distrofacev1connect.ActionsServiceListSecretsProcedure:       true,
	distrofacev1connect.ActionsServiceSetSecretProcedure:         true,
	distrofacev1connect.ActionsServiceDeleteSecretProcedure:      true,
	distrofacev1connect.ActionsServiceListVariablesProcedure:     true,
	distrofacev1connect.ActionsServiceSetVariableProcedure:       true,
	distrofacev1connect.ActionsServiceDeleteVariableProcedure:    true,
	distrofacev1connect.ActionsServiceListRunnersProcedure:       true,
	distrofacev1connect.ActionsServiceCreateRunnerTokenProcedure: true,
	distrofacev1connect.ActionsServiceDeleteRunnerProcedure:      true,
	distrofacev1connect.ActionsServiceUpdateRunnerProcedure:      true,

	// Agent connectors and definitions resolve to system, org, or repo admins in-service
	distrofacev1connect.AgentsServiceListAgentProvidersProcedure:      true,
	distrofacev1connect.AgentsServiceGetAgentProviderProcedure:        true,
	distrofacev1connect.AgentsServiceCreateAgentProviderProcedure:     true,
	distrofacev1connect.AgentsServiceUpdateAgentProviderProcedure:     true,
	distrofacev1connect.AgentsServiceDeleteAgentProviderProcedure:     true,
	distrofacev1connect.AgentsServiceTestAgentProviderProcedure:       true,
	distrofacev1connect.AgentsServiceListAgentProviderModelsProcedure: true,
	distrofacev1connect.AgentsServiceListAgentsProcedure:              true,
	distrofacev1connect.AgentsServiceGetAgentProcedure:                true,
	distrofacev1connect.AgentsServiceCreateAgentProcedure:             true,
	distrofacev1connect.AgentsServiceUpdateAgentProcedure:             true,
	distrofacev1connect.AgentsServiceDeleteAgentProcedure:             true,
	distrofacev1connect.AgentsServiceGetAgentUsageProcedure:           true,

	// Boards resolve their repo or org in-service
	distrofacev1connect.ProjectServiceCreateProjectProcedure:          true,
	distrofacev1connect.ProjectServiceUpdateProjectProcedure:          true,
	distrofacev1connect.ProjectServiceDeleteProjectProcedure:          true,
	distrofacev1connect.ProjectServiceCloseProjectProcedure:           true,
	distrofacev1connect.ProjectServiceCreateColumnProcedure:           true,
	distrofacev1connect.ProjectServiceUpdateColumnProcedure:           true,
	distrofacev1connect.ProjectServiceDeleteColumnProcedure:           true,
	distrofacev1connect.ProjectServiceMoveColumnProcedure:             true,
	distrofacev1connect.ProjectServiceAddIssueToProjectProcedure:      true,
	distrofacev1connect.ProjectServiceMoveIssueProcedure:              true,
	distrofacev1connect.ProjectServiceRemoveIssueFromProjectProcedure: true,

	// Org and repo labels by org id resolve in-service
	distrofacev1connect.IssueServiceSubscribeIssueProcedure:   true,
	distrofacev1connect.IssueServiceUnsubscribeIssueProcedure: true,

	// Settings scope permissions enforced in-service per tier
	distrofacev1connect.SettingsServiceGetSettingsProcedure:      true,
	distrofacev1connect.SettingsServiceUpdateSettingsProcedure:   true,
	distrofacev1connect.SettingsServiceProbeGitSSHPortProcedure:  true,
	distrofacev1connect.SettingsServiceGetGitServerInfoProcedure: true,

	// Target org derived from the row in-service
	distrofacev1connect.CertificateServiceRemoveCertificateDomainProcedure:      true,
	distrofacev1connect.CertificateServiceBulkRemoveCertificateDomainsProcedure: true,
	distrofacev1connect.CertificateServiceIssueCertificateProcedure:             true,
	// System admin for app ca, org admin for org ca, checked in-service
	distrofacev1connect.CertificateServiceSignCSRProcedure: true,
}

// ProcedurePermissions maps each RPC procedure path to the resource and action
// required to invoke it, plus an optional ObjectIDField for per-object scoping.
var ProcedurePermissions = map[string]ProcedurePermission{
	// ── RepositoryService ─────────────────────────────────────────────
	// Browsing needs the read capability, private visibility is filtered in-service
	distrofacev1connect.RepositoryServiceListRepositoriesProcedure:   {Resource: ResourceRepositories, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.RepositoryServiceGetRepositoryProcedure:      {Resource: ResourceRepositories, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.RepositoryServiceListTagsProcedure:           {Resource: ResourceRepositories, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.RepositoryServiceResolveTagProcedure:         {Resource: ResourceRepositories, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.RepositoryServiceCreateRepositoryProcedure:   {Resource: ResourceRepositories, Action: ActionCreate},
	distrofacev1connect.RepositoryServiceDeleteRepositoryProcedure:   {Resource: ResourceRepositories, Action: ActionDelete, ObjectIDField: "namespace+name"},
	distrofacev1connect.RepositoryServiceUpdateRepositoryProcedure:   {Resource: ResourceRepositories, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.RepositoryServiceSyncRepositoryProcedure:     {Resource: ResourceRepositories, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.RepositoryServiceStopRepositorySyncProcedure: {Resource: ResourceRepositories, Action: ActionUpdate, ObjectIDField: "namespace+name"},

	// ── UserService (admin) ───────────────────────────────────────────
	distrofacev1connect.UserServiceListUsersProcedure:            {Resource: ResourceUsers, Action: ActionRead},
	distrofacev1connect.UserServiceAdminUpdateUserProcedure:      {Resource: ResourceUsers, Action: ActionUpdate},
	distrofacev1connect.UserServiceAdminDeleteUserProcedure:      {Resource: ResourceUsers, Action: ActionDelete},
	distrofacev1connect.UserServiceAdminCreateUserProcedure:      {Resource: ResourceUsers, Action: ActionCreate},
	distrofacev1connect.UserServiceAdminBulkUpdateUsersProcedure: {Resource: ResourceUsers, Action: ActionUpdate},
	distrofacev1connect.UserServiceAdminBulkDeleteUsersProcedure: {Resource: ResourceUsers, Action: ActionDelete},

	// ── RoleService ───────────────────────────────────────────────────
	distrofacev1connect.RoleServiceListRolesProcedure:            {Resource: ResourceRoles, Action: ActionRead},
	distrofacev1connect.RoleServiceGetRoleProcedure:              {Resource: ResourceRoles, Action: ActionRead},
	distrofacev1connect.RoleServiceCreateRoleProcedure:           {Resource: ResourceRoles, Action: ActionCreate},
	distrofacev1connect.RoleServiceUpdateRoleProcedure:           {Resource: ResourceRoles, Action: ActionUpdate},
	distrofacev1connect.RoleServiceDeleteRoleProcedure:           {Resource: ResourceRoles, Action: ActionDelete},
	distrofacev1connect.RoleServiceGetPermissionMatrixProcedure:  {Resource: ResourceRoles, Action: ActionRead},
	distrofacev1connect.RoleServiceListScopeableObjectsProcedure: {Resource: ResourceRoles, Action: ActionRead},
	distrofacev1connect.RoleServiceUpdatePermissionsProcedure:    {Resource: ResourceRoles, Action: ActionUpdate},
	distrofacev1connect.RoleServiceAssignRoleProcedure:           {Resource: ResourceRoles, Action: ActionCreate},
	distrofacev1connect.RoleServiceUnassignRoleProcedure:         {Resource: ResourceRoles, Action: ActionDelete},
	distrofacev1connect.RoleServiceGetUserRolesProcedure:         {Resource: ResourceRoles, Action: ActionRead},

	// ── GCService (admin) ─────────────────────────────────────────────
	distrofacev1connect.GCServiceRunGCProcedure:           {Resource: ResourceSettings, Action: ActionUpdate},
	distrofacev1connect.GCServiceGetGCStatusProcedure:     {Resource: ResourceSettings, Action: ActionRead},
	distrofacev1connect.GCServiceGetStorageUsageProcedure: {Resource: ResourceSettings, Action: ActionRead},

	// ── AuthService (admin) ───────────────────────────────────────────
	distrofacev1connect.AuthServiceCreateInviteProcedure:      {Resource: ResourceSettings, Action: ActionCreate},
	distrofacev1connect.AuthServiceListInvitesProcedure:       {Resource: ResourceSettings, Action: ActionRead},
	distrofacev1connect.AuthServiceGetInviteProcedure:         {Resource: ResourceSettings, Action: ActionRead},
	distrofacev1connect.AuthServiceDeleteInviteProcedure:      {Resource: ResourceSettings, Action: ActionDelete},
	distrofacev1connect.AuthServiceBulkDeleteInvitesProcedure: {Resource: ResourceSettings, Action: ActionDelete},

	// ── TokenService ────────────────────────────────────────────────
	distrofacev1connect.TokenServiceCreateAPITokenProcedure: {Resource: ResourceTokens, Action: ActionCreate},
	distrofacev1connect.TokenServiceListAPITokensProcedure:  {Resource: ResourceTokens, Action: ActionRead},
	distrofacev1connect.TokenServiceDeleteAPITokenProcedure: {Resource: ResourceTokens, Action: ActionDelete},

	// ── OrganizationService ───────────────────────────────────────────
	distrofacev1connect.OrganizationServiceCreateOrganizationProcedure:   {Resource: ResourceOrganizations, Action: ActionCreate},
	distrofacev1connect.OrganizationServiceListOrganizationsProcedure:    {Resource: ResourceOrganizations, Action: ActionRead},
	distrofacev1connect.OrganizationServiceUpdateOrganizationProcedure:   {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "id"},
	distrofacev1connect.OrganizationServiceDeleteOrganizationProcedure:   {Resource: ResourceOrganizations, Action: ActionDelete, ObjectIDField: "id"},
	distrofacev1connect.OrganizationServiceListOrgMembersProcedure:       {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceAddOrgMemberProcedure:         {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceRemoveOrgMemberProcedure:      {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceUpdateOrgMemberRoleProcedure:  {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceTransferOrgOwnershipProcedure: {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},

	// ── PortalService (org-scoped; owner/admin checks in-service) ──────
	distrofacev1connect.PortalServiceCreatePortalProcedure: {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.PortalServiceGetPortalProcedure:    {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.PortalServiceListPortalsProcedure:  {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.PortalServiceUpdatePortalProcedure: {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.PortalServiceDeletePortalProcedure: {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},

	// ── CertificateService (system or org scope, checks in-service) ────
	distrofacev1connect.CertificateServiceListCertificateDomainsProcedure:   {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceAddCertificateDomainProcedure:     {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceApproveCertificateDomainProcedure: {Resource: ResourceSettings, Action: ActionManage},
	distrofacev1connect.CertificateServiceUploadTLSCertificateProcedure:     {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceDeleteTLSCertificateProcedure:     {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceGetTLSMaterialProcedure:           {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceGenerateOrgCAProcedure:            {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceGenerateAppCAProcedure:            {Resource: ResourceSettings, Action: ActionManage},
	distrofacev1connect.CertificateServiceIssueOrgICAProcedure:              {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceGetCertStatusProcedure:            {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},

	// ── AuditService (admin) ──────────────────────────────────────────
	distrofacev1connect.AuditServiceListAuditEventsProcedure: {Resource: ResourceSettings, Action: ActionRead},

	// ── ArtifactService ───────────────────────────────────────────────
	distrofacev1connect.ArtifactServiceCreateArtifactRepositoryProcedure:        {Resource: ResourceArtifacts, Action: ActionCreate},
	distrofacev1connect.ArtifactServiceGetArtifactRepositoryProcedure:           {Resource: ResourceArtifacts, Action: ActionRead, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceListArtifactRepositoriesProcedure:        {Resource: ResourceArtifacts, Action: ActionRead},
	distrofacev1connect.ArtifactServiceUpdateArtifactRepositoryProcedure:        {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceSyncArtifactRepositoryProcedure:          {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceStopArtifactRepositorySyncProcedure:      {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceDeleteArtifactRepositoryProcedure:        {Resource: ResourceArtifacts, Action: ActionDelete, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceInitiateArtifactUploadProcedure:          {Resource: ResourceArtifacts, Action: ActionPush, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceCompleteArtifactUploadProcedure:          {Resource: ResourceArtifacts, Action: ActionPush, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceGetArtifactProcedure:                     {Resource: ResourceArtifacts, Action: ActionRead, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceListArtifactsProcedure:                   {Resource: ResourceArtifacts, Action: ActionRead, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceListArtifactVersionsProcedure:            {Resource: ResourceArtifacts, Action: ActionRead, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceSearchArtifactsProcedure:                 {Resource: ResourceArtifacts, Action: ActionRead},
	distrofacev1connect.ArtifactServiceListStarredArtifactRepositoriesProcedure: {Resource: ResourceArtifacts, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ArtifactServiceUpdateArtifactProcedure:                  {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceSetArtifactPropertiesProcedure:           {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceDeleteArtifactProcedure:                  {Resource: ResourceArtifacts, Action: ActionDelete, ObjectIDField: "namespace+repo_name"},

	// ── RepositoryService and ArtifactService moves ───────────────────
	distrofacev1connect.RepositoryServiceMoveRepositoryProcedure:       {Resource: ResourceRepositories, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceMoveArtifactRepositoryProcedure: {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+name"},

	// ── OrganizationService teams ─────────────────────────────────────
	distrofacev1connect.OrganizationServiceListTeamsProcedure:        {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceGetTeamProcedure:          {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceCreateTeamProcedure:       {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceUpdateTeamProcedure:       {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceDeleteTeamProcedure:       {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceListTeamMembersProcedure:  {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceAddTeamMemberProcedure:    {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceRemoveTeamMemberProcedure: {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceListTeamReposProcedure:    {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceAddTeamRepoProcedure:      {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.OrganizationServiceRemoveTeamRepoProcedure:   {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},

	// ── GitService ────────────────────────────────────────────────────
	// Browsing needs the read capability, visibility and units are filtered in-service
	distrofacev1connect.GitServiceCreateGitRepositoryProcedure:        {Resource: ResourceGit, Action: ActionCreate},
	distrofacev1connect.GitServiceGetGitRepositoryProcedure:           {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceListGitRepositoriesProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceSearchGitRepositoriesProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceUpdateGitRepositoryProcedure:        {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceDeleteGitRepositoryProcedure:        {Resource: ResourceGit, Action: ActionDelete, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceMoveGitRepositoryProcedure:          {Resource: ResourceGit, Action: ActionDelete, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceArchiveGitRepositoryProcedure:       {Resource: ResourceGit, Action: ActionDelete, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceUnarchiveGitRepositoryProcedure:     {Resource: ResourceGit, Action: ActionDelete, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceForkGitRepositoryProcedure:          {Resource: ResourceGit, Action: ActionCreate},
	distrofacev1connect.GitServiceSyncForkProcedure:                   {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceListForksProcedure:                  {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceUseTemplateProcedure:                {Resource: ResourceGit, Action: ActionCreate},
	distrofacev1connect.GitServiceSetTopicsProcedure:                  {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceListStarredGitRepositoriesProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceSyncGitRepositoryProcedure:          {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceStopGitRepositorySyncProcedure:      {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceDismissGitImportProcedure:           {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceGenerateMirrorKeyProcedure:          {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceListPushMirrorsProcedure:            {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceCreatePushMirrorProcedure:           {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceUpdatePushMirrorProcedure:           {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceDeletePushMirrorProcedure:           {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceSyncPushMirrorProcedure:             {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceListLinkedRepositoriesProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceLinkRepositoryProcedure:             {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceUnlinkRepositoryProcedure:           {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceListCollaboratorsProcedure:          {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceAddCollaboratorProcedure:            {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceUpdateCollaboratorProcedure:         {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceRemoveCollaboratorProcedure:         {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitServiceGetRepositoryActivityProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceGetRepositoryLanguagesProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceListContributorsProcedure:           {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitServiceRunMaintenanceProcedure:             {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},

	// ── GitRefService ─────────────────────────────────────────────────
	distrofacev1connect.GitRefServiceListBranchesProcedure:           {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitRefServiceGetBranchProcedure:              {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitRefServiceCreateBranchProcedure:           {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitRefServiceDeleteBranchProcedure:           {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitRefServiceRenameBranchProcedure:           {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitRefServiceListGitTagsProcedure:            {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitRefServiceGetTagProcedure:                 {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitRefServiceCreateTagProcedure:              {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitRefServiceDeleteTagProcedure:              {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitRefServiceSetBranchProtectionProcedure:    {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitRefServiceListBranchProtectionsProcedure:  {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitRefServiceDeleteBranchProtectionProcedure: {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitRefServiceSetTagProtectionProcedure:       {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitRefServiceListTagProtectionsProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitRefServiceDeleteTagProtectionProcedure:    {Resource: ResourceGit, Action: ActionUpdate, ObjectIDField: "namespace+name"},

	// ── GitFileService ────────────────────────────────────────────────
	distrofacev1connect.GitFileServiceGetTreeProcedure:            {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceGetBlobProcedure:            {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceGetReadmeProcedure:          {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceListCommitsProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceGetCommitProcedure:          {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceGetCommitDiffProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceCompareRefsProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceListCompareCommitsProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceGetCompareDiffProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceGetDiffFileProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceBlameProcedure:              {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceSearchCodeProcedure:         {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceUpdateFilesProcedure:        {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitFileServiceRevertCommitProcedure:       {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitFileServiceCherryPickCommitProcedure:   {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitFileServiceGetArchiveURLProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceGetTemplatesProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceCreateCommitStatusProcedure: {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.GitFileServiceListCommitStatusesProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.GitFileServiceGetCombinedStatusProcedure:  {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},

	// ── ReleaseService ────────────────────────────────────────────────
	distrofacev1connect.ReleaseServiceCreateReleaseProcedure:        {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.ReleaseServiceGetReleaseProcedure:           {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ReleaseServiceListReleasesProcedure:         {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ReleaseServiceUpdateReleaseProcedure:        {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.ReleaseServiceDeleteReleaseProcedure:        {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.ReleaseServicePublishReleaseProcedure:       {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.ReleaseServiceAttachReleaseAssetProcedure:   {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.ReleaseServiceDetachReleaseAssetProcedure:   {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.ReleaseServiceAttachReleaseImageProcedure:   {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.ReleaseServiceDetachReleaseImageProcedure:   {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.ReleaseServiceGetLatestReleaseProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ReleaseServiceGenerateReleaseNotesProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},

	// ── IssueService, unit levels enforced in-service ─────────────────
	distrofacev1connect.IssueServiceCreateIssueProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceGetIssueProcedure:          {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceListIssuesProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceSearchIssuesProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceUpdateIssueProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceBulkUpdateIssuesProcedure:  {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceLockIssueProcedure:         {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceUnlockIssueProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServicePinIssueProcedure:          {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceUnpinIssueProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceDeleteIssueProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceCreateCommentProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceUpdateCommentProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceDeleteCommentProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceListCommentsProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceAddReactionProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceRemoveReactionProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceCreateLabelProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceUpdateLabelProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceDeleteLabelProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceListMilestonesProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceCreateMilestoneProcedure:   {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceUpdateMilestoneProcedure:   {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceDeleteMilestoneProcedure:   {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceAddDependencyProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceRemoveDependencyProcedure:  {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceListDependenciesProcedure:  {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceAddTrackedTimeProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceDeleteTrackedTimeProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.IssueServiceListTrackedTimeProcedure:   {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},

	// ── PullRequestService, unit levels enforced in-service ───────────
	distrofacev1connect.PullRequestServiceCreatePullRequestProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceGetPullRequestProcedure:         {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceListPullRequestsProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceUpdatePullRequestProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceGetPullRequestDiffProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceGetPullRequestFileProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceListPullRequestCommitsProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceGetMergeabilityProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceMergePullRequestProcedure:       {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.PullRequestServiceScheduleAutoMergeProcedure:      {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.PullRequestServiceCancelAutoMergeProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceClosePullRequestProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceReopenPullRequestProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceUpdateBranchProcedure:           {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.PullRequestServiceRequestReviewersProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceRemoveReviewersProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceCreateReviewProcedure:           {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceSubmitReviewProcedure:           {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceDismissReviewProcedure:          {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceListReviewsProcedure:            {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceCreateReviewCommentProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceUpdateReviewCommentProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceDeleteReviewCommentProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceListReviewCommentsProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceResolveReviewThreadProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.PullRequestServiceApplySuggestionProcedure:        {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.PullRequestServiceRestoreHeadBranchProcedure:      {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},

	// ── WikiService ───────────────────────────────────────────────────
	distrofacev1connect.WikiServiceListWikiPagesProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.WikiServiceGetWikiPageProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.WikiServiceCreateWikiPageProcedure:    {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.WikiServiceUpdateWikiPageProcedure:    {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.WikiServiceDeleteWikiPageProcedure:    {Resource: ResourceGit, Action: ActionPush, ObjectIDField: "namespace+name"},
	distrofacev1connect.WikiServiceListWikiRevisionsProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},

	// ── ActionsService, repository scoped ─────────────────────────────
	distrofacev1connect.ActionsServiceListWorkflowsProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceGetWorkflowProcedure:      {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceEnableWorkflowProcedure:   {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceDisableWorkflowProcedure:  {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceDispatchWorkflowProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceListWorkflowRunsProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceGetWorkflowRunProcedure:   {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceCancelRunProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceRerunRunProcedure:         {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceApproveRunProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceGetJobProcedure:           {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceStreamJobLogProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceListRunArtifactsProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.ActionsServiceListRunImagesProcedure:    {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},

	// ── AgentsService, repository scoped ──────────────────────────────
	distrofacev1connect.AgentsServiceGetRepoAgentsProcedure:            {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceStartAgentSessionProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceListAgentSessionsProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceGetAgentSessionProcedure:          {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceCancelAgentSessionProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceRerunAgentSessionProcedure:        {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceContinueAgentSessionProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceResolveAgentApprovalProcedure:     {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceListAgentSessionEventsProcedure:   {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceStreamAgentSessionEventsProcedure: {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},
	distrofacev1connect.AgentsServiceDeleteAgentSessionProcedure:       {Resource: ResourceGit, Action: ActionRead, AnyGrant: true},

	// ── WebhookService ────────────────────────────────────────────────
	distrofacev1connect.WebhookServiceCreateWebhookProcedure:         {Resource: ResourceWebhooks, Action: ActionCreate},
	distrofacev1connect.WebhookServiceListWebhooksProcedure:          {Resource: ResourceWebhooks, Action: ActionRead},
	distrofacev1connect.WebhookServiceGetWebhookProcedure:            {Resource: ResourceWebhooks, Action: ActionRead},
	distrofacev1connect.WebhookServiceUpdateWebhookProcedure:         {Resource: ResourceWebhooks, Action: ActionUpdate},
	distrofacev1connect.WebhookServiceDeleteWebhookProcedure:         {Resource: ResourceWebhooks, Action: ActionDelete},
	distrofacev1connect.WebhookServiceListWebhookDeliveriesProcedure: {Resource: ResourceWebhooks, Action: ActionRead},
	distrofacev1connect.WebhookServiceRedeliverWebhookProcedure:      {Resource: ResourceWebhooks, Action: ActionUpdate},
}

// Field value by proto name, plus joined parts become a composite id
func ExtractObjectID(req any, field string) string {
	msg, ok := req.(proto.Message)
	if !ok {
		return "*"
	}

	if strings.Contains(field, "+") {
		parts := strings.Split(field, "+")
		values := make([]string, 0, len(parts))
		for _, part := range parts {
			fd := msg.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(part))
			if fd == nil {
				return "*"
			}
			val := msg.ProtoReflect().Get(fd).String()
			if val == "" {
				return "*"
			}
			values = append(values, val)
		}
		return strings.Join(values, "/")
	}

	fd := msg.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(field))
	if fd == nil {
		return "*"
	}
	val := msg.ProtoReflect().Get(fd)
	if str := val.String(); str != "" {
		return str
	}
	return "*"
}
