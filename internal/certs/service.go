package certs

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/pages"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.CertificateServiceHandler = (*Service)(nil)

// Lowercase host, no port
var domainRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// Engine is always constructed, cert material is runtime managed
type Service struct {
	store    *stores.Store
	enforcer *rbac.Enforcer
	engine   *Engine
	config   *config.Config
	log      *logger.Logger
	// Failed acme attempts burn shared ca quota, cap issuance per org
	orgIssue *admin.Limiter
}

func NewService(store *stores.Store, enforcer *rbac.Enforcer, engine *Engine, cfg *config.Config, log *logger.Logger) *Service {
	return &Service{
		store:    store,
		enforcer: enforcer,
		engine:   engine,
		config:   cfg,
		log:      log,
		orgIssue: admin.NewLimiter(5, time.Hour),
	}
}

func (s *Service) isSystemAdmin(ctx context.Context) bool {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return false
	}
	allowed, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceSettings, rbac.ActionManage, "*")
	return allowed
}

// System scope needs settings manage, only wildcard admins pass
func (s *Service) requireSystemAdmin(ctx context.Context) error {
	if auth.UserFromContext(ctx) == nil {
		return connect.NewError(connect.CodeUnauthenticated, nil)
	}
	if !s.isSystemAdmin(ctx) {
		return connect.NewError(connect.CodePermissionDenied, nil)
	}
	return nil
}

// Resolves the org, caller needs global org-manage or owner/admin membership
func (s *Service) requireOrgAdmin(ctx context.Context, orgID string) (*storage.Organization, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	org, err := s.store.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.ID)
	if !canManage {
		member, _ := s.store.GetOrgMember(ctx, org.ID, user.ID)
		if member == nil || (member.Role != storage.OrgRoleOwner && member.Role != storage.OrgRoleAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied, nil)
		}
	}
	return org, nil
}

// Org domains must match one of the org's portal hostnames
func (s *Service) orgOwnsHostname(ctx context.Context, org *storage.Organization, domain string) (bool, error) {
	portals, err := s.store.ListRegistryPortalsByOrg(ctx, org.ID)
	if err != nil {
		return false, err
	}
	for _, p := range portals {
		if p.Hostname != "" && strings.EqualFold(p.Hostname, domain) {
			return true, nil
		}
	}
	return false, nil
}

func certInfoToProto(info *CertInfo) *v1.CertificateInfo {
	if info == nil || !info.Issued {
		return nil
	}
	return &v1.CertificateInfo{
		Issued:    true,
		Issuer:    info.Issuer,
		NotBefore: timestamppb.New(info.NotBefore),
		NotAfter:  timestamppb.New(info.NotAfter),
		Sans:      info.SANs,
	}
}

// Cert details a handshake for domain would observe, any source
func (s *Service) servingCertProto(ctx context.Context, domain string) *v1.CertificateInfo {
	if s.engine == nil {
		return nil
	}
	return certInfoToProto(s.engine.ServingCertInfo(ctx, domain))
}

func (s *Service) domainToProto(ctx context.Context, d *storage.CertificateDomain, orgName string) *v1.CertificateDomain {
	orgID := ""
	if d.OrgID != nil {
		orgID = *d.OrgID
	}
	return &v1.CertificateDomain{
		Id:         d.ID,
		Domain:     d.Domain,
		Scope:      DomainScopeToProto(d.Scope),
		OrgId:      orgID,
		OrgName:    orgName,
		CreatedBy:  d.CreatedBy,
		CreatedAt:  timestamppb.New(d.CreatedAt),
		Cert:       s.servingCertProto(ctx, d.Domain),
		Approved:   d.Approved,
		ApprovedBy: d.ApprovedBy,
	}
}

