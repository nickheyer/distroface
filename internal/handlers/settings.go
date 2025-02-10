package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
)

type SettingsHandler struct {
	repo repository.Repository
	log  *logging.LogService
	cfg  *models.Config
}

func NewSettingsHandler(repo repository.Repository, cfg *models.Config, log *logging.LogService) *SettingsHandler {
	return &SettingsHandler{
		repo: repo,
		log:  log,
		cfg:  cfg,
	}
}

func (h *SettingsHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	section := vars["section"]

	if section == "config" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(h.cfg)
		return
	}

	settings, err := h.getSettingsForSection(section)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

func (h *SettingsHandler) getSettingsForSection(section string) (interface{}, error) {
	// GET DEFAULTS
	settings, err := models.GetSettingsWithDefaults(section)
	if err != nil {
		return nil, err
	}

	// GET STORED SETTINGS
	data, err := h.repo.GetSettingsSection(section)
	if err != nil {
		// RETURN DEFAULTS IF NOT STORED
		return settings, nil
	}

	// MERGE STORED WITH DEFAULTS
	if err := json.Unmarshal(data, settings); err != nil {
		return nil, h.log.Errorf("failed to parse settings", err)
	}

	return settings, nil
}

func (h *SettingsHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	section := vars["section"]

	settings, err := models.NewSettings(section)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := json.NewDecoder(r.Body).Decode(settings); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// HANDLE REGULAR SETTINGS
	if err := settings.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(settings)
	if err := h.repo.UpdateSettingsSection(section, data); err != nil {
		http.Error(w, "Failed to update settings", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *SettingsHandler) ResetSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	section := vars["section"]

	// DEFAULTS
	settings, err := models.GetSettingsWithDefaults(section)
	if err != nil {
		http.Error(w, "Invalid settings section", http.StatusBadRequest)
		return
	}

	data, err := json.Marshal(settings)
	if err != nil {
		http.Error(w, "Failed to marshal settings", http.StatusInternalServerError)
		return
	}

	if err := h.repo.UpdateSettingsSection(section, data); err != nil {
		http.Error(w, "Failed to reset settings", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
