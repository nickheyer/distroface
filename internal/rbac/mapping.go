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
}

// PublicProcedures lists RPC procedures that require no authentication.
var PublicProcedures = map[string]bool{
	distrofacev1connect.AuthServiceRegisterProcedure:                  true,
	distrofacev1connect.AuthServiceLoginProcedure:                     true,
	distrofacev1connect.AuthServiceGetAuthStatusProcedure:             true,
	distrofacev1connect.AuthServiceGetOIDCLoginURLProcedure:           true,
	distrofacev1connect.HealthServiceHealthCheckProcedure: true,
	// Anonymous callers receive the redacted public subset only
	distrofacev1connect.SettingsServiceGetEffectiveSettingsProcedure: true,
	// Public repo browsing (visibility filtering handled in service)
	distrofacev1connect.RepositoryServiceGetRepositoryProcedure:    true,
	distrofacev1connect.RepositoryServiceListRepositoriesProcedure: true,
	distrofacev1connect.RepositoryServiceListTagsProcedure:         true,
	distrofacev1connect.RepositoryServiceResolveTagProcedure:       true,
	distrofacev1connect.UserServiceGetUserProcedure:                true,
	// Invite validation is public (used during registration)
	distrofacev1connect.AuthServiceValidateInviteProcedure: true,
	// Portal identity for the serving host, needed pre-login
	distrofacev1connect.PortalServiceResolvePortalProcedure: true,
}

// AuthenticatedOnlyProcedures lists RPC procedures that require authentication
// but no specific resource permission.
var AuthenticatedOnlyProcedures = map[string]bool{
	// Auth - user operations
	distrofacev1connect.AuthServiceGetCurrentUserProcedure: true,
	distrofacev1connect.AuthServiceLogoutProcedure:         true,
	distrofacev1connect.AuthServiceRefreshSessionProcedure: true,

	// User - self-service
	distrofacev1connect.UserServiceUpdateUserProcedure:     true,
	distrofacev1connect.UserServiceChangePasswordProcedure: true,

	// Stars - read access enforced in-service
	distrofacev1connect.RepositoryServiceStarRepositoryProcedure:          true,
	distrofacev1connect.RepositoryServiceUnstarRepositoryProcedure:        true,
	distrofacev1connect.RepositoryServiceListStarredRepositoriesProcedure: true,

	// Org slug resolution, object scoped read enforced in-service
	distrofacev1connect.OrganizationServiceGetOrganizationProcedure: true,

	// Settings scope permissions enforced in-service per tier
	distrofacev1connect.SettingsServiceGetSettingsProcedure:    true,
	distrofacev1connect.SettingsServiceUpdateSettingsProcedure: true,

	// Target org derived from the row in-service
	distrofacev1connect.CertificateServiceRemoveCertificateDomainProcedure:      true,
	distrofacev1connect.CertificateServiceBulkRemoveCertificateDomainsProcedure: true,
	distrofacev1connect.CertificateServiceIssueCertificateProcedure:             true,
}