// Parsed public details of stored pem material, key never included
func materialInfo(record *storage.TLSCertificate, includePEM bool) *v1.TLSMaterialInfo {
	if record == nil {
		return nil
	}
	info := &v1.TLSMaterialInfo{
		CreatedBy: record.CreatedBy,
		UpdatedAt: timestamppb.New(record.UpdatedAt),
	}
	if leaf := firstCertificate([]byte(record.CertPEM)); leaf != nil {
		info.Subject = leaf.Subject.CommonName
		info.Issuer = leaf.Issuer.CommonName
		info.NotBefore = timestamppb.New(leaf.NotBefore)
		info.NotAfter = timestamppb.New(leaf.NotAfter)
		info.Sans = leaf.DNSNames
		info.IsCa = leaf.IsCA
	}
	if includePEM {
		info.CertPem = record.CertPEM
	}
	return info
}

func (s *Service) tlsStatus(ctx context.Context) *v1.GetTLSStatusResponse {
	eff := s.engine.EffectiveACME()
	resp := &v1.GetTLSStatusResponse{
		TlsEnabled:              s.config.TLS.Enabled,
		AcmeEnabled:             eff.Enabled,
		AcmeEmail:               eff.Email,
		AcmeDirectory:           eff.DirectoryURL,
		AcmeHttpPort:            s.config.TLS.ACME.HTTPPort,
		ManualCert:              s.engine.ManualCertLoaded(),
		ConfigDomains:           s.config.TLS.ACME.Domains,
		PrimaryHostname:         s.config.Server.Hostname,
		PrimarySource:           CertSourceToProto(s.engine.PrimarySource()),
		HostnameBlacklist:       s.engine.Policy().Blacklist(ctx),
		RequireHostnameApproval: s.engine.Policy().RequireApproval(ctx),
	}
	resp.PrimaryCert = s.servingCertProto(ctx, bareHost(s.config.Server.Hostname))
	if app, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopeApp, "", ""); err == nil {
		resp.AppCert = materialInfo(app, false)
	}
	if ca, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopeAppCA, "", ""); err == nil {
		resp.AppCa = materialInfo(ca, true)
	}
	return resp
}

func (s *Service) GetTLSStatus(ctx context.Context, _ *connect.Request[v1.GetTLSStatusRequest]) (*connect.Response[v1.GetTLSStatusResponse], error) {
	if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}
	return connect.NewResponse(s.tlsStatus(ctx)), nil
}

