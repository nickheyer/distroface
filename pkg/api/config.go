package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

const (
	defaultServerURL = "http://localhost:8080"
	patPrefix        = "df_"
)

var cfgFile string

// Persisted at ~/.dfcli/config.json
type AuthConfig struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Username  string    `json:"username"`
	Server    string    `json:"server"`
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		configDir := filepath.Join(homeDir(), ".dfcli")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create config directory: %v\n", err)
			os.Exit(1)
		}
		viper.AddConfigPath(configDir)
		viper.SetConfigName("config")
		viper.SetConfigType("json")
	}

	viper.AutomaticEnv()

	// Missing config just means not logged in yet
	if err := viper.ReadInConfig(); err != nil {
		_, notFound := err.(viper.ConfigFileNotFoundError)
		if !notFound && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "Failed to read config file: %v\n", err)
			os.Exit(1)
		}
	}
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func configPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	return filepath.Join(homeDir(), ".dfcli", "config.json")
}

// Instance CA trusted by dfcli, installed via trust install
func caPath() string {
	return filepath.Join(filepath.Dir(configPath()), "ca.pem")
}

func readAuthConfig() (AuthConfig, error) {
	var config AuthConfig
	data, err := os.ReadFile(configPath())
	if err != nil {
		return config, nil
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("failed to parse config %s: %v", configPath(), err)
	}
	return config, nil
}

func saveConfig(config AuthConfig) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	// Preserve fields not being overwritten
	if existing, err := readAuthConfig(); err == nil && config.Username == "" {
		config.Username = existing.Username
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}
	return os.WriteFile(path, data, 0600)
}