// ProcedurePermissions maps each RPC procedure path to the resource and action
// required to invoke it, plus an optional ObjectIDField for per-object scoping.
var ProcedurePermissions = map[string]ProcedurePermission{
	// ── RepositoryService ─────────────────────────────────────────────
	distrofacev1connect.RepositoryServiceDeleteRepositoryProcedure: {Resource: ResourceRepositories, Action: ActionDelete, ObjectIDField: "namespace+name"},
	distrofacev1connect.RepositoryServiceUpdateRepositoryProcedure: {Resource: ResourceRepositories, Action: ActionUpdate, ObjectIDField: "namespace+name"},

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
	distrofacev1connect.AuthServiceCreateInviteProcedure: {Resource: ResourceSettings, Action: ActionCreate},
	distrofacev1connect.AuthServiceListInvitesProcedure:        {Resource: ResourceSettings, Action: ActionRead},
	distrofacev1connect.AuthServiceGetInviteProcedure:          {Resource: ResourceSettings, Action: ActionRead},
	distrofacev1connect.AuthServiceDeleteInviteProcedure:       {Resource: ResourceSettings, Action: ActionDelete},
	distrofacev1connect.AuthServiceBulkDeleteInvitesProcedure:  {Resource: ResourceSettings, Action: ActionDelete},

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
	distrofacev1connect.CertificateServiceDeleteTLSCertificateProcedure:         {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceGetTLSMaterialProcedure:               {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceGenerateOrgCAProcedure:                {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceGenerateAppCAProcedure:                {Resource: ResourceSettings, Action: ActionManage},
	distrofacev1connect.CertificateServiceIssueOrgICAProcedure:                  {Resource: ResourceOrganizations, Action: ActionUpdate, ObjectIDField: "org_id"},
	distrofacev1connect.CertificateServiceGetCertStatusProcedure:                {Resource: ResourceOrganizations, Action: ActionRead, ObjectIDField: "org_id"},

	// ── AuditService (admin) ──────────────────────────────────────────
	distrofacev1connect.AuditServiceListAuditEventsProcedure: {Resource: ResourceSettings, Action: ActionRead},

	// ── ArtifactService ───────────────────────────────────────────────
	distrofacev1connect.ArtifactServiceCreateArtifactRepositoryProcedure: {Resource: ResourceArtifacts, Action: ActionCreate},
	distrofacev1connect.ArtifactServiceGetArtifactRepositoryProcedure:    {Resource: ResourceArtifacts, Action: ActionRead, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceListArtifactRepositoriesProcedure: {Resource: ResourceArtifacts, Action: ActionRead},
	distrofacev1connect.ArtifactServiceUpdateArtifactRepositoryProcedure: {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceDeleteArtifactRepositoryProcedure: {Resource: ResourceArtifacts, Action: ActionDelete, ObjectIDField: "namespace+name"},
	distrofacev1connect.ArtifactServiceInitiateArtifactUploadProcedure:   {Resource: ResourceArtifacts, Action: ActionPush, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceCompleteArtifactUploadProcedure:   {Resource: ResourceArtifacts, Action: ActionPush, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceGetArtifactProcedure:              {Resource: ResourceArtifacts, Action: ActionRead, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceListArtifactsProcedure:            {Resource: ResourceArtifacts, Action: ActionRead, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceListArtifactVersionsProcedure:     {Resource: ResourceArtifacts, Action: ActionRead, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceSearchArtifactsProcedure:          {Resource: ResourceArtifacts, Action: ActionRead},
	distrofacev1connect.ArtifactServiceUpdateArtifactProcedure:           {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceSetArtifactPropertiesProcedure:    {Resource: ResourceArtifacts, Action: ActionUpdate, ObjectIDField: "namespace+repo_name"},
	distrofacev1connect.ArtifactServiceDeleteArtifactProcedure:           {Resource: ResourceArtifacts, Action: ActionDelete, ObjectIDField: "namespace+repo_name"},

	// ── WebhookService ────────────────────────────────────────────────
	distrofacev1connect.WebhookServiceCreateWebhookProcedure:         {Resource: ResourceWebhooks, Action: ActionCreate},
	distrofacev1connect.WebhookServiceListWebhooksProcedure:          {Resource: ResourceWebhooks, Action: ActionRead},
	distrofacev1connect.WebhookServiceGetWebhookProcedure:            {Resource: ResourceWebhooks, Action: ActionRead},
	distrofacev1connect.WebhookServiceUpdateWebhookProcedure:         {Resource: ResourceWebhooks, Action: ActionUpdate},
	distrofacev1connect.WebhookServiceDeleteWebhookProcedure:         {Resource: ResourceWebhooks, Action: ActionDelete},
	distrofacev1connect.WebhookServiceListWebhookDeliveriesProcedure: {Resource: ResourceWebhooks, Action: ActionRead},
	distrofacev1connect.WebhookServiceRedeliverWebhookProcedure:      {Resource: ResourceWebhooks, Action: ActionUpdate},
}

// ExtractObjectID extracts a field value from a protobuf request using reflection.
// If field contains "+", it splits on "+", extracts each proto field, and joins
// with "/" to form a composite ID (e.g., "namespace+name" → "nick/myimage").
func ExtractObjectID(req interface{ Any() any }, field string) string {
	msg, ok := req.Any().(proto.Message)
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
