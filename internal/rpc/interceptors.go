package rpc

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
)

// Credential rpcs that get brute force lockout
var throttledProcedures = map[string]bool{
	distrofacev1connect.AuthServiceLoginProcedure:          true,
	distrofacev1connect.AuthServiceRegisterProcedure:       true,
	distrofacev1connect.UserServiceChangePasswordProcedure: true,
}

// Credential lifecycle rpcs audited despite lacking a permission entry
var auditedAuthProcedures = map[string]bool{
	distrofacev1connect.AuthServiceLoginProcedure:          true,
	distrofacev1connect.AuthServiceRegisterProcedure:       true,
	distrofacev1connect.AuthServiceLogoutProcedure:         true,
	distrofacev1connect.UserServiceChangePasswordProcedure: true,
}

// Rpcs still reachable while a password rotation is pending
var mustChangeExemptProcedures = map[string]bool{
	distrofacev1connect.AuthServiceGetCurrentUserProcedure: true,
	distrofacev1connect.AuthServiceLogoutProcedure:         true,
	distrofacev1connect.AuthServiceRefreshSessionProcedure: true,
	distrofacev1connect.UserServiceChangePasswordProcedure: true,
}

// Peer address of the in flight call, empty off the wire
func peerAddr(ctx context.Context) string {
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		return info.PeerAddr
	}
	return ""
}

// Client ip honoring trusted proxy headers
func clientIP(ctx context.Context) string {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return ""
	}
	return admin.ClientIP(info.PeerAddr, info.RequestHeader())
}

// Bearer or basic token from the request headers
func bearerToken(ctx context.Context) string {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return ""
	}
	return auth.ExtractToken(info.RequestHeader())
}

func unauthenticated() error {
	return connect.NewError(connect.CodeUnauthenticated, auth.ErrInvalidToken.Error()).WithCause(auth.ErrInvalidToken)
}

func scopeDenied() error {
	return connect.Errorf(connect.CodePermissionDenied, "credential scope does not cover this procedure")
}

// Failed auth counts toward lockout success clears it
func (s *Server) rateLimitInterceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if s.AuthLimiter == nil || !throttledProcedures[spec.Procedure] {
				return next(ctx, spec, stream)
			}

			ip := clientIP(ctx)
			if s.AuthLimiter.Blocked(ip) {
				return connect.Errorf(connect.CodeResourceExhausted, "too many failed attempts, try again later")
			}

			err := next(ctx, spec, stream)
			if err != nil {
				switch connect.CodeOf(err) {
				case connect.CodeUnauthenticated, connect.CodePermissionDenied:
					s.AuthLimiter.Record(ip)
				}
			} else {
				s.AuthLimiter.Reset(ip)
			}
			return err
		}
	}
}

// Resolves the caller then gates the procedure, unary and streaming alike
func (s *Server) authInterceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			procedure := spec.Procedure
			isPublic := rbac.PublicProcedures[procedure]

			// If no auth providers are enabled, bypass auth entirely
			if !s.AuthManager.IsAnyAuthEnabled() {
				return next(auth.WithUser(ctx, auth.NoAuthAdmin()), spec, stream)
			}

			// Resolve user from token or anonymous access - always, for every request
			token := bearerToken(ctx)
			var user *auth.AuthenticatedUser

			if token != "" {
				var err error
				user, err = s.AuthManager.ValidateToken(ctx, token)
				if err != nil {
					if !isPublic {
						s.recordAuthDenial(ctx, procedure, "invalid token")
						return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
					}
					// Public route with bad token - proceed without user
				}
			} else if s.anonymousAllowed(ctx) {
				user = s.AuthManager.AnonymousUser()
			} else if !isPublic {
				return unauthenticated()
			}

			if user != nil {
				ctx = auth.WithUser(ctx, user)
			}

			if err := portalAllowsProcedure(ctx, procedure); err != nil {
				return err
			}

			// Public procedures - no further checks
			if isPublic {
				return next(ctx, spec, stream)
			}

			// Object scoped grants wait for the decoded request
			if perm, ok := rbac.ProcedurePermissions[procedure]; ok && perm.ObjectIDField != "" {
				return next(ctx, spec, &gatedStream{ServerStream: stream, gate: func(msg any) error {
					return s.authorize(ctx, msg, user, procedure)
				}})
			}

			if err := s.authorize(ctx, nil, user, procedure); err != nil {
				if connect.CodeOf(err) == connect.CodePermissionDenied {
					s.recordAuthDenial(ctx, procedure, err.Error())
				}
				return err
			}
			return next(ctx, spec, stream)
		}
	}
}

// Runs the grant check once the first request message is decoded
type gatedStream struct {
	connect.ServerStream
	gate    func(msg any) error
	checked bool
}