func (s *Service) UpdateACMESettings(ctx context.Context, req *connect.Request[v1.UpdateACMESettingsRequest]) (*connect.Response[v1.UpdateACMESettingsResponse], error) {
	if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}
	msg := req.Msg
	if msg.Enabled != nil {
		if err := s.store.SetSystemSetting(ctx, storage.SettingACMEEnabled, strconv.FormatBool(*msg.Enabled)); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if msg.Email != nil {
		if err := s.store.SetSystemSetting(ctx, storage.SettingACMEEmail, strings.TrimSpace(*msg.Email)); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if msg.DirectoryUrl != nil {
		if err := s.store.SetSystemSetting(ctx, storage.SettingACMEDirectory, strings.TrimSpace(*msg.DirectoryUrl)); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if msg.PrimarySource != v1.CertSource_CERT_SOURCE_UNSPECIFIED {
		src, err := CertSourceFromProto(msg.PrimarySource)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		switch src {
		case storage.PrimarySourceConfig, storage.PrimarySourceManual, storage.PrimarySourceACME, storage.PrimarySourceAppCA:
		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("primary source must be config, manual, acme, or app_ca"))
		}
		if err := s.store.SetSystemSetting(ctx, storage.SettingPrimarySource, src); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if msg.RequireHostnameApproval != nil {
		if err := s.store.SetSystemSetting(ctx, storage.SettingRequireApproval, strconv.FormatBool(*msg.RequireHostnameApproval)); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if msg.SetBlacklist {
		patterns := make([]string, 0, len(msg.HostnameBlacklist))
		for _, p := range msg.HostnameBlacklist {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			if !domainRegex.MatchString(strings.TrimPrefix(p, "*.")) {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid blacklist pattern %q", p))
			}
			patterns = append(patterns, p)
		}
		encoded, err := json.Marshal(patterns)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if err := s.store.SetSystemSetting(ctx, storage.SettingHostnameBlacklist, string(encoded)); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	s.engine.Invalidate(ctx)
	s.log.Info("tls settings updated at runtime")
	return connect.NewResponse(&v1.UpdateACMESettingsResponse{Status: s.tlsStatus(ctx)}), nil
}

// Resolves and authorizes a tls material target by scope
func (s *Service) resolveCertTarget(ctx context.Context, protoScope v1.TLSScope, orgID, portalID string) (*storage.TLSCertificate, error) {
	scope, err := TLSScopeFromProto(protoScope)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	target := &storage.TLSCertificate{Scope: scope}
	switch scope {
	case storage.TLSCertScopeApp, storage.TLSCertScopeAppCA:
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
	case storage.TLSCertScopeOrg, storage.TLSCertScopeOrgCA:
		org, err := s.requireOrgAdmin(ctx, orgID)
		if err != nil {
			return nil, err
		}
		target.OrgID = org.ID
	case storage.TLSCertScopePortal:
		org, err := s.requireOrgAdmin(ctx, orgID)
		if err != nil {
			return nil, err
		}
		portal, err := s.store.GetRegistryPortal(ctx, portalID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if portal == nil || portal.OrgID != org.ID {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("portal not found"))
		}
		target.OrgID = org.ID
		target.PortalID = portal.ID
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid scope %q", scope))
	}
	return target, nil
}

func (s *Service) UploadTLSCertificate(ctx context.Context, req *connect.Request[v1.UploadTLSCertificateRequest]) (*connect.Response[v1.UploadTLSCertificateResponse], error) {
	msg := req.Msg
	target, err := s.resolveCertTarget(ctx, msg.Scope, msg.OrgId, msg.PortalId)
	if err != nil {
		return nil, err
	}
	if _, err := tls.X509KeyPair([]byte(msg.CertPem), []byte(msg.KeyPem)); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid certificate or key: %v", err))
	}
	if target.Scope == storage.TLSCertScopeOrgCA || target.Scope == storage.TLSCertScopeAppCA {
		if leaf := firstCertificate([]byte(msg.CertPem)); leaf == nil || !leaf.IsCA {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("scope %s requires a ca certificate", target.Scope))
		}
	}
	target.CertPEM = msg.CertPem
	target.KeyPEM = msg.KeyPem
	if user := auth.UserFromContext(ctx); user != nil {
		target.CreatedBy = user.Username
	}
	if err := s.store.SaveTLSCertificate(ctx, target); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.engine.Invalidate(ctx)
	s.log.Info("tls material uploaded: scope %s org %s portal %s", target.Scope, target.OrgID, target.PortalID)

	includePEM := target.Scope == storage.TLSCertScopeOrgCA || target.Scope == storage.TLSCertScopeAppCA
	return connect.NewResponse(&v1.UploadTLSCertificateResponse{
		Info: materialInfo(target, includePEM),
	}), nil
}

func (s *Service) DeleteTLSCertificate(ctx context.Context, req *connect.Request[v1.DeleteTLSCertificateRequest]) (*connect.Response[v1.DeleteTLSCertificateResponse], error) {
	msg := req.Msg
	target, err := s.resolveCertTarget(ctx, msg.Scope, msg.OrgId, msg.PortalId)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteTLSCertificate(ctx, target.Scope, target.OrgID, target.PortalID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.engine.Invalidate(ctx)
	s.log.Info("tls material removed: scope %s org %s portal %s", target.Scope, target.OrgID, target.PortalID)

	return connect.NewResponse(&v1.DeleteTLSCertificateResponse{}), nil
}

func (s *Service) GetTLSMaterial(ctx context.Context, req *connect.Request[v1.GetTLSMaterialRequest]) (*connect.Response[v1.GetTLSMaterialResponse], error) {
	msg := req.Msg
	resp := &v1.GetTLSMaterialResponse{}

	if msg.OrgId == "" {
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
		app, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopeApp, "", "")
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		resp.AppCert = materialInfo(app, false)
		appCA, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopeAppCA, "", "")
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		resp.AppCa = materialInfo(appCA, true)
		return connect.NewResponse(resp), nil
	}

	org, err := s.requireOrgAdmin(ctx, msg.OrgId)
	if err != nil {
		return nil, err
	}
	orgCert, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopeOrg, org.ID, "")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp.OrgCert = materialInfo(orgCert, false)
	orgCA, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopeOrgCA, org.ID, "")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp.OrgCa = materialInfo(orgCA, true)
	// Orgs only learn whether an ica can be issued, never the root key material
	if appCA, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopeAppCA, "", ""); err == nil && appCA != nil {
		resp.AppCaExists = true
	}

	if msg.PortalId != "" {
		portal, err := s.store.GetRegistryPortal(ctx, msg.PortalId)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if portal == nil || portal.OrgID != org.ID {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("portal not found"))
		}
		portalCert, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopePortal, org.ID, portal.ID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		resp.PortalCert = materialInfo(portalCert, false)
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) GenerateOrgCA(ctx context.Context, req *connect.Request[v1.GenerateOrgCARequest]) (*connect.Response[v1.GenerateOrgCAResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
	if err != nil {
		return nil, err
	}
	cn := strings.TrimSpace(req.Msg.CommonName)
	if cn == "" {
		cn = org.Name
	}
	certPEM, keyPEM, err := GenerateCA(cn)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	record := &storage.TLSCertificate{
		Scope:   storage.TLSCertScopeOrgCA,
		OrgID:   org.ID,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	}
	if user := auth.UserFromContext(ctx); user != nil {
		record.CreatedBy = user.Username
	}
	if err := s.store.SaveTLSCertificate(ctx, record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.engine.Invalidate(ctx)
	s.log.Info("org ca generated: %s (org %s)", cn, org.Name)

	return connect.NewResponse(&v1.GenerateOrgCAResponse{OrgCa: materialInfo(record, true)}), nil
}

func (s *Service) GenerateAppCA(ctx context.Context, req *connect.Request[v1.GenerateAppCARequest]) (*connect.Response[v1.GenerateAppCAResponse], error) {
	if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}
	cn := strings.TrimSpace(req.Msg.CommonName)
	if cn == "" {
		cn = bareHost(s.config.Server.Hostname)
	}
	if cn == "" {
		cn = "distroface"
	}
	certPEM, keyPEM, err := GenerateRootCA(cn)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	record := &storage.TLSCertificate{
		Scope:   storage.TLSCertScopeAppCA,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	}
	if user := auth.UserFromContext(ctx); user != nil {
		record.CreatedBy = user.Username
	}
	if err := s.store.SaveTLSCertificate(ctx, record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.engine.Invalidate(ctx)
	s.log.Info("instance root ca generated: %s", cn)

	return connect.NewResponse(&v1.GenerateAppCAResponse{AppCa: materialInfo(record, true)}), nil
}

func (s *Service) IssueOrgICA(ctx context.Context, req *connect.Request[v1.IssueOrgICARequest]) (*connect.Response[v1.IssueOrgICAResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
	if err != nil {
		return nil, err
	}
	root, err := s.store.GetTLSCertificate(ctx, storage.TLSCertScopeAppCA, "", "")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if root == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the instance has no root ca, ask a system administrator to create one"))
	}
	cn := strings.TrimSpace(req.Msg.CommonName)
	if cn == "" {
		cn = org.Name
	}
	certPEM, keyPEM, err := IssueICA(root.CertPEM, root.KeyPEM, cn)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	record := &storage.TLSCertificate{
		Scope:   storage.TLSCertScopeOrgCA,
		OrgID:   org.ID,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	}
	if user := auth.UserFromContext(ctx); user != nil {
		record.CreatedBy = user.Username
	}
	if err := s.store.SaveTLSCertificate(ctx, record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.engine.Invalidate(ctx)
	s.log.Info("org ica issued from instance root: %s (org %s)", cn, org.Name)

	return connect.NewResponse(&v1.IssueOrgICAResponse{OrgCa: materialInfo(record, true)}), nil
}

func statusToProto(st *Status) *v1.GetCertStatusResponse {
	return &v1.GetCertStatusResponse{
		Source:        CertSourceToProto(st.Source),
		State:         CertStateToProto(st.State),
		Problems:      st.Problems,
		AcmeDirectory: st.Directory,
		AcmeEmail:     st.Email,
		ServingCert:   materialInfo(st.Material, false),
		AcmeCert:      certInfoToProto(st.ACMEInfo),
	}
}

func (s *Service) GetCertStatus(ctx context.Context, req *connect.Request[v1.GetCertStatusRequest]) (*connect.Response[v1.GetCertStatusResponse], error) {
	msg := req.Msg
	if msg.OrgId == "" {
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
		return connect.NewResponse(statusToProto(s.engine.AppStatus(ctx))), nil
	}
	org, err := s.requireOrgAdmin(ctx, msg.OrgId)
	if err != nil {
		return nil, err
	}
	if msg.PortalId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("portal_id required with org_id"))
	}
	portal, err := s.store.GetRegistryPortal(ctx, msg.PortalId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if portal == nil || portal.OrgID != org.ID {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("portal not found"))
	}
	return connect.NewResponse(statusToProto(s.engine.PortalStatus(ctx, portal))), nil
}

func (s *Service) ListCertificateDomains(ctx context.Context, req *connect.Request[v1.ListCertificateDomainsRequest]) (*connect.Response[v1.ListCertificateDomainsResponse], error) {
	resp := &v1.ListCertificateDomainsResponse{}
	limit, offset := pages.Parse(req.Msg.Page)
	q := pages.ParseQuery(req.Msg.Page)
	if err := stores.CertDomainsQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	scope := ""
	if req.Msg.Scope != v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_UNSPECIFIED {
		var err error
		if scope, err = DomainScopeFromProto(req.Msg.Scope); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}

	if req.Msg.OrgId != "" {
		org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
		domains, total, err := s.store.ListCertificateDomainsByOrg(ctx, org.ID, q, req.Msg.PendingOnly, limit, offset)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		for _, d := range domains {
			resp.Domains = append(resp.Domains, s.domainToProto(ctx, d, org.Name))
		}
		resp.Page = pages.Info(offset, limit, total)
		return connect.NewResponse(resp), nil
	}

	if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}
	domains, total, err := s.store.ListCertificateDomains(ctx, q, scope, req.Msg.PendingOnly, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	for _, d := range domains {
		orgName := ""
		if d.Org != nil {
			orgName = d.Org.Name
		}
		resp.Domains = append(resp.Domains, s.domainToProto(ctx, d, orgName))
	}
	resp.Page = pages.Info(offset, limit, total)
	return connect.NewResponse(resp), nil
}

func (s *Service) AddCertificateDomain(ctx context.Context, req *connect.Request[v1.AddCertificateDomainRequest]) (*connect.Response[v1.AddCertificateDomainResponse], error) {
	domain := bareHost(req.Msg.Domain)
	if domain == "" || !domainRegex.MatchString(domain) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid domain"))
	}
	if err := s.engine.Policy().Blocked(ctx, domain); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Hostnames belong to org portals, the app tier has one identity
	if req.Msg.OrgId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("organization is required"))
	}
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
	if err != nil {
		return nil, err
	}
	record := &storage.CertificateDomain{
		Domain: domain, Scope: storage.CertDomainScopeOrg, OrgID: &org.ID,
	}
	orgName := org.Name

	existing, err := s.store.GetCertificateDomainByName(ctx, domain)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("domain already registered"))
	}

	user := auth.UserFromContext(ctx)
	if user != nil {
		record.CreatedBy = user.Username
	}
	// Org registrations wait for approval unless a system admin made them
	if s.isSystemAdmin(ctx) {
		record.Approved = true
		if user != nil {
			record.ApprovedBy = user.Username
		}
	}
	if err := s.store.CreateCertificateDomain(ctx, record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.log.Info("certificate domain registered: %s (scope %s approved %v)", domain, record.Scope, record.Approved)

	return connect.NewResponse(&v1.AddCertificateDomainResponse{Domain: s.domainToProto(ctx, record, orgName)}), nil
}

