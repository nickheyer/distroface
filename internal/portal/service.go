package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/certs"
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

var _ distrofacev1connect.PortalServiceHandler = (*Service)(nil)

// Lowercase host, no port
var hostnameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

type Service struct {
	store    *stores.Store
	enforcer *rbac.Enforcer
	proxies  *Manager
	engine   *certs.Engine
	res      *settings.Resolver
	mainPort int
	log      *logger.Logger
}

func NewService(store *stores.Store, enforcer *rbac.Enforcer, proxies *Manager, engine *certs.Engine, res *settings.Resolver, mainPort int, log *logger.Logger) *Service {
	return &Service{store: store, enforcer: enforcer, proxies: proxies, engine: engine, res: res, mainPort: mainPort, log: log}
}

// Resolves the org, caller needs global org-manage or owner/admin membership
func (s *Service) requireOrgAdmin(ctx context.Context, orgID string) (*storage.Organization, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	if orgID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("org_id is required"))
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

// Fetches a portal, must belong to the org
func (s *Service) getOrgPortal(ctx context.Context, org *storage.Organization, id string) (*storage.RegistryPortal, error) {
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id is required"))
	}
	portal, err := s.store.GetRegistryPortal(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if portal == nil || portal.OrgID != org.ID {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return portal, nil
}

// Normalizes and validates hostname/port, rejects primary hostname and hosts
// claimed by another portal, the main port is stored as 0 (serve on app port)
func (s *Service) validatePlacement(ctx context.Context, hostname string, port int, excludePortalID string) (string, int, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" && port == 0 {
		return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("hostname or port is required"))
	}
	if port < 0 || port > 65535 {
		return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid port"))
	}
	if port != 0 && port == s.mainPort {
		if hostname == "" {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("a catch-all portal on the app's own port would shadow the primary UI"))
		}
		port = 0
	}

	if hostname != "" {
		if !hostnameRegex.MatchString(hostname) {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid hostname"))
		}
		primary := strings.ToLower(s.res.System(ctx).GetServer().GetPublicHostname())
		if hostname == primary || hostname == strings.SplitN(primary, ":", 2)[0] {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("hostname conflicts with the server's primary hostname"))
		}
	}

	// Hostnames are unique per port, catch-alls one per port
	portals, err := s.store.ListRegistryPortals(ctx)
	if err != nil {
		return "", 0, connect.NewError(connect.CodeInternal, err)
	}
	for _, p := range portals {
		if p.ID == excludePortalID || p.Port != port {
			continue
		}
		if p.Hostname == hostname {
			if hostname == "" {
				return "", 0, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("port %d already has a catch-all portal", port))
			}
			return "", 0, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("hostname already in use on this port"))
		}
	}

	if port > 0 {
		if err := s.proxies.ProbePort(port); err != nil {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("cannot bind port %d: %v", port, err))
		}
	}
	return hostname, port, nil
}

// Portal sources exclude the app only config pair and instance root
func portalCertSource(src v1.CertSource) (v1.CertSource, error) {
	switch src {
	case v1.CertSource_CERT_SOURCE_UNSPECIFIED, v1.CertSource_CERT_SOURCE_NONE:
		return v1.CertSource_CERT_SOURCE_NONE, nil
	case v1.CertSource_CERT_SOURCE_ACME, v1.CertSource_CERT_SOURCE_ORG_CA,
		v1.CertSource_CERT_SOURCE_ORG_CERT, v1.CertSource_CERT_SOURCE_MANUAL:
		return src, nil
	}
	return 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("cert source %v is app only", src))
}

