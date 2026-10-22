package auth

import (
	"context"
	"strings"

	"github.com/nickheyer/distroface/internal/rbac"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
)

type contextKey string

const userContextKey contextKey = "authenticated_user"

// AuthenticatedUser represents a validated user in context.
type AuthenticatedUser struct {
	ID                 string
	Username           string
	Email              string
	Roles              []string
	Provider           string // "local", "oidc", "anonymous", "deploykey"
	MustChangePassword bool   // rpc access pending pw rotation
	// Narrows a deploy token or deploy key to one object, nil for full identity
	Scope *CredentialScope
	// Row id of the api token behind this identity, empty otherwise
	TokenID string
}

// Narrowing carried by scoped api tokens and deploy keys
type CredentialScope struct {
	Type     v1.CredentialScopeType
	ID       string // namespace/name for git repos, org name for orgs
	ReadOnly bool
}

// Registry token subjects of job tokens carry the job id
const jobSubjectPrefix = "actions-job/"

// JobSubject names a job token in a registry token subject
func JobSubject(jobID, username string) string {
	return jobSubjectPrefix + jobID + "/" + username
}

// ParseJobSubject splits a job subject into job id and username
func ParseJobSubject(subject string) (jobID, username string, ok bool) {
	if !strings.HasPrefix(subject, jobSubjectPrefix) {
		return "", subject, false
	}
	jobID, username, found := strings.Cut(strings.TrimPrefix(subject, jobSubjectPrefix), "/")
	if !found || jobID == "" || username == "" {
		return "", subject, false
	}
	return jobID, username, true
}

// Principal key an upload session is opened under
func UploadOwner(u *AuthenticatedUser) string {
	if u == nil || u.ID == "" {
		return ""
	}
	return "user:" + u.ID
}

// Keys a caller may present for a session, the api token row included
func UploadOwners(u *AuthenticatedUser) []string {
	if u == nil || u.ID == "" {
		return nil
	}
	owners := []string{UploadOwner(u)}
	if u.TokenID != "" {
		owners = append(owners, TokenUploadOwner(u.TokenID))
	}
	return owners
}

// Principal key of a session opened for one api token row
func TokenUploadOwner(tokenID string) string {
	if tokenID == "" {
		return ""
	}
	return "token:" + tokenID
}

// Signed out visitors carry the anonymous role only
func (u *AuthenticatedUser) IsAnonymous() bool {
	return u != nil && u.Provider == "anonymous"
}

// Reports whether the credential is a deploy key principal
func (u *AuthenticatedUser) IsDeployKey() bool {
	return u != nil && u.Provider == "deploykey"
}

// Reports whether the identity is narrowed by a credential scope
func (u *AuthenticatedUser) IsScoped() bool {
	return u != nil && u.Scope != nil && u.Scope.Type != v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_UNSPECIFIED
}

// Source repo or namespace a scoped credential's listings are confined to
func (u *AuthenticatedUser) ListingScope() (repo, namespace string) {
	if !u.IsScoped() {
		return "", ""
	}
	switch u.Scope.Type {
	case v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO:
		return u.Scope.ID, ""
	case v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_ORG:
		return "", u.Scope.ID
	}
	return "", ""
}

// Actions a scope admits on an object, nil scope admits everything.
// Git repo scopes cover the repo and its linked packages, whose ids the
// caller passes as alternatives. Org scopes cover objects inside the org.
func ScopeAllows(user *AuthenticatedUser, action string, objectIDs ...string) bool {
	if !user.IsScoped() {
		return true
	}
	writes := action == rbac.ActionPush || action == rbac.ActionCreate || action == rbac.ActionUpdate || action == rbac.ActionDelete || action == rbac.ActionManage
	if user.Scope.ReadOnly && writes {
		return false
	}
	switch user.Scope.Type {
	case v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_GIT_REPO:
		if action == rbac.ActionManage || action == rbac.ActionDelete || action == rbac.ActionCreate {
			return false
		}
		for _, id := range objectIDs {
			if id == user.Scope.ID {
				return true
			}
		}
		return false
	case v1.CredentialScopeType_CREDENTIAL_SCOPE_TYPE_ORG:
		for _, id := range objectIDs {
			if id == user.Scope.ID || strings.HasPrefix(id, user.Scope.ID+"/") {
				return true
			}
		}
		return false
	}
	return false
}

// Identity used when no auth provider is enabled
func NoAuthAdmin() *AuthenticatedUser {
	return &AuthenticatedUser{ID: "admin", Username: "admin", Roles: []string{"admin"}, Provider: "none"}
}

// WithUser attaches an authenticated user to the context.
func WithUser(ctx context.Context, user *AuthenticatedUser) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext retrieves the authenticated user from the context.
func UserFromContext(ctx context.Context) *AuthenticatedUser {
	user, _ := ctx.Value(userContextKey).(*AuthenticatedUser)
	return user
}

// ExtractToken extracts a bearer token from Authorization header or session cookie.
func ExtractToken(headers interface{ Get(string) string }) string {
	authHeader := headers.Get("Authorization")
	if authHeader != "" {
		prefix, token, ok := strings.Cut(authHeader, " ")
		if ok && strings.EqualFold(prefix, "Bearer") {
			return strings.TrimSpace(token)
		}
	}

	cookie := headers.Get("Cookie")
	if cookie != "" {
		for _, part := range strings.Split(cookie, ";") {
			part = strings.TrimSpace(part)
			if name, value, ok := strings.Cut(part, "="); ok && name == "distroface_session" {
				return value
			}
		}
	}

	return ""
}
