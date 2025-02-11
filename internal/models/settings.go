package models

import (
	"encoding/json"
	"fmt"
)

type Config struct {
	Server struct {
		Port        string `json:"port" yaml:"port"`
		Domain      string `json:"domain" yaml:"domain"`
		RSAKeyFile  string `json:"rsaKeyFile" yaml:"rsa_key_file"`
		TLSKeyFile  string `json:"tlsKeyFile" yaml:"tls_key_file"`
		TLSCertFile string `json:"tlsCertFile" yaml:"tls_certificate"`
		CertBundle  string `json:"certBundle" yaml:"cert_bundle"`
	} `json:"server" yaml:"server"`

	Storage struct {
		RootDirectory string `json:"rootDirectory" yaml:"root_directory"`
	} `json:"storage" yaml:"storage"`

	Database struct {
		Path string `json:"path" yaml:"path"`
	} `json:"database" yaml:"database"`

	Auth struct {
		Realm   string `json:"realm" yaml:"realm"`
		Service string `json:"service" yaml:"service"`
		Issuer  string `json:"issuer" yaml:"issuer"`
	} `json:"auth" yaml:"auth"`

	Init struct {
		Drop     bool   `json:"drop" yaml:"drop"`
		Roles    bool   `json:"roles" yaml:"roles"`
		Groups   bool   `json:"groups" yaml:"groups"`
		User     bool   `json:"user" yaml:"user"`
		Username string `json:"username" yaml:"username"`
		Password string `json:"password" yaml:"password"`
	} `json:"init" yaml:"init"`
}

// SETTINGS KEYS
const (
	SettingKeyArtifacts = "artifacts"
	SettingKeyRegistry  = "registry"
	SettingKeyAuth      = "auth"
)

// SETTINGS INTERFACE
type Settings interface {
	Validate() error
	GetDefaults() Settings
	Section() string
	IsStartupOnly() bool
}

// ARTIFACT SETTINGS
type ArtifactSettings struct {
	Retention  RetentionPolicy  `json:"retention"`
	Storage    StorageConfig    `json:"storage"`
	Properties PropertiesConfig `json:"properties"`
	Search     SearchConfig     `json:"search"`
}

func (s *ArtifactSettings) Section() string {
	return "artifacts"
}

func (s *ArtifactSettings) GetDefaults() Settings {
	return &ArtifactSettings{
		Retention: RetentionPolicy{
			Enabled:       false,
			MaxVersions:   5,
			MaxAge:        30,
			ExcludeLatest: true,
		},
		Storage: StorageConfig{
			MaxFileSize:        1024 * 10, // 10GB
			AllowedTypes:       []string{"*/*"},
			CompressionEnabled: true,
		},
		Properties: PropertiesConfig{
			Required: []string{},
			Indexed:  []string{"build", "branch", "commit"},
		},
		Search: SearchConfig{
			MaxResults:   100,
			DefaultSort:  "created_at",
			DefaultOrder: "DESC",
		},
	}
}

type RetentionPolicy struct {
	Enabled       bool `json:"enabled"`
	MaxVersions   int  `json:"maxVersions"`
	MaxAge        int  `json:"maxAge"` // IN DAYS
	ExcludeLatest bool `json:"excludeLatest"`
}

type StorageConfig struct {
	MaxFileSize        int64    `json:"maxFileSize"` // IN MB
	AllowedTypes       []string `json:"allowedTypes"`
	CompressionEnabled bool     `json:"compressionEnabled"`
}

type PropertiesConfig struct {
	Required []string `json:"required"`
	Indexed  []string `json:"indexed"`
}

type SearchConfig struct {
	MaxResults   int    `json:"maxResults"`
	DefaultSort  string `json:"defaultSort"`
	DefaultOrder string `json:"defaultOrder"`
}

func (s *ArtifactSettings) Validate() error {
	if s.Retention.MaxVersions < 0 {
		return fmt.Errorf("maxVersions cannot be negative")
	}
	if s.Retention.MaxAge < 0 {
		return fmt.Errorf("maxAge cannot be negative")
	}
	if s.Storage.MaxFileSize < 0 {
		return fmt.Errorf("maxFileSize cannot be negative")
	}
	if s.Search.MaxResults <= 0 {
		return fmt.Errorf("maxResults must be positive")
	}
	return nil
}

func (s *ArtifactSettings) IsStartupOnly() bool {
	return false // Can be modified at runtime
}