func (s *Service) RemoveCertificateDomain(ctx context.Context, req *connect.Request[v1.RemoveCertificateDomainRequest]) (*connect.Response[v1.RemoveCertificateDomainResponse], error) {
	record, err := s.store.GetCertificateDomain(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	if req.Msg.OrgId != "" {
		org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
		if record.OrgID == nil || *record.OrgID != org.ID {
			return nil, connect.NewError(connect.CodeNotFound, nil)
		}
	} else if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}

	if err := s.store.DeleteCertificateDomain(ctx, record.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.log.Info("certificate domain removed: %s", record.Domain)

	return connect.NewResponse(&v1.RemoveCertificateDomainResponse{}), nil
}

func (s *Service) BulkRemoveCertificateDomains(ctx context.Context, req *connect.Request[v1.BulkRemoveCertificateDomainsRequest]) (*connect.Response[v1.BulkRemoveCertificateDomainsResponse], error) {
	if len(req.Msg.Ids) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no domain ids provided"))
	}

	var org *storage.Organization
	if req.Msg.OrgId != "" {
		var err error
		org, err = s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
	} else if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}

	records, err := s.store.GetCertificateDomainsByIDs(ctx, req.Msg.Ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	byID := make(map[string]*storage.CertificateDomain, len(records))
	for _, r := range records {
		byID[r.ID] = r
	}

	resp := &v1.BulkRemoveCertificateDomainsResponse{}
	var deleteIDs []string
	for _, id := range req.Msg.Ids {
		record := byID[id]
		if record == nil || (org != nil && (record.OrgID == nil || *record.OrgID != org.ID)) {
			resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: "domain not found"})
			continue
		}
		deleteIDs = append(deleteIDs, id)
	}
	if err := s.store.DeleteCertificateDomains(ctx, deleteIDs); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp.RemovedCount = int32(len(deleteIDs))
	if len(deleteIDs) > 0 {
		s.log.Info("certificate domains removed: %d", len(deleteIDs))
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) ApproveCertificateDomain(ctx context.Context, req *connect.Request[v1.ApproveCertificateDomainRequest]) (*connect.Response[v1.ApproveCertificateDomainResponse], error) {
	if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}
	record, err := s.store.GetCertificateDomain(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	if !record.Approved {
		record.Approved = true
		if user := auth.UserFromContext(ctx); user != nil {
			record.ApprovedBy = user.Username
		}
		if err := s.store.UpdateCertificateDomain(ctx, record); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		s.log.Info("certificate domain approved: %s (by %s)", record.Domain, record.ApprovedBy)
	}
	orgName := ""
	if record.Org != nil {
		orgName = record.Org.Name
	}
	return connect.NewResponse(&v1.ApproveCertificateDomainResponse{Domain: s.domainToProto(ctx, record, orgName)}), nil
}

