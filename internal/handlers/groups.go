package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
)

type GroupHandler struct {
	repo repository.Repository
}

func NewGroupHandler(repo repository.Repository) *GroupHandler {
	return &GroupHandler{repo: repo}
}

func (h *GroupHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.repo.ListGroups()
	if err != nil {
		log.Printf("Failed to list groups: %v", err)
		http.Error(w, "FAILED TO LIST GROUPS", http.StatusInternalServerError)
		return
	}

	// BUILD RESPONSE
	type GroupResponse struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Roles       []string `json:"roles"`
	}

	response := make([]GroupResponse, len(groups))
	for i, group := range groups {
		response[i] = GroupResponse{
			Name:        group.Name,
			Description: group.Description,
			Roles:       group.Roles,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode groups response: %v", err)
		http.Error(w, "FAILED TO ENCODE RESPONSE", http.StatusInternalServerError)
		return
	}
}

func (h *GroupHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var group models.Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	// NAME MUST MATCH
	if group.Name == "" {
		http.Error(w, "GROUP NAME REQUIRED", http.StatusBadRequest)
		return
	}

	// ROLES MUST EXIST
	for _, roleName := range group.Roles {
		if _, err := h.repo.GetRole(roleName); err != nil {
			http.Error(w, "INVALID ROLE: "+roleName, http.StatusBadRequest)
			return
		}
	}

	if err := h.repo.CreateGroup(&group); err != nil {
		http.Error(w, "GROUP CREATION FAILED", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *GroupHandler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	var group models.Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		http.Error(w, "INVALID REQUEST", http.StatusBadRequest)
		return
	}

	// NAME MUST MATCH
	group.Name = name

	// ROLES MUST EXIST
	for _, roleName := range group.Roles {
		if _, err := h.repo.GetRole(roleName); err != nil {
			http.Error(w, "INVALID ROLE: "+roleName, http.StatusBadRequest)
			return
		}
	}

	if err := h.repo.UpdateGroup(&group); err != nil {
		http.Error(w, "GROUP UPDATE FAILED", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *GroupHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if err := h.repo.DeleteGroup(name); err != nil {
		http.Error(w, "GROUP DELETION FAILED", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
