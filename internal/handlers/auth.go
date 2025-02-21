package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/models"
	"go.uber.org/zap"
)

type AuthHandler struct {
	config *models.Config
	auth   auth.AuthService
	log    *logging.LogService
}

func NewAuthHandler(cfg *models.Config, authService auth.AuthService, log *logging.LogService) *AuthHandler {
	return &AuthHandler{
		config: cfg,
		auth:   authService,
		log:    log,
	}
}

// REGISTRY V2 CHECK
func (h *AuthHandler) HandleV2Check(w http.ResponseWriter, r *http.Request) {
	h.log.Info("V2 HANDLE CHECK RECIEVED")
	authHeader := r.Header.Get("Authorization")

	if authHeader == "" {
		challenge := fmt.Sprintf(`Bearer realm="%s",service="%s"`,
			h.config.Auth.Realm,
			h.config.Auth.Service)
		w.Header().Set("WWW-Authenticate", challenge)
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// VALIDATE TOKEN
	h.log.Info("VALIDATING BEARER TOKEN")
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	if _, err := h.auth.ValidateToken(r.Context(), tokenString); err != nil {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer realm="%s",service="%s",error="invalid_token"`,
				h.config.Auth.Realm,
				h.config.Auth.Service))
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	w.WriteHeader(http.StatusOK)
}

// HANDLE REGISTRY AUTH TOKEN REQUEST
func (h *AuthHandler) HandleRegistryAuth(w http.ResponseWriter, r *http.Request) {
	h.log.Info("REGISTRY AUTH REQUEST",
		zap.String("METHOD", r.Method),
		zap.Any("CONTENT TYPE", r.Header.Get("Content-Type")))

	var username, password, scope, service string

	// HANDLE FORM POST
	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			h.log.Error("ERROR PARSING FORM", err)
			http.Error(w, "INVALID FORM DATA", http.StatusBadRequest)
			return
		}

		// DUMP FORM DATA FOR DEBUGGING
		h.log.Debug("REGISTRY AUTH FORM",
			zap.Any("FORM DATA", r.PostForm))

		username = r.PostForm.Get("username")
		password = r.PostForm.Get("password")
		scope = r.PostForm.Get("scope")
		service = h.config.Auth.Service
	} else {
		// HANDLE BASIC AUTH
		if basicUser, basicPass, ok := r.BasicAuth(); ok {
			username = basicUser
			password = basicPass
		}
		scope = r.URL.Query().Get("scope")
		service = r.URL.Query().Get("service")
		if service == "" {
			service = h.config.Auth.Service
		}
	}

	// DEFAULT TO ANONYMOUS IF NO USERNAME
	if username == "" {
		username = "anonymous"
	}

	h.log.Debug("PROCESSING AUTH REQUEST",
		zap.String("USER", username),
		zap.String("SCOPE", scope),
		zap.String("SERVICE", service))

	// CREATE AUTH REQUEST
	authReq := auth.AuthRequest{
		Username: username,
		Password: password,
		Scope:    scope,
		Service:  service,
		Type:     auth.AuthTypeRegistry,
	}

	// AUTHENTICATE
	response, err := h.auth.Authenticate(r.Context(), authReq)
	if err != nil {
		h.log.Error("AUTH FAILED", err)
		challenge := fmt.Sprintf(`Bearer realm="%s",service="%s"`,
			h.config.Auth.Realm,
			h.config.Auth.Service)
		if scope != "" {
			challenge += fmt.Sprintf(`,scope="%s"`, scope)
		}
		w.Header().Set("WWW-Authenticate", challenge)
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

	regResponse, ok := response.(*auth.RegAuthResponse)
	if !ok {
		h.log.Error("INVALID RESPONSE TYPE", fmt.Errorf("expected RegAuthResponse"))
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	// SET RECOMENDED HEADERS
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if err := json.NewEncoder(w).Encode(regResponse); err != nil {
		h.log.Error("FAILED TO ENCODE RESPONSE", err)
		http.Error(w, "INTERNAL SERVER ERROR", http.StatusInternalServerError)
		return
	}

	h.log.Info("AUTH SUCCESS",
		zap.String("USER", username),
		zap.String("SCOPE", scope))
}

// WEB UI LOGIN
func (h *AuthHandler) HandleWebLogin(w http.ResponseWriter, r *http.Request) {
	var loginRequest struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&loginRequest); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	// USE WEB AUTH TYPE FOR WEB LOGIN
	authReq := auth.AuthRequest{
		Username: loginRequest.Username,
		Password: loginRequest.Password,
		Type:     auth.AuthTypeWeb,
		Service:  h.config.Auth.Service,
	}

	response, err := h.auth.Authenticate(r.Context(), authReq)
	if err != nil {
		h.log.Error("ATTEMPTED LOGIN WITH INVALID CREDENTIALS", err)
		http.Error(w, "INVALID CREDENTIALS", http.StatusUnauthorized)
		return
	}

	webResponse, ok := response.(*auth.WebAuthResponse)
	if !ok {
		h.log.Error("ERROR CONVERTING WEB AUTH RESPONSE", err)
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(webResponse)
}

// WEB UI TOKEN REFRESH
func (h *AuthHandler) HandleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	var refreshRequest struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&refreshRequest); err != nil {
		h.log.Error("INVALID REQUEST", err)
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	response, err := h.auth.RefreshToken(r.Context(), refreshRequest.RefreshToken)
	if err != nil {
		h.log.Error("INVALID REFRESH TOKEN", err)
		http.Error(w, "INVALID REFRESH TOKEN", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