func (s *Service) IssueCertificate(ctx context.Context, req *connect.Request[v1.IssueCertificateRequest]) (*connect.Response[v1.IssueCertificateResponse], error) {
	domain := bareHost(req.Msg.Domain)

	if req.Msg.OrgId != "" {
		org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
		// The hostname must belong to the org via a portal or a registration
		owns, err := s.orgOwnsHostname(ctx, org, domain)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if !owns {
			record, err := s.store.GetCertificateDomainByName(ctx, domain)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
			if record == nil || record.OrgID == nil || *record.OrgID != org.ID {
				return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("domain is not associated with this organization"))
			}
		}
		if err := s.engine.Policy().Allowed(ctx, domain, org.ID); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if allowed, _, resetAt := s.orgIssue.Take("org:" + org.ID); !allowed {
			return nil, connect.NewError(connect.CodeResourceExhausted,
				fmt.Errorf("issuance rate limit reached for this organization, retry in %s", time.Until(resetAt).Round(time.Second)))
		}
	} else if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}

	info, err := s.engine.EnsureCertificate(ctx, domain)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("issuance failed: %w", err))
	}
	s.log.Info("certificate issued for %s (expires %s)", domain, info.NotAfter)

	return connect.NewResponse(&v1.IssueCertificateResponse{Cert: &v1.CertificateInfo{
		Issued:    info.Issued,
		Issuer:    info.Issuer,
		NotBefore: timestamppb.New(info.NotBefore),
		NotAfter:  timestamppb.New(info.NotAfter),
		Sans:      info.SANs,
	}}), nil
}