// Registers an acme portal's hostname so issuance and approval can track it
func (s *Service) ensureCertDomain(ctx context.Context, org *storage.Organization, hostname string) {
	if hostname == "" || hostname == "localhost" || !strings.Contains(hostname, ".") || net.ParseIP(hostname) != nil {
		return
	}
	existing, err := s.store.GetCertificateDomainByName(ctx, hostname)
	if err != nil || existing != nil {
		return
	}
	record := &storage.CertificateDomain{
		Domain: hostname,
		Scope:  v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_ORG,
		OrgID:  &org.ID,
	}
	// Only registrations claimed while approval is on wait for an admin
	record.Approved = !s.engine.Policy().RequireApproval(ctx)
	if user := auth.UserFromContext(ctx); user != nil {
		record.CreatedBy = user.Username
		if allowed, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceSettings, rbac.ActionManage, "*"); allowed {
			record.Approved = true
		}
		if record.Approved {
			record.ApprovedBy = user.Username
		}
	}
	if err := s.store.CreateCertificateDomain(ctx, record); err != nil {
		s.log.Error("auto registering cert domain %s: %v", hostname, err)
		return
	}
	s.log.Info("cert domain auto registered for portal: %s (approved %v)", hostname, record.Approved)
}

// Drops the org's registration once no portal of the org serves the hostname
func (s *Service) cleanupCertDomain(ctx context.Context, org *storage.Organization, hostname string) {
	if hostname == "" {
		return
	}
	record, err := s.store.GetCertificateDomainByName(ctx, hostname)
	if err != nil || record == nil || record.OrgID == nil || *record.OrgID != org.ID {
		return
	}
	portals, err := s.store.ListRegistryPortalsByOrg(ctx, org.ID)
	if err != nil {
		return
	}
	for _, p := range portals {
		if strings.EqualFold(p.Hostname, hostname) {
			return
		}
	}
	if err := s.store.DeleteCertificateDomain(ctx, record.ID); err != nil {
		s.log.Error("cleaning cert domain %s: %v", hostname, err)
		return
	}
	s.log.Info("cert domain released with portal: %s", hostname)
}

// Validates rules compile, returns their JSON form
func (s *Service) encodeRules(rules []*v1.PortalRule) (string, error) {
	if len(rules) == 0 {
		return "[]", nil
	}
	if err := ValidateRules(rules, s.log); err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, err)
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, err)
	}
	return string(encoded), nil
}

func (s *Service) CreatePortal(ctx context.Context, req *connect.Request[v1.CreatePortalRequest]) (*connect.Response[v1.CreatePortalResponse], error) {
	msg := req.Msg
	org, err := s.requireOrgAdmin(ctx, msg.OrgId)
	if err != nil {
		return nil, err
	}
	if msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}

	hostname, port, err := s.validatePlacement(ctx, msg.Hostname, int(msg.Port), "")
	if err != nil {
		return nil, err
	}
	certSource, err := portalCertSource(msg.CertSource)
	if err != nil {
		return nil, err
	}
	if msg.Tls && certSource == v1.CertSource_CERT_SOURCE_NONE {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("requiring https needs a certificate source"))
	}
	if err := s.engine.Policy().AllowedClaim(ctx, hostname, org.ID); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	rulesJSON, err := s.encodeRules(msg.Rules)
	if err != nil {
		return nil, err
	}

	portal := &storage.RegistryPortal{
		OrgID:           org.ID,
		Name:            msg.Name,
		Hostname:        hostname,
		Port:            port,
		MapUnqualified:  msg.MapUnqualified,
		Rules:           rulesJSON,
		AllowPush:       msg.AllowPush,
		RequireAuth:     msg.RequireAuth,
		TLS:             msg.Tls,
		CertSource:      certSource,
		HidePrimaryLink: msg.HidePrimaryLink,
		Enabled:         true,
	}
	if err := s.store.CreateRegistryPortal(ctx, portal); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if portal.CertSource == v1.CertSource_CERT_SOURCE_ACME {
		s.ensureCertDomain(ctx, org, portal.Hostname)
	}
	s.reconcile(ctx)
	s.log.Info("portal created: %s port %d tls=%v -> org %s (%s)", portal.Hostname, portal.Port, portal.TLS, org.Name, portal.ID)

	return connect.NewResponse(&v1.CreatePortalResponse{Portal: s.portalWithStatus(ctx, portal)}), nil
}