// REGISTRY SETTINGS
type RegistrySettings struct {
	Cleanup CleanupPolicy `json:"cleanup"`
	Proxy   ProxyConfig   `json:"proxy"`
	Storage StorageConfig `json:"storage"`
}

func (s *RegistrySettings) Section() string {
	return "registry"
}

func (s *RegistrySettings) GetDefaults() Settings {
	return &RegistrySettings{
		Cleanup: CleanupPolicy{
			Enabled:     false,
			MaxAge:      90,
			UnusedOnly:  true,
			MinVersions: 1,
		},
		Proxy: ProxyConfig{
			Enabled:      false,
			RemoteURL:    "",
			CacheEnabled: true,
			CacheMaxAge:  24,
		},
		Storage: StorageConfig{
			MaxFileSize:        10240,
			AllowedTypes:       []string{"application/vnd.docker.distribution.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"},
			CompressionEnabled: true,
		},
	}
}

type CleanupPolicy struct {
	Enabled     bool `json:"enabled"`
	MaxAge      int  `json:"maxAge"` // DAYS
	UnusedOnly  bool `json:"unusedOnly"`
	MinVersions int  `json:"minVersions"` // MIN VERSIONS TO KEEP
}

type ProxyConfig struct {
	Enabled      bool   `json:"enabled"`
	RemoteURL    string `json:"remoteUrl"`
	CacheEnabled bool   `json:"cacheEnabled"`
	CacheMaxAge  int    `json:"cacheMaxAge"` // HOURS
}

func (s *RegistrySettings) Validate() error {
	if s.Cleanup.MaxAge < 0 {
		return fmt.Errorf("maxAge cannot be negative")
	}
	if s.Cleanup.MinVersions < 0 {
		return fmt.Errorf("minVersions cannot be negative")
	}
	if s.Proxy.CacheMaxAge < 0 {
		return fmt.Errorf("cacheMaxAge cannot be negative")
	}
	if s.Proxy.Enabled && s.Proxy.RemoteURL == "" {
		return fmt.Errorf("remoteUrl is required when proxy is enabled")
	}
	return nil
}

func (s *RegistrySettings) IsStartupOnly() bool {
	return false
}

// AUTH SETTINGS
type AuthSettings struct {
	TokenExpiry    int      `json:"tokenExpiry"`
	SessionTimeout int      `json:"sessionTimeout"`
	PasswordPolicy PwPolicy `json:"passwordPolicy"`
	AllowAnonymous bool     `json:"allowAnonymous"`
}

func (s *AuthSettings) Section() string {
	return "auth"
}

func (s *AuthSettings) GetDefaults() Settings {
	return &AuthSettings{
		TokenExpiry:    60,
		SessionTimeout: 1440,
		PasswordPolicy: PwPolicy{
			MinLength:      8,
			RequireUpper:   true,
			RequireLower:   true,
			RequireNumber:  true,
			RequireSpecial: false,
		},
		AllowAnonymous: false,
	}
}

type PwPolicy struct {
	MinLength      int  `json:"minLength"`
	RequireUpper   bool `json:"requireUpper"`
	RequireLower   bool `json:"requireLower"`
	RequireNumber  bool `json:"requireNumber"`
	RequireSpecial bool `json:"requireSpecial"`
}

func (s *AuthSettings) Validate() error {
	if s.TokenExpiry <= 0 {
		return fmt.Errorf("tokenExpiry must be positive")
	}
	if s.SessionTimeout <= 0 {
		return fmt.Errorf("sessionTimeout must be positive")
	}
	if s.PasswordPolicy.MinLength < 4 {
		return fmt.Errorf("minimum password length must be at least 4")
	}
	return nil
}

func (s *AuthSettings) IsStartupOnly() bool {
	return false
}

func NewSettings(section string) (Settings, error) {
	switch section {
	case "artifacts":
		return &ArtifactSettings{}, nil
	case "registry":
		return &RegistrySettings{}, nil
	case "auth":
		return &AuthSettings{}, nil
	default:
		return nil, fmt.Errorf("unknown settings section: %s", section)
	}
}

// GET SETTINGS WITH DEFAULTS
func GetSettingsWithDefaults(section string) (Settings, error) {
	settings, err := NewSettings(section)
	if err != nil {
		return nil, err
	}

	// Apply defaults
	defaults := settings.GetDefaults()
	defaultsJson, err := json.Marshal(defaults)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(defaultsJson, settings); err != nil {
		return nil, err
	}

	return settings, nil
}
