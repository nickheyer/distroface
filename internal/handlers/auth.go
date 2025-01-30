package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/config"
)

type AuthHandler struct {
	config *config.Config
	auth   auth.AuthService
}

func NewAuthHandler(cfg *config.Config, authService auth.AuthService) *AuthHandler {
	return &AuthHandler{
		config: cfg,
		auth:   authService,
	}
}

// REGISTRY V2 CHECK
func (h *AuthHandler) HandleV2Check(w http.ResponseWriter, r *http.Request) {
	fmt.Println("V2 HANDLE CHECK RECIEVED!")
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
	fmt.Printf("REGISTRY AUTH REQUEST: METHOD=%s CONTENT-TYPE=%s\n",
		r.Method, r.Header.Get("Content-Type"))

	var username, password, scope, service string

	// HANDLE FORM POST
	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			fmt.Printf("ERROR PARSING FORM: %v\n", err)
			http.Error(w, "INVALID FORM DATA", http.StatusBadRequest)
			return
		}

		// DUMP FORM DATA FOR DEBUGGING
		fmt.Printf("FORM DATA: %+v\n", r.PostForm)

		username = r.PostForm.Get("username") // DOCKER SENDS username
		password = r.PostForm.Get("password") // GET PASSWORD FROM FORM
		scope = r.PostForm.Get("scope")
		service = h.config.Auth.Service // USE CONFIGURED SERVICE
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

	fmt.Printf("PROCESSING AUTH REQUEST - USER=%s SCOPE=%s SERVICE=%s\n",
		username, scope, service)

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
		fmt.Printf("AUTH FAILED: %v\n", err)
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
		http.Error(w, "INTERNAL ERROR", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(regResponse); err != nil {
		fmt.Printf("ERROR ENCODING RESPONSE: %v\n", err)
		http.Error(w, "INTERNAL SERVER ERROR", http.StatusInternalServerError)
		return
	}
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
	webResponse, ok := response.(*auth.WebAuthResponse)
	if !ok || err != nil {
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
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	response, err := h.auth.RefreshToken(r.Context(), refreshRequest.RefreshToken)
	if err != nil {
		http.Error(w, "INVALID REFRESH TOKEN", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
