package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	cfg "github.com/nickheyer/distroface/internal/config"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
)

type SettingsHandler struct {
	repo   repository.Repository
	logger *log.Logger
	cfg    *cfg.Config
}

func NewSettingsHandler(repo repository.Repository, cfg *cfg.Config) *SettingsHandler {
	return &SettingsHandler{
		repo:   repo,
		logger: log.New(os.Stdout, "SETTINGS: ", log.LstdFlags),
		cfg:    cfg,
	}
}

// GetSettings returns all settings or specific section if specified
func (h *SettingsHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	section := vars["section"]

	var settings interface{}
	var err error

	if section != "" {
		settings, err = h.repo.GetSettingsSection(section)
		fmt.Printf("GETTING SETTINGS SECTION: %v\n", err)
		if err != nil {
			// GET DEFAULTS IF NOT FOUND
			settings, err = models.GetDefaultSettings(section)
			fmt.Printf("GETTING DEFAULT BECAUSE: %v\n", err)
			if err != nil {
				fmt.Printf("Failed to get settings: %v\n", err)
				http.Error(w, "Invalid settings section", http.StatusBadRequest)
				return
			}
		}
	} else {
		settings, err = h.repo.GetAllSettings()
		fmt.Printf("All settings: %v\n%v\n", err, settings)
	}

	if err != nil {
		fmt.Printf("Failed to get settings: %v\n", err)
		http.Error(w, "Failed to get settings", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

// UpdateSettings updates settings for a specific section
func (h *SettingsHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	section := vars["section"]

	fmt.Printf("UPDATING SETTINGS: %v\n", section)

	if section == "" {
		http.Error(w, "Section is required", http.StatusBadRequest)
		return
	}

	var settings json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// VALIDATE SETTINGS
	fmt.Printf("VALIDATING SETTINGS: %v\n", settings)
	if err := h.validateSettings(section, settings); err != nil {
		fmt.Printf("Settings validation failed: %v\n", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fmt.Printf("UPDATING SETTINGS: %v\n", settings)
	if err := h.repo.UpdateSettingsSection(section, settings); err != nil {
		fmt.Printf("Failed to update settings: %v\n", err)
		http.Error(w, "Failed to update settings", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// validateSettings validates settings based on section
func (h *SettingsHandler) validateSettings(section string, settings json.RawMessage) error {
	switch section {
	case "artifacts":
		var s models.ArtifactSettings
		if err := json.Unmarshal(settings, &s); err != nil {
			return err
		}
		return s.Validate()

	case "registry":
		var s models.RegistrySettings
		if err := json.Unmarshal(settings, &s); err != nil {
			return err
		}
		return s.Validate()

	case "auth":
		var s models.AuthSettings
		if err := json.Unmarshal(settings, &s); err != nil {
			return err
		}
		return s.Validate()

	default:
		return nil // No validation for unknown sections
	}
}

// ResetSettings resets settings to defaults for a section
func (h *SettingsHandler) ResetSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	section := vars["section"]

	if section == "" {
		http.Error(w, "Section is required", http.StatusBadRequest)
		return
	}

	// GET DEFAULT SETTINGS
	defaultSettings, err := models.GetDefaultSettings(section)
	if err != nil {
		h.logger.Printf("Failed to get default settings: %v", err)
		http.Error(w, "Invalid settings section", http.StatusBadRequest)
		return
	}

	// CONVERT TO JSON
	settingsJSON, err := json.Marshal(defaultSettings)
	if err != nil {
		h.logger.Printf("Failed to marshal default settings: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// UPDATE WITH DEFAULTS
	if err := h.repo.UpdateSettingsSection(section, settingsJSON); err != nil {
		h.logger.Printf("Failed to reset settings: %v", err)
		http.Error(w, "Failed to reset settings", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