// Proto portal annotated with its computed certificate state
func (s *Service) portalWithStatus(ctx context.Context, p *storage.RegistryPortal) *v1.RegistryPortal {
	proto := portalToProto(p)
	st := s.engine.PortalStatus(ctx, p)
	proto.CertState = st.State
	if len(st.Problems) > 0 {
		proto.CertDetail = st.Problems[0]
	}
	return proto
}

func (s *Service) GetPortal(ctx context.Context, req *connect.Request[v1.GetPortalRequest]) (*connect.Response[v1.GetPortalResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
	if err != nil {
		return nil, err
	}
	portal, err := s.getOrgPortal(ctx, org, req.Msg.Id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.GetPortalResponse{
		Portal: s.portalWithStatus(ctx, portal),
	}), nil
}

func (s *Service) ListPortals(ctx context.Context, req *connect.Request[v1.ListPortalsRequest]) (*connect.Response[v1.ListPortalsResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
	if err != nil {
		return nil, err
	}
	limit, offset := pages.Parse(req.Msg.Page)
	q := pages.ParseQuery(req.Msg.Page)
	if err := stores.PortalsQuery.Validate(q); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	portals, total, err := s.store.ListRegistryPortalsByOrgPaged(ctx, org.ID, q, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &v1.ListPortalsResponse{
		Page: pages.Info(offset, limit, total),
	}
	for _, p := range portals {
		resp.Portals = append(resp.Portals, s.portalWithStatus(ctx, p))
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) UpdatePortal(ctx context.Context, req *connect.Request[v1.UpdatePortalRequest]) (*connect.Response[v1.UpdatePortalResponse], error) {
	msg := req.Msg
	org, err := s.requireOrgAdmin(ctx, msg.OrgId)
	if err != nil {
		return nil, err
	}
	portal, err := s.getOrgPortal(ctx, org, msg.Id)
	if err != nil {
		return nil, err
	}
	oldHostname := portal.Hostname

	if msg.Name != nil {
		if *msg.Name == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name cannot be empty"))
		}
		portal.Name = *msg.Name
	}
	if msg.Hostname != nil || msg.Port != nil {
		hostname, port := portal.Hostname, portal.Port
		if msg.Hostname != nil {
			hostname = *msg.Hostname
		}
		if msg.Port != nil {
			port = int(*msg.Port)
		}
		hostname, port, err := s.validatePlacement(ctx, hostname, port, portal.ID)
		if err != nil {
			return nil, err
		}
		if hostname != portal.Hostname {
			if err := s.engine.Policy().AllowedClaim(ctx, hostname, org.ID); err != nil {
				return nil, connect.NewError(connect.CodeFailedPrecondition, err)
			}
		}
		portal.Hostname = hostname
		portal.Port = port
	}
	if msg.MapUnqualified != nil {
		portal.MapUnqualified = *msg.MapUnqualified
	}
	if msg.SetRules {
		rulesJSON, err := s.encodeRules(msg.Rules)
		if err != nil {
			return nil, err
		}
		portal.Rules = rulesJSON
	}
	if msg.AllowPush != nil {
		portal.AllowPush = *msg.AllowPush
	}
	if msg.RequireAuth != nil {
		portal.RequireAuth = *msg.RequireAuth
	}
	if msg.HidePrimaryLink != nil {
		portal.HidePrimaryLink = *msg.HidePrimaryLink
	}
	if msg.Tls != nil {
		portal.TLS = *msg.Tls
	}
	if msg.CertSource != v1.CertSource_CERT_SOURCE_UNSPECIFIED {
		src, err := portalCertSource(msg.CertSource)
		if err != nil {
			return nil, err
		}
		portal.CertSource = src
	}
	if msg.Enabled != nil {
		portal.Enabled = *msg.Enabled
	}
	if portal.CertSource == v1.CertSource_CERT_SOURCE_UNSPECIFIED {
		portal.CertSource = v1.CertSource_CERT_SOURCE_NONE
	}
	if portal.TLS && portal.CertSource == v1.CertSource_CERT_SOURCE_NONE {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("requiring https needs a certificate source"))
	}

	if err := s.store.UpdateRegistryPortal(ctx, portal); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if portal.CertSource == v1.CertSource_CERT_SOURCE_ACME {
		s.ensureCertDomain(ctx, org, portal.Hostname)
	}
	if !strings.EqualFold(oldHostname, portal.Hostname) {
		s.cleanupCertDomain(ctx, org, oldHostname)
	}
	s.reconcile(ctx)

	return connect.NewResponse(&v1.UpdatePortalResponse{Portal: s.portalWithStatus(ctx, portal)}), nil
}

func (s *Service) DeletePortal(ctx context.Context, req *connect.Request[v1.DeletePortalRequest]) (*connect.Response[v1.DeletePortalResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgId)
	if err != nil {
		return nil, err
	}
	portal, err := s.getOrgPortal(ctx, org, req.Msg.Id)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteRegistryPortal(ctx, portal.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := s.store.DeleteTLSCertificatesByPortal(ctx, portal.ID); err != nil {
		s.log.Error("cleaning portal tls material: %v", err)
	}
	if err := s.res.DeleteScope(ctx, v1.SettingsScopeType_SETTINGS_SCOPE_TYPE_PORTAL, portal.ID); err != nil {
		s.log.Error("cleaning portal settings: %v", err)
	}
	s.cleanupCertDomain(ctx, org, portal.Hostname)
	s.reconcile(ctx)
	s.log.Info("portal deleted: %s (%s)", portal.Hostname, portal.ID)

	return connect.NewResponse(&v1.DeletePortalResponse{}), nil
}

// Reports the portal serving the current request host, empty off portals
func (s *Service) ResolvePortal(ctx context.Context, _ *connect.Request[v1.ResolvePortalRequest]) (*connect.Response[v1.ResolvePortalResponse], error) {
	p := FromContext(ctx)
	if p == nil {
		return connect.NewResponse(&v1.ResolvePortalResponse{
			PrimaryScheme: s.primaryScheme(ctx),
		}), nil
	}
	return connect.NewResponse(&v1.ResolvePortalResponse{
		IsPortal:        true,
		OrgName:         p.OrgName,
		OrgDisplayName:  p.OrgDisplayName,
		PortalName:      p.Name,
		AllowPush:       p.AllowPush,
		RequireAuth:     p.RequireAuth,
		MapUnqualified:  p.MapUnqualified,
		PrimaryHost:     s.res.System(ctx).GetServer().GetPublicHostname(),
		PrimaryScheme:   s.primaryScheme(ctx),
		HidePrimaryLink: p.HidePrimaryLink,
	}), nil
}

// Https once the primary either refuses cleartext or has a working cert
func (s *Service) primaryScheme(ctx context.Context) string {
	if s.res.System(ctx).GetTls().GetMode() == v1.TLSMode_TLS_MODE_HTTPS_ONLY {
		return "https"
	}
	if st := s.engine.AppStatus(ctx); st.State == v1.CertState_CERT_STATE_READY {
		return "https"
	}
	return "http"
}

// Probes/applies portal changes to the running proxies
func (s *Service) reconcile(ctx context.Context) {
	if err := s.proxies.Reconcile(ctx); err != nil {
		s.log.Error("portal proxy reconcile: %v", err)
	}
}

func portalToProto(p *storage.RegistryPortal) *v1.RegistryPortal {
	proto := &v1.RegistryPortal{
		Id:              p.ID,
		OrgId:           p.OrgID,
		Name:            p.Name,
		Hostname:        p.Hostname,
		Port:            int32(p.Port),
		MapUnqualified:  p.MapUnqualified,
		AllowPush:       p.AllowPush,
		RequireAuth:     p.RequireAuth,
		Tls:             p.TLS,
		CertSource:      p.CertSource,
		HidePrimaryLink: p.HidePrimaryLink,
		Enabled:         p.Enabled,
		CreatedAt:       timestamppb.New(p.CreatedAt),
		UpdatedAt:       timestamppb.New(p.UpdatedAt),
	}
	if rules, err := ParseRules(p.Rules); err == nil {
		proto.Rules = rules
	}
	return proto
}