func (g *gatedStream) Receive(msg any) error {
	if err := g.ServerStream.Receive(msg); err != nil {
		return err
	}
	if g.checked {
		return nil
	}
	g.checked = true
	return g.gate(msg)
}

// Identity tier and rbac grant checks for one resolved caller
func (s *Server) authorize(ctx context.Context, req any, user *auth.AuthenticatedUser, procedure string) error {
	if user == nil {
		return unauthenticated()
	}

	// Forced rotation blocks everything but session and password rpcs
	if user.MustChangePassword && !mustChangeExemptProcedures[procedure] {
		return connect.Errorf(connect.CodePermissionDenied, "password change required before continuing")
	}

	if user.IsScoped() {
		if err := s.scopeAllows(ctx, req, user, procedure); err != nil {
			return err
		}
	}

	if rbac.IdentityProcedures[procedure] {
		return nil
	}

	// Account only rpcs refuse the anonymous identity
	if rbac.AuthenticatedOnlyProcedures[procedure] {
		if user.IsAnonymous() {
			return unauthenticated()
		}
		return nil
	}

	perm, ok := rbac.ProcedurePermissions[procedure]
	if !ok {
		// Every procedure names its policy, an unmapped one is denied rather than waved through
		return connect.Errorf(connect.CodePermissionDenied, "procedure %s has no access policy", procedure)
	}
	if s.Enforcer == nil {
		return nil
	}
	objectID := "*"
	if perm.ObjectIDField != "" {
		objectID = rbac.ExtractObjectID(req, perm.ObjectIDField)
	}
	var allowed bool
	if perm.AnyGrant {
		allowed = s.Enforcer.HasPermission(user.Roles, perm.Resource, perm.Action)
	} else {
		var err error
		if allowed, err = s.Enforcer.Enforce(user.Roles, perm.Resource, perm.Action, objectID); err != nil {
			s.Log.Error("RBAC enforcement error: %v", err)
			return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
	}
	if !allowed {
		return connect.Errorf(connect.CodePermissionDenied, "insufficient permissions for %s/%s on %s", perm.Resource, perm.Action, objectID)
	}
	return nil
}

// Scoped credentials reach their object and their own identity, nothing else
func (s *Server) scopeAllows(ctx context.Context, req any, user *auth.AuthenticatedUser, procedure string) error {
	if procedure == distrofacev1connect.AuthServiceGetCurrentUserProcedure {
		return nil
	}
	perm, ok := rbac.ProcedurePermissions[procedure]
	if !ok {
		return scopeDenied()
	}
	switch perm.Resource {
	case rbac.ResourceGit, rbac.ResourceRepositories, rbac.ResourceArtifacts:
	default:
		return scopeDenied()
	}
	if perm.ObjectIDField == "" {
		// Lists filter visibility in service, creates never fit a scope
		if perm.Action == rbac.ActionRead || perm.Action == rbac.ActionPull {
			return nil
		}
		return scopeDenied()
	}
	objectID := rbac.ExtractObjectID(req, perm.ObjectIDField)
	if objectID == "*" {
		return scopeDenied()
	}
	namespace, _, _ := strings.Cut(objectID, "/")
	ids := []string{objectID, namespace}
	if src := s.linkedSource(ctx, perm.Resource, objectID); src != "" {
		ids = append(ids, src)
	}
	if !auth.ScopeAllows(user, perm.Action, ids...) {
		return scopeDenied()
	}
	return nil
}

// Full name of the git repo a package links to, empty when standalone
func (s *Server) linkedSource(ctx context.Context, resource, objectID string) string {
	namespace, name, ok := strings.Cut(objectID, "/")
	if !ok {
		return ""
	}
	store := s.AuthManager.GetStore()
	var gitID *string
	switch resource {
	case rbac.ResourceRepositories:
		if repo, _ := store.GetRepository(ctx, namespace, name); repo != nil {
			gitID = repo.GitRepoID
		}
	case rbac.ResourceArtifacts:
		if repo, _ := store.GetArtifactRepository(ctx, namespace, name); repo != nil {
			gitID = repo.GitRepoID
		}
	}
	if gitID == nil {
		return ""
	}
	if g, _ := store.GetGitRepositoryByID(ctx, *gitID); g != nil {
		return g.Namespace + "/" + g.Name
	}
	return ""
}

// Reflection needs a real token, anonymous never browses the schema
func (s *Server) reflectionAuthInterceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if !s.AuthManager.IsAnyAuthEnabled() {
				return next(ctx, spec, stream)
			}
			token := bearerToken(ctx)
			if token == "" {
				return unauthenticated()
			}
			if _, err := s.AuthManager.ValidateToken(ctx, token); err != nil {
				return unauthenticated()
			}
			return next(ctx, spec, stream)
		}
	}
}

