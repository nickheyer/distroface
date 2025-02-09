package utils

import (
	"encoding/json"
	"fmt"

	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
)

func GetSettings[T models.Settings](repo repository.Repository, section string) (T, error) {
	var empty T

	// GET BASE SETTINGS
	settings, err := models.GetSettingsWithDefaults(section)
	if err != nil {
		return empty, fmt.Errorf("failed to get settings: %v", err)
	}

	// GET STORED SETTINGS
	data, err := repo.GetSettingsSection(section)
	if err != nil {
		// IF NOT STORED, RETURN DEFAULTS
		if typed, ok := settings.(T); ok {
			return typed, nil
		}
		return empty, fmt.Errorf("invalid settings type")
	}

	// MERGE STORED WITH DEFAULTS
	if err := json.Unmarshal(data, settings); err != nil {
		return empty, fmt.Errorf("failed to parse settings: %v", err)
	}

	// TYPE ASSERT BECAUSE THIS IS WHY GO SUCKS
	if typed, ok := settings.(T); ok {
		return typed, nil
	}

	return empty, fmt.Errorf("invalid settings type")
}
