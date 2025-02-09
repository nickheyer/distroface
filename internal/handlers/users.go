package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/auth/permissions"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
)

type UserHandler struct {
	repo        repository.Repository
	permManager *permissions.PermissionManager
	log         *logging.LogService
}

func NewUserHandler(repo repository.Repository, permManager *permissions.PermissionManager, log *logging.LogService) *UserHandler {
	return &UserHandler{
		repo:        repo,
		permManager: permManager,
		log:         log,
	}
}

func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var user struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		Groups   []string `json:"groups"`
	}

	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	// VALIDATE USERNAME
	if user.Username == "" {
		http.Error(w, "USERNAME REQUIRED", http.StatusBadRequest)
		return
	}

	// VALIDATE PASSWORD
	if user.Password == "" {
		http.Error(w, "PASSWORD REQUIRED", http.StatusBadRequest)
		return
	}

	// VALIDATE GROUPS
	if err := h.validateGroups(user.Groups); err != nil {
		log.Printf("Group validation failed: %v", err)
		http.Error(w, fmt.Sprintf("INVALID GROUPS: %v", err), http.StatusBadRequest)
		return
	}

	// GROUPS TO LOWERCASE
	normalizedGroups := make([]string, len(user.Groups))
	for i, group := range user.Groups {
		normalizedGroups[i] = strings.ToLower(group)
	}

	hashedPassword, err := auth.HashPassword(user.Password)
	if err != nil {
		http.Error(w, "PASSWORD PROCESSING FAILED", http.StatusInternalServerError)
		return
	}

	newUser := &models.User{
		Username: user.Username,
		Password: hashedPassword,
		Groups:   normalizedGroups,
	}

	if err := h.repo.CreateUser(newUser); err != nil {
		http.Error(w, "USER CREATION FAILED", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *UserHandler) UpdateUserGroups(w http.ResponseWriter, r *http.Request) {
	var update struct {
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	}

	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	// GET USER FROM CONTEXT
	requestingUser, ok := r.Context().Value(constants.UsernameKey).(string)
	if !ok || requestingUser == "" {
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

	// CHECK IF USER CAN UPDATE USERS
	if !h.permManager.HasPermission(r.Context(), requestingUser, models.Permission{
		Action:   models.ActionUpdate,
		Resource: models.ResourceUser,
	}) {
		http.Error(w, "FORBIDDEN", http.StatusForbidden)
		return
	}

	// FOR UPDATE SELF
	if update.Username == requestingUser {
		currentUser, err := h.repo.GetUser(requestingUser)
		if err != nil {
			http.Error(w, "USER NOT FOUND", http.StatusNotFound)
			return
		}

		// CHECK IF USER RM THEIR OWN ADMIN - TELL THEM NO
		hasAdmin := false
		willHaveAdmin := false
		for _, group := range currentUser.Groups {
			if group == "admins" {
				hasAdmin = true
				break
			}
		}
		for _, group := range update.Groups {
			if group == "admins" {
				willHaveAdmin = true
				break
			}
		}

		if hasAdmin && !willHaveAdmin {
			http.Error(w, "CANNOT REMOVE OWN ADMIN ACCESS", http.StatusForbidden)
			return
		}
	}

	// VALIDATE GROUPS
	if err := h.validateGroups(update.Groups); err != nil {
		log.Printf("Group validation failed: %v", err)
		http.Error(w, fmt.Sprintf("INVALID GROUPS: %v", err), http.StatusBadRequest)
		return
	}

	if err := h.repo.UpdateUserGroups(update.Username, update.Groups); err != nil {
		http.Error(w, "UPDATE FAILED", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.repo.ListUsers()
	if err != nil {
		http.Error(w, "QUERY FAILED", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(users)
}

func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	var username string
	if r.URL.Path == "/api/v1/users/me" {
		ctxUsername, ok := r.Context().Value(constants.UsernameKey).(string)
		if !ok || ctxUsername == "" {
			log.Printf("No username found in context for /me endpoint")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		username = ctxUsername
	} else {
		username = mux.Vars(r)["username"]
	}

	log.Printf("GetUser handler processing request for username: %s", username)

	if username == "" {
		http.Error(w, "USERNAME REQUIRED", http.StatusBadRequest)
		return
	}

	user, err := h.repo.GetUser(username)
	if err != nil {
		log.Printf("Error getting user from database: %v", err)
		http.Error(w, "USER NOT FOUND", http.StatusNotFound)
		return
	}

	userResponse := struct {
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	}{
		Username: user.Username,
		Groups:   user.Groups,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userResponse)
}

func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	usernameToDelete := vars["username"]

	// GET TARGET USER FROM CONTEXT
	requestingUser, ok := r.Context().Value(constants.UsernameKey).(string)
	if !ok || requestingUser == "" {
		http.Error(w, "UNAUTHORIZED", http.StatusUnauthorized)
		return
	}

	// CANT DELETE SELF, THIS MAKES SENSE RIGHT?
	if requestingUser == usernameToDelete {
		http.Error(w, "CANNOT DELETE YOUR OWN ACCOUNT", http.StatusBadRequest)
		return
	}

	// CHECK IF TARGET USER EXISTS
	if _, err := h.repo.GetUser(usernameToDelete); err != nil {
		http.Error(w, "USER NOT FOUND", http.StatusNotFound)
		return
	}

	// DELETE USER
	if err := h.repo.DeleteUser(usernameToDelete); err != nil {
		h.log.Printf("Failed to delete user: %v", err)
		http.Error(w, "FAILED TO DELETE USER", http.StatusInternalServerError)
		return
	}

	// RETURN 204
	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandler) validateGroups(groups []string) error {
	validGroups, err := h.repo.ListGroups()
	if err != nil {
		return fmt.Errorf("failed to fetch valid groups: %v", err)
	}

	// CREATE MAP OF GROUPS
	validGroupMap := make(map[string]bool)
	for _, group := range validGroups {
		validGroupMap[strings.ToLower(group.Name)] = true
	}

	// VALIDATE AND NORMALIZE
	for _, group := range groups {
		if !validGroupMap[strings.ToLower(group)] {
			return fmt.Errorf("invalid group: %s", group)
		}
	}

	return nil
}
