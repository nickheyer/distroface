package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/internal/auth"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
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
	config   *config.Config
	log      *logger.Logger
}

func NewService(store *stores.Store, enforcer *rbac.Enforcer, proxies *Manager, cfg *config.Config, log *logger.Logger) *Service {
	return &Service{store: store, enforcer: enforcer, proxies: proxies, config: cfg, log: log}
}

// Resolves the org, caller needs global org-manage or owner/admin membership
func (s *Service) requireOrgAdmin(ctx context.Context, orgName string) (*storage.Organization, error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	if orgName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("org_name is required"))
	}

	org, err := s.store.GetOrganization(ctx, orgName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	canManage, _ := s.enforcer.Enforce(user.Roles, rbac.ResourceOrganizations, rbac.ActionManage, org.Name)
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

// The app's own port, 0 when unparsable
func (s *Service) mainPort() int {
	port, _ := strconv.Atoi(s.config.Server.Port)
	return port
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
	if port != 0 && port == s.mainPort() {
		if hostname == "" {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("a catch-all portal on the app's own port would shadow the primary UI"))
		}
		port = 0
	}

	if hostname != "" {
		if !hostnameRegex.MatchString(hostname) {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid hostname"))
		}
		primary := strings.ToLower(s.config.Server.Hostname)
		if hostname == primary || hostname == strings.SplitN(primary, ":", 2)[0] {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("hostname conflicts with the server's primary hostname"))
		}
		existing, err := s.store.GetRegistryPortalByHostname(ctx, hostname)
		if err != nil {
			return "", 0, connect.NewError(connect.CodeInternal, err)
		}
		if existing != nil && existing.ID != excludePortalID {
			return "", 0, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("hostname already in use"))
		}
	} else {
		// Port catch-all, only one per port
		portals, err := s.store.ListRegistryPortals(ctx)
		if err != nil {
			return "", 0, connect.NewError(connect.CodeInternal, err)
		}
		for _, p := range portals {
			if p.ID != excludePortalID && p.Port == port && p.Hostname == "" {
				return "", 0, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("port %d already has a catch-all portal", port))
			}
		}
	}

	if port > 0 {
		if err := s.proxies.ProbePort(port); err != nil {
			return "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("cannot bind port %d: %v", port, err))
		}
	}
	return hostname, port, nil
}

// Validates rules compile, returns their JSON form
func (s *Service) encodeRules(rules []*v1.PortalRule) (string, error) {
	if len(rules) == 0 {
		return "[]", nil
	}
	mappingRules := make([]MappingRule, 0, len(rules))
	for _, r := range rules {
		mappingRules = append(mappingRules, MappingRule{Pattern: r.Pattern, Replace: r.Replace})
	}
	if err := ValidateRules(mappingRules, s.log); err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, err)
	}
	encoded, err := json.Marshal(mappingRules)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, err)
	}
	return string(encoded), nil
}

func (s *Service) CreatePortal(ctx context.Context, req *connect.Request[v1.CreatePortalRequest]) (*connect.Response[v1.CreatePortalResponse], error) {
	msg := req.Msg
	org, err := s.requireOrgAdmin(ctx, msg.OrgName)
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
	rulesJSON, err := s.encodeRules(msg.Rules)
	if err != nil {
		return nil, err
	}

	portal := &storage.RegistryPortal{
		OrgID:          org.ID,
		Name:           msg.Name,
		Hostname:       hostname,
		Port:           port,
		MapUnqualified: msg.MapUnqualified,
		Rules:          rulesJSON,
		AllowPush:      msg.AllowPush,
		RequireAuth:    msg.RequireAuth,
		Enabled:        true,
	}
	if err := s.store.CreateRegistryPortal(ctx, portal); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.reconcile(ctx)
	s.log.Info("portal created: %s port %d -> org %s (%s)", portal.Hostname, portal.Port, org.Name, portal.ID)

	return connect.NewResponse(&v1.CreatePortalResponse{Portal: portalToProto(portal, org.Name)}), nil
}

func (s *Service) ListPortals(ctx context.Context, req *connect.Request[v1.ListPortalsRequest]) (*connect.Response[v1.ListPortalsResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgName)
	if err != nil {
		return nil, err
	}
	portals, err := s.store.ListRegistryPortalsByOrg(ctx, org.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &v1.ListPortalsResponse{MainPort: int32(s.mainPort())}
	for _, p := range portals {
		resp.Portals = append(resp.Portals, portalToProto(p, org.Name))
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) UpdatePortal(ctx context.Context, req *connect.Request[v1.UpdatePortalRequest]) (*connect.Response[v1.UpdatePortalResponse], error) {
	msg := req.Msg
	org, err := s.requireOrgAdmin(ctx, msg.OrgName)
	if err != nil {
		return nil, err
	}
	portal, err := s.getOrgPortal(ctx, org, msg.Id)
	if err != nil {
		return nil, err
	}

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
	if msg.Enabled != nil {
		portal.Enabled = *msg.Enabled
	}

	if err := s.store.UpdateRegistryPortal(ctx, portal); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.reconcile(ctx)

	return connect.NewResponse(&v1.UpdatePortalResponse{Portal: portalToProto(portal, org.Name)}), nil
}

func (s *Service) DeletePortal(ctx context.Context, req *connect.Request[v1.DeletePortalRequest]) (*connect.Response[v1.DeletePortalResponse], error) {
	org, err := s.requireOrgAdmin(ctx, req.Msg.OrgName)
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
	s.reconcile(ctx)
	s.log.Info("portal deleted: %s (%s)", portal.Hostname, portal.ID)

	return connect.NewResponse(&v1.DeletePortalResponse{}), nil
}

// Reports the portal serving the current request host, empty off portals
func (s *Service) ResolvePortal(ctx context.Context, _ *connect.Request[v1.ResolvePortalRequest]) (*connect.Response[v1.ResolvePortalResponse], error) {
	p := FromContext(ctx)
	if p == nil {
		return connect.NewResponse(&v1.ResolvePortalResponse{}), nil
	}
	return connect.NewResponse(&v1.ResolvePortalResponse{
		IsPortal:       true,
		OrgName:        p.OrgName,
		OrgDisplayName: p.OrgDisplayName,
		PortalName:     p.Name,
		AllowPush:      p.AllowPush,
		RequireAuth:    p.RequireAuth,
		MapUnqualified: p.MapUnqualified,
		PrimaryHost:    s.config.Server.Hostname,
	}), nil
}

// Probes/applies portal changes to the running proxies
func (s *Service) reconcile(ctx context.Context) {
	if err := s.proxies.Reconcile(ctx); err != nil {
		s.log.Error("portal proxy reconcile: %v", err)
	}
}

func portalToProto(p *storage.RegistryPortal, orgName string) *v1.RegistryPortal {
	proto := &v1.RegistryPortal{
		Id:             p.ID,
		OrgName:        orgName,
		Name:           p.Name,
		Hostname:       p.Hostname,
		Port:           int32(p.Port),
		MapUnqualified: p.MapUnqualified,
		AllowPush:      p.AllowPush,
		RequireAuth:    p.RequireAuth,
		Enabled:        p.Enabled,
		CreatedAt:      timestamppb.New(p.CreatedAt),
		UpdatedAt:      timestamppb.New(p.UpdatedAt),
	}
	if rules, err := ParseRules(p.Rules); err == nil {
		for _, r := range rules {
			proto.Rules = append(proto.Rules, &v1.PortalRule{Pattern: r.Pattern, Replace: r.Replace})
		}
	}
	return proto
}
