package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/auth/permissions"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
)

type RoleHandler struct {
	repo        repository.Repository
	permManager *permissions.PermissionManager
}

func NewRoleHandler(repo repository.Repository, permManager *permissions.PermissionManager) *RoleHandler {
	return &RoleHandler{
		repo:        repo,
		permManager: permManager,
	}
}

type RoleResponse struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Permissions []models.Permission `json:"permissions"`
}

func (h *RoleHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.repo.ListRoles()
	if err != nil {
		log.Printf("Failed to list roles: %v", err)
		http.Error(w, "FAILED TO LIST ROLES", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(roles); err != nil {
		log.Printf("Failed to encode roles response: %v", err)
		http.Error(w, "FAILED TO ENCODE RESPONSE", http.StatusInternalServerError)
		return
	}
}

func (h *RoleHandler) CreateRole(w http.ResponseWriter, r *http.Request) {
	var roleRequest models.Role
	if err := json.NewDecoder(r.Body).Decode(&roleRequest); err != nil {
		log.Printf("Failed to decode role request: %v", err)
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	if roleRequest.Name == "" {
		http.Error(w, "ROLE NAME REQUIRED", http.StatusBadRequest)
		return
	}

	if err := h.repo.CreateRole(&roleRequest); err != nil {
		log.Printf("Failed to create role: %v", err)
		http.Error(w, "ROLE CREATION FAILED", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *RoleHandler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// ROLE MUST EXIST
	if _, err := h.repo.GetRole(name); err != nil {
		log.Printf("Role not found: %v", err)
		http.Error(w, "ROLE NOT FOUND", http.StatusNotFound)
		return
	}

	if err := h.repo.DeleteRole(name); err != nil {
		log.Printf("Failed to delete role: %v", err)
		http.Error(w, "ROLE DELETION FAILED", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *RoleHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	// EXTRACT ROLE NAME FROM URL
	vars := mux.Vars(r)
	name := vars["name"]

	// CHECK IF ROLE EXISTS
	existingRole, err := h.repo.GetRole(name)
	if err != nil {
		log.Printf("Role not found: %v", err)
		http.Error(w, "ROLE NOT FOUND", http.StatusNotFound)
		return
	}

	// DECODE REQUEST BODY
	var roleRequest struct {
		Description string              `json:"description"`
		Permissions []models.Permission `json:"permissions"`
	}

	if err := json.NewDecoder(r.Body).Decode(&roleRequest); err != nil {
		log.Printf("Failed to decode role update request: %v", err)
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	// VALIDATE PERMISSIONS
	for _, perm := range roleRequest.Permissions {
		if !isValidAction(perm.Action) || !isValidResource(perm.Resource) {
			log.Printf("Invalid permission: Action=%s, Resource=%s", perm.Action, perm.Resource)
			http.Error(w, "INVALID PERMISSION", http.StatusBadRequest)
			return
		}

		if perm.Scope != "" && !isValidScope(perm.Scope) {
			log.Printf("Invalid scope: %s", perm.Scope)
			http.Error(w, "INVALID SCOPE", http.StatusBadRequest)
			return
		}
	}

	// PREPARE UPDATED ROLE
	updatedRole := &models.Role{
		ID:          existingRole.ID,
		Name:        existingRole.Name,
		Description: roleRequest.Description,
		Permissions: roleRequest.Permissions,
		CreatedAt:   existingRole.CreatedAt,
	}

	// UPDATE ROLE IN DATABASE
	if err := h.repo.UpdateRole(updatedRole); err != nil {
		log.Printf("Failed to update role: %v", err)
		http.Error(w, "ROLE UPDATE FAILED", http.StatusInternalServerError)
		return
	}

	// RETURN SUCCESS
	w.WriteHeader(http.StatusOK)
}

// HELPER FUNCTIONS TO VALIDATE PERMISSIONS
func isValidAction(action models.Action) bool {
	validActions := map[models.Action]bool{
		models.ActionView:     true,
		models.ActionCreate:   true,
		models.ActionUpdate:   true,
		models.ActionDelete:   true,
		models.ActionPush:     true,
		models.ActionPull:     true,
		models.ActionAdmin:    true,
		models.ActionLogin:    true,
		models.ActionLogout:   true,
		models.ActionMigrate:  true,
		models.ActionUpload:   true,
		models.ActionDownload: true,
	}
	return validActions[action]
}

func isValidResource(resource models.Resource) bool {
	validResources := map[models.Resource]bool{
		models.ResourceWebUI:    true,
		models.ResourceImage:    true,
		models.ResourceTag:      true,
		models.ResourceUser:     true,
		models.ResourceGroup:    true,
		models.ResourceSystem:   true,
		models.ResourceTask:     true,
		models.ResourceArtifact: true,
		models.ResourceRepo:     true,
	}
	return validResources[resource]
}

func isValidScope(scope models.Scope) bool {
	validScopes := map[models.Scope]bool{
		models.ScopeGlobal:     true,
		models.ScopeRepository: true,
		models.ScopeProject:    true,
	}
	return validScopes[scope]
}
