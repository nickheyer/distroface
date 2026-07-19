package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/settings"
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
	res      *settings.Resolver
	log      *logger.Logger
	// Failed acme attempts burn shared ca quota, cap issuance per org
	orgIssue *admin.Limiter
}

func NewService(store *stores.Store, enforcer *rbac.Enforcer, engine *Engine, res *settings.Resolver, log *logger.Logger) *Service {
	return &Service{
		store:    store,
		enforcer: enforcer,
		engine:   engine,
		res:      res,
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

// Row level gate, org rows accept their org admins, the rest need system
func (s *Service) requireDomainAdmin(ctx context.Context, record *storage.CertificateDomain) error {
	if record.OrgID != nil && !s.isSystemAdmin(ctx) {
		_, err := s.requireOrgAdmin(ctx, *record.OrgID)
		return err
	}
	return s.requireSystemAdmin(ctx)
}

// Cert details a handshake for domain would observe, any source
func (s *Service) servingCertProto(ctx context.Context, domain string) *v1.CertificateInfo {
	if s.engine == nil {
		return nil
	}
	return s.engine.ServingCertInfo(ctx, domain)
}

func (s *Service) domainToProto(ctx context.Context, d *storage.CertificateDomain, orgName string) *v1.CertificateDomain {
	orgID := ""
	if d.OrgID != nil {
		orgID = *d.OrgID
	}
	return &v1.CertificateDomain{
		Id:         d.ID,
		Domain:     d.Domain,
		Scope:      d.Scope,
		OrgId:      orgID,
		OrgName:    orgName,
		CreatedBy:  d.CreatedBy,
		CreatedAt:  timestamppb.New(d.CreatedAt),
		Cert:       s.servingCertProto(ctx, d.Domain),
		Approved:   d.Approved,
		ApprovedBy: d.ApprovedBy,
	}
}

// Resolves and authorizes a tls material target by scope
func (s *Service) resolveCertTarget(ctx context.Context, scope v1.TLSScope, orgID, portalID string) (*storage.TLSCertificate, error) {
	target := &storage.TLSCertificate{Scope: scope}
	switch scope {
	case v1.TLSScope_TLS_SCOPE_APP, v1.TLSScope_TLS_SCOPE_APP_CA:
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
	case v1.TLSScope_TLS_SCOPE_ORG, v1.TLSScope_TLS_SCOPE_ORG_CA:
		org, err := s.requireOrgAdmin(ctx, orgID)
		if err != nil {
			return nil, err
		}
		target.OrgID = org.ID
	case v1.TLSScope_TLS_SCOPE_PORTAL:
		portal, err := s.store.GetRegistryPortal(ctx, portalID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if portal == nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("portal not found"))
		}
		org, err := s.requireOrgAdmin(ctx, portal.OrgID)
		if err != nil {
			return nil, err
		}
		target.OrgID = org.ID
		target.PortalID = portal.ID
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid scope %v", scope))
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
	isCAScope := target.Scope == v1.TLSScope_TLS_SCOPE_ORG_CA || target.Scope == v1.TLSScope_TLS_SCOPE_APP_CA
	if isCAScope {
		if err := ValidateCABundle([]byte(msg.CertPem)); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
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
	s.log.Info("tls material uploaded: scope %v org %s portal %s", target.Scope, target.OrgID, target.PortalID)
	// A reimported root re-derives the acme issuer and flags orphans
	if target.Scope == v1.TLSScope_TLS_SCOPE_APP_CA {
		s.reconcileRoot(ctx)
	} else {
		s.engine.Invalidate(ctx)
	}

	return connect.NewResponse(&v1.UploadTLSCertificateResponse{
		Info: materialInfo(target, isCAScope),
	}), nil
}

func (s *Service) DeleteTLSCertificate(ctx context.Context, req *connect.Request[v1.DeleteTLSCertificateRequest]) (*connect.Response[v1.DeleteTLSCertificateResponse], error) {
	msg := req.Msg
	target, err := s.resolveCertTarget(ctx, msg.Scope, msg.OrgId, msg.PortalId)
	if err != nil {
		return nil, err
	}
	// Removing the root strands every intermediate that chains to it
	if target.Scope == v1.TLSScope_TLS_SCOPE_APP_CA {
		if deps := s.engine.OrgICADependents(ctx); len(deps) > 0 {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%d organization intermediate cas chain to this root, re-issue or remove them first", len(deps)))
		}
	}
	if err := s.store.DeleteTLSCertificate(ctx, target.Scope, target.OrgID, target.PortalID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// The acme issuer is derived from the root, drop it to re-mint later
	if target.Scope == v1.TLSScope_TLS_SCOPE_APP_CA {
		_ = s.store.DeleteTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_ACME_CA, "", "")
	}
	s.engine.Invalidate(ctx)
	s.log.Info("tls material removed: scope %v org %s portal %s", target.Scope, target.OrgID, target.PortalID)

	return connect.NewResponse(&v1.DeleteTLSCertificateResponse{}), nil
}

func (s *Service) GetTLSMaterial(ctx context.Context, req *connect.Request[v1.GetTLSMaterialRequest]) (*connect.Response[v1.GetTLSMaterialResponse], error) {
	msg := req.Msg
	resp := &v1.GetTLSMaterialResponse{}

	if msg.OrgId == "" {
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
		app, err := s.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_APP, "", "")
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		resp.AppCert = materialInfo(app, false)
		appCA, err := s.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_APP_CA, "", "")
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
	orgCert, err := s.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_ORG, org.ID, "")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp.OrgCert = materialInfo(orgCert, false)
	orgCA, err := s.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_ORG_CA, org.ID, "")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp.OrgCa = materialInfo(orgCA, true)
	// Flag an intermediate that no longer chains to the current root
	if orgCA != nil && resp.OrgCa != nil {
		resp.OrgCa.Orphaned = s.engine.Orphaned(ctx, orgCA.CertPEM)
	}
	// Orgs only learn whether an ica can be issued, never the root key material
	if appCA, err := s.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_APP_CA, "", ""); err == nil && appCA != nil {
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
		portalCert, err := s.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_PORTAL, org.ID, portal.ID)
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
		Scope:   v1.TLSScope_TLS_SCOPE_ORG_CA,
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
		// A distinct name avoids colliding with minted leaf hostnames
		cn = "DistroFace Instance Root CA"
	}
	certPEM, keyPEM, err := GenerateRootCA(cn)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	record := &storage.TLSCertificate{
		Scope:   v1.TLSScope_TLS_SCOPE_APP_CA,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	}
	if user := auth.UserFromContext(ctx); user != nil {
		record.CreatedBy = user.Username
	}
	if err := s.store.SaveTLSCertificate(ctx, record); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.log.Info("instance root ca generated: %s", cn)
	s.reconcileRoot(ctx)

	return connect.NewResponse(&v1.GenerateAppCAResponse{AppCa: materialInfo(record, true)}), nil
}

