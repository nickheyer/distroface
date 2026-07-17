package certs

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/pagination"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ distrofacev1connect.CertificateServiceHandler = (*Service)(nil)

// Lowercase host, no port
var domainRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// Engine may be nil when tls is off, domain management still works
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

func (s *Service) certInfoProto(ctx context.Context, domain string) *v1.CertificateInfo {
	if s.engine == nil {
		return nil
	}
	info, err := s.engine.CertificateInfo(ctx, domain)
	if err != nil || info == nil || !info.Issued {
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
		Cert:       s.certInfoProto(ctx, d.Domain),
		Approved:   d.IssuanceAllowed(),
		ApprovedBy: d.ApprovedBy,
	}
}

func (s *Service) GetTLSStatus(ctx context.Context, _ *connect.Request[v1.GetTLSStatusRequest]) (*connect.Response[v1.GetTLSStatusResponse], error) {
	if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}
	resp := &v1.GetTLSStatusResponse{
		TlsEnabled:      s.config.TLS.Enabled,
		AcmeEnabled:     s.config.TLS.ACME.Enabled,
		AcmeEmail:       s.config.TLS.ACME.Email,
		AcmeDirectory:   s.config.TLS.ACME.DirectoryURL,
		ManualCert:      s.engine.ManualCertLoaded(),
		ConfigDomains:   s.config.TLS.ACME.Domains,
		PrimaryHostname: s.config.Server.Hostname,
	}
	resp.PrimaryCert = s.certInfoProto(ctx, bareHost(s.config.Server.Hostname))
	return connect.NewResponse(resp), nil
}

func (s *Service) ListCertificateDomains(ctx context.Context, req *connect.Request[v1.ListCertificateDomainsRequest]) (*connect.Response[v1.ListCertificateDomainsResponse], error) {
	resp := &v1.ListCertificateDomainsResponse{}
	limit, offset := pagination.Parse(req.Msg.Page)
	q := pagination.ParseQuery(req.Msg.Page)
	if err := stores.CertDomainsQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if req.Msg.OrgId != "" {
		org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
		domains, total, err := s.store.ListCertificateDomainsByOrg(ctx, org.ID, q, limit, offset)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		for _, d := range domains {
			resp.Domains = append(resp.Domains, s.domainToProto(ctx, d, org.Name))
		}
		resp.Page = pagination.Info(offset, limit, total)
		return connect.NewResponse(resp), nil
	}

	if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
	}
	domains, total, err := s.store.ListCertificateDomains(ctx, q, limit, offset)
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
	resp.Page = pagination.Info(offset, limit, total)
	return connect.NewResponse(resp), nil
}

func (s *Service) ListCertificateHosts(ctx context.Context, req *connect.Request[v1.ListCertificateHostsRequest]) (*connect.Response[v1.ListCertificateHostsResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
	if err != nil {
		return nil, err
	}
	limit, offset := pagination.Parse(req.Msg.Page)
	q := pagination.ParseQuery(req.Msg.Page)
	if err := stores.CertHostsQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rows, total, err := s.store.ListCertificateHosts(ctx, org.ID, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	hostnames := make([]string, len(rows))
	for i, r := range rows {
		hostnames[i] = r.Hostname
	}
	domains, err := s.store.GetCertificateDomainsByNames(ctx, org.ID, hostnames)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	regByDomain := make(map[string]*storage.CertificateDomain, len(domains))
	for _, d := range domains {
		regByDomain[d.Domain] = d
	}

	hosts := make([]*v1.CertificateHost, len(rows))
	for i, r := range rows {
		host := &v1.CertificateHost{
			Hostname:   r.Hostname,
			PortalId:   r.PortalID,
			PortalName: r.PortalName,
			Eligible:   issuableHost(r.Hostname),
		}
		if reg := regByDomain[r.Hostname]; reg != nil {
			host.Registration = s.domainToProto(ctx, reg, org.Name)
		}
		hosts[i] = host
	}

	return connect.NewResponse(&v1.ListCertificateHostsResponse{
		Hosts: hosts,
		Page:  pagination.Info(offset, limit, total),
	}), nil
}

func (s *Service) AddCertificateDomain(ctx context.Context, req *connect.Request[v1.AddCertificateDomainRequest]) (*connect.Response[v1.AddCertificateDomainResponse], error) {
	domain := bareHost(req.Msg.Domain)
	if domain == "" || !domainRegex.MatchString(domain) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid domain"))
	}
	if !issuableHost(domain) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("domain %q is not a publicly issuable hostname", domain))
	}

	record := &storage.CertificateDomain{Domain: domain, Scope: storage.CertDomainScopeSystem}
	orgName := ""

	if req.Msg.OrgId != "" {
		org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
		owns, err := s.orgOwnsHostname(ctx, org, domain)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if !owns {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("domain must match one of the organization's portal hostnames"))
		}
		record.Scope = storage.CertDomainScopeOrg
		record.OrgID = &org.ID
		orgName = org.Name
	} else if err := s.requireSystemAdmin(ctx); err != nil {
		return nil, err
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
	if record.Scope == storage.CertDomainScopeSystem || s.isSystemAdmin(ctx) {
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
	if s.engine == nil || !s.engine.ACMEEnabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("acme is not enabled"))
	}
	domain := bareHost(req.Msg.Domain)

	if req.Msg.OrgId != "" {
		org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
		if err != nil {
			return nil, err
		}
		record, err := s.store.GetCertificateDomainByName(ctx, domain)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if record == nil || record.OrgID == nil || *record.OrgID != org.ID {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("domain not registered for this organization"))
		}
		if !record.IssuanceAllowed() {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("domain is awaiting approval by a system administrator"))
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