// Denials raised ahead of the audit interceptor still land in the trail
func (s *Server) recordAuthDenial(ctx context.Context, procedure, detail string) {
	resource, ok := auditableResource(procedure)
	if !ok {
		return
	}
	ev := audit.Event{
		Action:   procedureAction(procedure),
		Resource: resource,
		Outcome:  audit.OutcomeDenied,
		Detail:   detail,
		SourceIP: clientIP(ctx),
	}
	if user := auth.UserFromContext(ctx); user != nil {
		ev.Actor, ev.ActorID = user.Username, user.ID
	}
	s.AuditRecorder.Record(ctx, ev)
}

// Notes the verified client certificate subject on the audit row
func mtlsDetail(ctx context.Context) string {
	if id := certs.ClientIdentityFrom(ctx); id != nil {
		return "mtls client " + id.CommonName
	}
	return ""
}

// Keeps the first request message for audit details, even when gated
type capturedStream struct {
	connect.ServerStream
	first any
}

func (c *capturedStream) Receive(msg any) error {
	if c.first == nil {
		c.first = msg
	}
	return c.ServerStream.Receive(msg)
}

func (s *Server) auditInterceptor(recorder *audit.Recorder) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			procedure := spec.Procedure
			resource, ok := auditableResource(procedure)
			if !ok {
				return next(ctx, spec, stream)
			}

			captured := &capturedStream{ServerStream: stream}
			err := next(ctx, spec, captured)

			ev := audit.Event{
				Action:   procedureAction(procedure),
				Resource: resource,
				Outcome:  audit.OutcomeSuccess,
				SourceIP: clientIP(ctx),
				Detail:   mtlsDetail(ctx),
			}
			if user := auth.UserFromContext(ctx); user != nil {
				ev.Actor, ev.ActorID = user.Username, user.ID
			}
			if err != nil {
				switch connect.CodeOf(err) {
				case connect.CodeUnauthenticated, connect.CodePermissionDenied:
					ev.Outcome = audit.OutcomeDenied
				default:
					ev.Outcome = audit.OutcomeError
				}
			}
			if perm, ok := rbac.ProcedurePermissions[procedure]; ok && perm.ObjectIDField != "" {
				if obj := rbac.ExtractObjectID(captured.first, perm.ObjectIDField); obj != "*" {
					ev.Detail = obj
				}
			}
			// Credential rpcs carry the subject in the request
			if auditedAuthProcedures[procedure] && ev.Detail == "" {
				for _, field := range []string{"username", "identifier"} {
					if subject := rbac.ExtractObjectID(captured.first, field); subject != "*" {
						ev.Detail = subject
						break
					}
				}
			}
			recorder.Record(ctx, ev)
			return err
		}
	}
}

// One line per call, streams log open and close
func (s *Server) loggingInterceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			peer := peerAddr(ctx)
			if spec.StreamType == connect.StreamTypeUnary {
				s.Log.Info("RPC %s %s", peer, spec.Procedure)
				return next(ctx, spec, stream)
			}
			s.Log.Info("RPC Stream open %s %s", peer, spec.Procedure)
			err := next(ctx, spec, stream)
			s.Log.Info("RPC Stream closed %s %s", peer, spec.Procedure)
			return err
		}
	}
}

// Mutations and credential events land in the audit trail, reads skip
func auditableResource(procedure string) (string, bool) {
	if auditedAuthProcedures[procedure] {
		return "auth", true
	}
	perm, ok := rbac.ProcedurePermissions[procedure]
	if !ok || perm.Action == rbac.ActionRead || perm.Action == rbac.ActionPull {
		return "", false
	}
	return perm.Resource, true
}

// Short human form of a connect procedure path
func procedureAction(procedure string) string {
	return strings.TrimPrefix(procedure, "/distroface.v1.")
}

// Portal tier on portal hosts, instance toggle on the primary
func (s *Server) anonymousAllowed(ctx context.Context) bool {
	return portal.AnonymousAllowed(ctx, s.AuthManager.IsAnonymousAccessEnabled())
}

// Read-only portals refuse content mutations on the RPC surface too
func portalAllowsProcedure(ctx context.Context, procedure string) error {
	p := portal.FromContext(ctx)
	if p == nil || p.AllowPush || !portalWriteProcedure(procedure) {
		return nil
	}
	return connect.Errorf(connect.CodePermissionDenied, "portal is read-only")
}

// Explicit lists first, then the resource action of the rbac mapping
func portalWriteProcedure(procedure string) bool {
	if rbac.PortalWriteProcedures[procedure] {
		return true
	}
	if rbac.PortalReadProcedures[procedure] {
		return false
	}
	perm, ok := rbac.ProcedurePermissions[procedure]
	if !ok || (perm.Resource != rbac.ResourceArtifacts && perm.Resource != rbac.ResourceRepositories && perm.Resource != rbac.ResourceGit) {
		return false
	}
	return perm.Action != rbac.ActionRead && perm.Action != rbac.ActionPull
}