// Re-mints the acme issuer and warns about org intermediates orphaned
// by a rotated or reimported instance root
func (s *Service) reconcileRoot(ctx context.Context) {
	if orphaned := s.engine.ReconcileAppCADependents(ctx); len(orphaned) > 0 {
		s.log.Warn("instance root changed, %d org intermediate cas no longer chain and need re-issue: %v", len(orphaned), orphaned)
	}
}

func (s *Service) IssueOrgICA(ctx context.Context, req *connect.Request[v1.IssueOrgICARequest]) (*connect.Response[v1.IssueOrgICAResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
	if err != nil {
		return nil, err
	}
	root, err := s.store.GetTLSCertificate(ctx, v1.TLSScope_TLS_SCOPE_APP_CA, "", "")
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
		Scope:   v1.TLSScope_TLS_SCOPE_ORG_CA,
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

func (s *Service) SignCSR(ctx context.Context, req *connect.Request[v1.SignCSRRequest]) (*connect.Response[v1.SignCSRResponse], error) {
	block, _ := pem.Decode([]byte(req.Msg.CsrPem))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("expected a pem certificate request"))
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid csr: %w", err))
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("csr signature invalid: %w", err))
	}

	orgID := ""
	if req.Msg.OrgId == "" {
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
	} else {
		org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
		orgID = org.ID
	}

	sans := csrSANs(csr)
	if len(sans) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("csr has no subject names"))
	}
	signed, err := s.engine.SignServerCert(ctx, orgID, csr.PublicKey, sans, int(req.Msg.ValidityDays))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if user := auth.UserFromContext(ctx); user != nil {
		s.log.Info("csr signed for %v by %s (org %q)", sans, user.Username, req.Msg.OrgId)
	}
	return connect.NewResponse(&v1.SignCSRResponse{
		CertPem: signed.CertPEM,
		Cert:    certInfoFromLeaf(signed.Leaf),
	}), nil
}

