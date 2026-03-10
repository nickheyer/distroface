package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/pkg/logger"
	"golang.org/x/crypto/bcrypt"
)

// TokenHandler implements the Docker Token Authentication Specification.
type TokenHandler struct {
	tokenService *TokenService
	store        *storage.Store
	authManager  *Manager
	enforcer     *rbac.Enforcer
	log          *logger.Logger
}

type tokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
}

// NewTokenHandler creates a new Docker token auth endpoint handler.
func NewTokenHandler(ts *TokenService, store *storage.Store, manager *Manager, enforcer *rbac.Enforcer, log *logger.Logger) *TokenHandler {
	return &TokenHandler{
		tokenService: ts,
		store:        store,
		authManager:  manager,
		enforcer:     enforcer,
		log:          log,
	}
}

func (h *TokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	service, scopeStr, account := r.FormValue("service"), r.FormValue("scope"), r.FormValue("account")

	username, password, hasCreds := r.BasicAuth()
	if !hasCreds {
		username, password = r.FormValue("username"), r.FormValue("password")
		hasCreds = username != ""
	}

	var authUser *AuthenticatedUser
	if hasCreds && username != "" {
		// Check if password is an API token (df_ prefix)
		if strings.HasPrefix(password, "df_") {
			user, err := h.authManager.ValidateAPIToken(r.Context(), password)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+service+`"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			authUser = user
		} else {
			// Standard password auth
			u, err := h.store.GetUserByIdentifier(r.Context(), username)
			if err != nil {
				h.log.Error("token auth: failed to look up user %s: %v", username, err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if u == nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+service+`"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// Resolve roles for the user
			roleNames, err := h.store.GetUserRoleNames(r.Context(), u.ID)
			if err != nil {
				roleNames = []string{}
			}
			authUser = &AuthenticatedUser{
				ID:       u.ID,
				Username: u.Username,
				Roles:    roleNames,
				Provider: u.AuthProvider,
			}
			if u.Email != nil {
				authUser.Email = *u.Email
			}
		}
		if account == "" && authUser != nil {
			account = authUser.Username
		}
	}

	var access []*ResourceActions
	if scopeStr != "" {
		access = h.resolveAccess(r, authUser, scopeStr)
	}

	tokenStr, err := h.tokenService.SignToken(account, access)
	if err != nil {
		h.log.Error("token auth: failed to sign token: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := tokenResponse{
		Token:       tokenStr,
		AccessToken: tokenStr,
		ExpiresIn:   int(h.tokenService.expiry / time.Second),
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		h.log.Error("token auth: failed to json encode token response: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
}

func (h *TokenHandler) resolveAccess(r *http.Request, user *AuthenticatedUser, scopeStr string) []*ResourceActions {
	var result []*ResourceActions

	for scope := range strings.SplitSeq(scopeStr, " ") {
		parts := strings.SplitN(scope, ":", 3)
		if len(parts) != 3 {
			continue
		}

		resourceType := parts[0]
		resourceName := parts[1]
		requestedActions := strings.Split(parts[2], ",")

		if resourceType != "repository" {
			continue
		}

		granted := h.filterActions(r, user, resourceName, requestedActions)
		if len(granted) > 0 {
			result = append(result, &ResourceActions{
				Type:    resourceType,
				Name:    resourceName,
				Actions: granted,
			})
		}
	}

	return result
}

func (h *TokenHandler) filterActions(r *http.Request, user *AuthenticatedUser, repoName string, requested []string) []string {
	namespaceName := strings.SplitN(repoName, "/", 2)
	if len(namespaceName) != 2 {
		return nil
	}
	namespace := namespaceName[0]

	repo, err := h.store.GetRepository(r.Context(), namespace, namespaceName[1])
	if err != nil {
		h.log.Error("token auth: failed to look up repo %s: %v", repoName, err)
		return nil
	}

	var granted []string
	for _, action := range requested {
		switch action {
		case "pull":
			if h.canPull(r, user, namespace, repo) {
				granted = append(granted, "pull")
			}
		case "push":
			if h.canPush(r, user, namespace) {
				granted = append(granted, "push")
			}
		}
	}
	return granted
}

func (h *TokenHandler) canPull(r *http.Request, user *AuthenticatedUser, namespace string, repo *storage.Repository) bool {
	// Repo doesn't exist yet, only authenticated users can pull (will get 404 from registry)
	if repo == nil {
		return user != nil
	}
	// Public repos are pullable by anyone
	if !repo.IsPrivate {
		return true
	}
	if user == nil {
		return false
	}
	// Use RBAC: check if user has pull permission on repositories
	if h.enforcer != nil {
		allowed, _ := h.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionPull, namespace)
		if allowed {
			return true
		}
	}
	// Namespace owner can always pull their own repos
	if user.Username == namespace {
		return true
	}
	// Org member can pull org repos
	isMember, _, _ := h.store.IsOrgMember(r.Context(), namespace, user.ID)
	return isMember
}

func (h *TokenHandler) canPush(r *http.Request, user *AuthenticatedUser, namespace string) bool {
	if user == nil {
		return false
	}
	// Namespace owner can always push
	if user.Username == namespace {
		return true
	}
	// Org member with admin/owner role can push
	isMember, role, _ := h.store.IsOrgMember(r.Context(), namespace, user.ID)
	if isMember && (role == storage.OrgRoleOwner || role == storage.OrgRoleAdmin) {
		return true
	}
	// Admin-level override: users with manage permission can push to any valid namespace
	if h.enforcer != nil {
		canManage, _ := h.enforcer.Enforce(user.Roles, rbac.ResourceRepositories, rbac.ActionManage, namespace)
		if canManage {
			nsOwner, _ := h.store.GetUserByUsername(r.Context(), namespace)
			if nsOwner != nil {
				return true
			}
			org, _ := h.store.GetOrganization(r.Context(), namespace)
			if org != nil {
				return true
			}
		}
	}
	return false
}
