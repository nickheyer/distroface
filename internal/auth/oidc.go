package auth

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/config"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/pkg/logger"
	"golang.org/x/oauth2"
)

// OIDCHandler implements OIDC-based authentication flows.
type OIDCHandler struct {
	manager      *Manager
	store        *db.Store
	config       *config.OIDCConfig
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth2Config *oauth2.Config
	httpClient   *http.Client
	log          *logger.Logger
}

// NewOIDCHandler creates a new OIDC handler. If OIDC is disabled in config,
// the handler is returned with a nil provider (IsEnabled() returns false).
func NewOIDCHandler(manager *Manager, store *db.Store, cfg *config.OIDCConfig, log *logger.Logger) *OIDCHandler {
	h := &OIDCHandler{
		manager: manager,
		store:   store,
		config:  cfg,
		log:     log,
	}

	if !cfg.Enabled {
		return h
	}

	ctx := context.Background()
	if cfg.SkipTLSVerify {
		h.httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
		ctx = oidc.ClientContext(ctx, h.httpClient)
	}

	provider, err := oidc.NewProvider(ctx, cfg.IssuerURI)
	if err != nil {
		log.Error("OIDC: failed to discover provider at %s: %v", cfg.IssuerURI, err)
		return h
	}

	h.provider = provider
	h.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}

	h.oauth2Config = &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}

	log.Info("OIDC handler initialized (issuer: %s)", cfg.IssuerURI)
	return h
}

// IsEnabled returns true if OIDC is configured and the provider was discovered.
func (h *OIDCHandler) IsEnabled() bool {
	return h.config.Enabled && h.provider != nil
}

// HandleLogin redirects the user to the OIDC provider's authorization endpoint.
func (h *OIDCHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if !h.IsEnabled() {
		http.Error(w, "OIDC is not enabled", http.StatusBadRequest)
		return
	}

	state, err := generateState()
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, h.oauth2Config.AuthCodeURL(state), http.StatusFound)
}

// HandleCallback processes the OIDC callback, verifies the ID token,
// finds or creates the user, maps roles, creates a session, and redirects.
func (h *OIDCHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if !h.IsEnabled() {
		http.Error(w, "OIDC is not enabled", http.StatusBadRequest)
		return
	}

	// Verify state
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	// Exchange code for token
	ctx := r.Context()
	if h.httpClient != nil {
		ctx = oidc.ClientContext(ctx, h.httpClient)
	}
	oauth2Token, err := h.oauth2Config.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		h.log.Error("OIDC: code exchange failed: %v", err)
		http.Error(w, "code exchange failed", http.StatusInternalServerError)
		return
	}

	// Extract and verify ID token
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token in response", http.StatusInternalServerError)
		return
	}
	idToken, err := h.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		h.log.Error("OIDC: token verification failed: %v", err)
		http.Error(w, "token verification failed", http.StatusInternalServerError)
		return
	}

	// Extract claims
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		h.log.Error("OIDC: failed to parse claims: %v", err)
		http.Error(w, "failed to parse claims", http.StatusInternalServerError)
		return
	}

	// Fetch UserInfo for additional claims
	tokenSource := h.oauth2Config.TokenSource(ctx, oauth2Token)
	userInfo, err := h.provider.UserInfo(ctx, tokenSource)
	if err == nil {
		var uiClaims map[string]any
		if err := userInfo.Claims(&uiClaims); err == nil {
			for k, v := range uiClaims {
				if _, exists := claims[k]; !exists {
					claims[k] = v
				}
			}
		}
	}

	// Extract user info
	sub := idToken.Subject
	email, _ := claims["email"].(string)
	username, _ := claims["preferred_username"].(string)
	if username == "" {
		username, _ = claims["name"].(string)
	}
	if username == "" {
		username = email
	}
	if username == "" {
		username = sub
	}

	// Find or create user
	user, err := h.findOrCreateOIDCUser(ctx, sub, username, email)
	if err != nil {
		h.log.Error("OIDC: user lookup/create failed: %v", err)
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}

	// Map claims to roles
	h.mapClaimsToRoles(ctx, user.ID, claims)

	// Get roles and create session
	roleNames, _ := h.store.GetUserRoleNames(ctx, user.ID)
	expiresAt := time.Now().Add(time.Duration(h.manager.config.SessionTimeout) * time.Second)
	token, err := h.manager.generateJWT(user.ID, user.Username, roleNames, expiresAt)
	if err != nil {
		h.log.Error("OIDC: JWT generation failed: %v", err)
		http.Error(w, "session creation failed", http.StatusInternalServerError)
		return
	}

	session := &db.Session{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: expiresAt,
	}
	if err := h.store.CreateSession(ctx, session); err != nil {
		h.log.Error("OIDC: session store failed: %v", err)
		http.Error(w, "session creation failed", http.StatusInternalServerError)
		return
	}

	h.log.Info("OIDC: user %s authenticated via OIDC", user.Username)
	http.Redirect(w, r, "/login?token="+token, http.StatusFound)
}

// findOrCreateOIDCUser looks up a user by OIDC subject and creates them if new.
func (h *OIDCHandler) findOrCreateOIDCUser(ctx context.Context, sub, username, email string) (*db.User, error) {
	user, err := h.store.GetUserByOIDCSubject(ctx, sub)
	if err != nil {
		return nil, err
	}

	if user != nil {
		if !user.IsActive {
			return nil, ErrUserNotActive
		}
		// Update email and last login
		if email != "" {
			user.Email = &email
		}
		now := time.Now()
		user.LastLogin = &now
		_ = h.store.UpdateUser(ctx, user)
		return user, nil
	}

	// Create new OIDC user
	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}

	user = &db.User{
		ID:           uuid.New().String(),
		Username:     username,
		Email:        emailPtr,
		AuthProvider: "oidc",
		OIDCSubject:  sub,
		OIDCIssuer:   h.config.IssuerURI,
		IsActive:     true,
	}

	if err := h.store.CreateUser(ctx, user); err != nil {
		return nil, err
	}

	// Assign default roles
	defaultRoles, _ := h.store.GetDefaultRoles(ctx)
	for _, role := range defaultRoles {
		_ = h.store.AssignRole(ctx, user.ID, role.Name, "oidc")
	}

	h.log.Info("OIDC: created new user %s (sub: %s)", username, sub)
	return user, nil
}

// mapClaimsToRoles maps OIDC claim values to local roles using the configured mapping.
func (h *OIDCHandler) mapClaimsToRoles(ctx context.Context, userID string, claims map[string]any) {
	if h.config.RoleClaim == "" {
		return
	}

	claimVal, ok := claims[h.config.RoleClaim]
	if !ok {
		h.log.Debug("OIDC: role claim %q not found in token", h.config.RoleClaim)
		return
	}

	var claimValues []string
	switch v := claimVal.(type) {
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				claimValues = append(claimValues, s)
			}
		}
	case string:
		var arr []string
		if json.Unmarshal([]byte(v), &arr) == nil {
			claimValues = arr
		} else {
			claimValues = []string{v}
		}
	}

	for _, cv := range claimValues {
		if len(h.config.RoleMapping) > 0 {
			if localRole, ok := h.config.RoleMapping[cv]; ok {
				_ = h.store.AssignRole(ctx, userID, localRole, "oidc")
			}
		} else {
			_ = h.store.AssignRole(ctx, userID, cv, "oidc")
		}
	}
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