// Union of the csr common name and dns sans
func csrSANs(csr *x509.CertificateRequest) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if csr.Subject.CommonName != "" {
		add(csr.Subject.CommonName)
	}
	for _, d := range csr.DNSNames {
		add(d)
	}
	for _, ip := range csr.IPAddresses {
		add(ip.String())
	}
	return out
}

func (s *Service) GetCertStatus(ctx context.Context, req *connect.Request[v1.GetCertStatusRequest]) (*connect.Response[v1.GetCertStatusResponse], error) {
	msg := req.Msg
	if msg.OrgId == "" {
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
		return connect.NewResponse(s.engine.AppStatus(ctx)), nil
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
	return connect.NewResponse(s.engine.PortalStatus(ctx, portal)), nil
}

func (s *Service) ListCertificateDomains(ctx context.Context, req *connect.Request[v1.ListCertificateDomainsRequest]) (*connect.Response[v1.ListCertificateDomainsResponse], error) {
	resp := &v1.ListCertificateDomainsResponse{}
	limit, offset := pages.Parse(req.Msg.Page)
	q := pages.ParseQuery(req.Msg.Page)
	if err := stores.CertDomainsQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
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
	domains, total, err := s.store.ListCertificateDomains(ctx, q, req.Msg.Scope, req.Msg.PendingOnly, limit, offset)
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

	record := &storage.CertificateDomain{Domain: domain}
	orgName := ""
	if req.Msg.OrgId == "" {
		// Instance level registration, always approved
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
		record.Scope = v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_SYSTEM
	} else {
		org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
		record.Scope = v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_ORG
		record.OrgID = &org.ID
		orgName = org.Name
	}

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
	s.log.Info("certificate domain registered: %s (scope %v approved %v)", domain, record.Scope, record.Approved)

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
	if err := s.requireDomainAdmin(ctx, record); err != nil {
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
	records, err := s.store.GetCertificateDomainsByIDs(ctx, req.Msg.Ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	byID := make(map[string]*storage.CertificateDomain, len(records))
	for _, r := range records {
		byID[r.ID] = r
	}

	resp := &v1.BulkRemoveCertificateDomainsResponse{}
	adminByOrg := map[string]bool{}
	var deleteIDs []string
	for _, id := range req.Msg.Ids {
		record := byID[id]
		if record == nil {
			resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: "domain not found"})
			continue
		}
		allowed := s.isSystemAdmin(ctx)
		if !allowed && record.OrgID != nil {
			orgID := *record.OrgID
			if _, cached := adminByOrg[orgID]; !cached {
				_, err := s.requireOrgAdmin(ctx, orgID)
				adminByOrg[orgID] = err == nil
			}
			allowed = adminByOrg[orgID]
		}
		if !allowed {
			resp.Errors = append(resp.Errors, &v1.BulkOperationError{Id: id, Error: "permission denied"})
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
	var domain, orgID string

	switch target := req.Msg.Target.(type) {
	case *v1.IssueCertificateRequest_DomainId:
		record, err := s.store.GetCertificateDomain(ctx, target.DomainId)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if record == nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("domain not found"))
		}
		if err := s.requireDomainAdmin(ctx, record); err != nil {
			return nil, err
		}
		domain = record.Domain
		if record.OrgID != nil {
			orgID = *record.OrgID
		}
	case *v1.IssueCertificateRequest_PortalId:
		portal, err := s.store.GetRegistryPortal(ctx, target.PortalId)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if portal == nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("portal not found"))
		}
		if _, err := s.requireOrgAdmin(ctx, portal.OrgID); err != nil {
			return nil, err
		}
		if portal.Hostname == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("portal has no hostname"))
		}
		domain = bareHost(portal.Hostname)
		orgID = portal.OrgID
	default:
		if err := s.requireSystemAdmin(ctx); err != nil {
			return nil, err
		}
		domain = s.engine.primaryHost(ctx)
	}

	if orgID != "" {
		if err := s.engine.Policy().Allowed(ctx, domain, orgID); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if allowed, _, resetAt := s.orgIssue.Take("org:" + orgID); !allowed {
			return nil, connect.NewError(connect.CodeResourceExhausted,
				fmt.Errorf("issuance rate limit reached for this organization, retry in %s", time.Until(resetAt).Round(time.Second)))
		}
	}

	info, err := s.engine.EnsureCertificate(ctx, domain)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("issuance failed: %w", err))
	}
	s.log.Info("certificate issued for %s (expires %s)", domain, info.NotAfter.AsTime())

	return connect.NewResponse(&v1.IssueCertificateResponse{Cert: info}), nil
}
