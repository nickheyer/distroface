package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"golang.org/x/crypto/bcrypt"
)

// TokenHandler implements the Docker Token Authentication Specification.
// GET /auth/token?service=<svc>&scope=<scope>&account=<acct>
type TokenHandler struct {
	tokenService *TokenService
	store        *storage.Store
	log          *logger.Logger
}

type tokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
}

// NewTokenHandler creates a new Docker token auth endpoint handler.
func NewTokenHandler(ts *TokenService, store *storage.Store, log *logger.Logger) *TokenHandler {
	return &TokenHandler{
		tokenService: ts,
		store:        store,
		log:          log,
	}
}

func (h *TokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	service := q.Get("service")
	scopeStr := q.Get("scope")
	account := q.Get("account")

	var user *storage.User
	username, password, hasBasic := r.BasicAuth()
	if hasBasic && username != "" {
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
		user = u
		if account == "" {
			account = user.Username
		}
	}

	var access []*ResourceActions
	if scopeStr != "" {
		access = h.resolveAccess(r, user, scopeStr)
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
	json.NewEncoder(w).Encode(resp)
}

func (h *TokenHandler) resolveAccess(r *http.Request, user *storage.User, scopeStr string) []*ResourceActions {
	var result []*ResourceActions

	for _, scope := range strings.Split(scopeStr, " ") {
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

func (h *TokenHandler) filterActions(r *http.Request, user *storage.User, repoName string, requested []string) []string {
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
			if h.canPull(user, namespace, repo) {
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

func (h *TokenHandler) canPull(user *storage.User, namespace string, repo *storage.Repository) bool {
	if repo == nil {
		return user != nil
	}
	if !repo.IsPrivate {
		return true
	}
	if user == nil {
		return false
	}
	if user.IsAdmin {
		return true
	}
	return user.Username == namespace
}

func (h *TokenHandler) canPush(r *http.Request, user *storage.User, namespace string) bool {
	if user == nil {
		return false
	}
	if user.Username == namespace {
		return true
	}
	if !user.IsAdmin {
		return false
	}
	// Admin can push to other namespaces, but only if the namespace owner exists
	nsOwner, err := h.store.GetUserByUsername(r.Context(), namespace)
	if err != nil || nsOwner == nil {
		return false
	}
	return true
}
