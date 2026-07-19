package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/spf13/viper"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Boot wiring only, everything tunable lives in runtime settings.
// The settings block seeds the db once, overrides pins fields forever
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Logging   LoggingConfig   `mapstructure:"logging"`
	Registry  RegistryConfig  `mapstructure:"registry"`
	Artifacts ArtifactsConfig `mapstructure:"artifacts"`
	TLS       TLSConfig       `mapstructure:"tls"`
	Auth      AuthConfig      `mapstructure:"auth"`
	Bootstrap BootstrapConfig `mapstructure:"bootstrap"`

	// Runtime settings seeded on first boot
	Settings *v1.Settings `mapstructure:"-"`
	// Runtime settings pinned by the operator, never editable in app
	Overrides *v1.Settings `mapstructure:"-"`
	// Legacy static acme domains seeded as approved system rows
	LegacyACMEDomains []string `mapstructure:"-"`
}

type ServerConfig struct {
	Port           string   `mapstructure:"port"`
	Host           string   `mapstructure:"host"`
	ReadTimeout    int      `mapstructure:"read_timeout"`
	WriteTimeout   int      `mapstructure:"write_timeout"`
	IdleTimeout    int      `mapstructure:"idle_timeout"`
	TrustedProxies []string `mapstructure:"trusted_proxies"`
}

// On disk certificate pair served for CERT_SOURCE_CONFIG
type TLSConfig struct {
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

// Secret material only, toggles are runtime settings
type AuthConfig struct {
	JWTSecret string `mapstructure:"jwt_secret"`
}

type DatabaseConfig struct {
	Path            string `mapstructure:"path"`
	MaxConnections  int    `mapstructure:"max_connections"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
}

type StorageConfig struct {
	DataDir string `mapstructure:"data_dir"`
}

type RegistryConfig struct {
	StoragePath string `mapstructure:"storage_path"`
}

type ArtifactsConfig struct {
	StoragePath string `mapstructure:"storage_path"`
}

type LoggingConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	Dir           string `mapstructure:"dir"`
	DefaultModule string `mapstructure:"default_module"`
	MaxSize       int    `mapstructure:"max_size"`
	MaxBackups    int    `mapstructure:"max_backups"`
	MaxAge        int    `mapstructure:"max_age"`
	Compress      bool   `mapstructure:"compress"`
}

// Seeds entities at startup skipping ones that exist
type BootstrapConfig struct {
	Users []BootstrapUser `mapstructure:"users"`
	Orgs  []BootstrapOrg  `mapstructure:"orgs"`
}

type BootstrapUser struct {
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	Email    string `mapstructure:"email"`
	// System roles granted default roles when empty
	Roles []string `mapstructure:"roles"`
}

type BootstrapOrg struct {
	Name        string               `mapstructure:"name"`
	DisplayName string               `mapstructure:"display_name"`
	Description string               `mapstructure:"description"`
	Members     []BootstrapOrgMember `mapstructure:"members"`
}

type BootstrapOrgMember struct {
	Username string `mapstructure:"username"`
	// Owner admin or member defaults to member
	Role string `mapstructure:"role"`
}

type MigrateConfig struct {
	V1DB        string // Path to v1 distro.db
	V1Root      string // V1 storage root (contains blobs/, repositories/, artifacts/)
	V2DB        string // Path to v2 sqlite db (direct writes: users, orgs, visibility, webhook toggle)
	V2Artifacts string // V2 artifact storage root (config artifacts.storage_path) for blob import
	Registry    string // V2 registry host[:port] for docker API push-replay
	User        string // V2 username for registry auth
	Pass        string // V2 password or df_ API token (or V2_PASSWORD env)
	PlainHTTP   bool   // Registry is plain http (dev / behind TLS terminator)
	LegacyNS    string // Org namespace that flat v1 names are mapped into
	DryRun      bool   // Print planned actions without writing
	Jobs        int    // Concurrent repo pushes
	Verbose     bool
}

func Load(configPath string) (*Config, error) {
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if configPath != "" {
		v.AddConfigPath(configPath)
	}
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/distroface")

	setDefaults(v)

	v.SetEnvPrefix("DISTROFACE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Short alias used by the compose examples
	_ = v.BindEnv("storage.data_dir", "DISTROFACE_STORAGE_DATA_DIR", "DISTROFACE_DATA_DIR")

	// Keys without defaults need explicit env binding
	_ = v.BindEnv("database.path")
	_ = v.BindEnv("registry.storage_path")
	_ = v.BindEnv("artifacts.storage_path")
	_ = v.BindEnv("logging.dir")
	_ = v.BindEnv("auth.jwt_secret")
	_ = v.BindEnv("tls.cert_file")
	_ = v.BindEnv("tls.key_file")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	var err error
	if cfg.Settings, err = settingsBlock(v, "settings", "DISTROFACE_SETTINGS_JSON"); err != nil {
		return nil, err
	}
	if cfg.Overrides, err = settingsBlock(v, "overrides", "DISTROFACE_OVERRIDES_JSON"); err != nil {
		return nil, err
	}
	applyLegacySettings(v, &cfg)

	applyEnvBootstrapUser(&cfg)
	applyDerivedPaths(&cfg)

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("config validation error: %w", err)
	}

	return &cfg, nil
}

// Parses one yaml block or env json payload as a settings document
func settingsBlock(v *viper.Viper, key, envKey string) (*v1.Settings, error) {
	var data []byte
	if env := os.Getenv(envKey); env != "" {
		data = []byte(env)
	} else if raw := v.Get(key); raw != nil {
		var err error
		if data, err = json.Marshal(raw); err != nil {
			return nil, fmt.Errorf("invalid %s block: %w", key, err)
		}
	} else {
		return nil, nil
	}
	out := &v1.Settings{}
	if err := protojson.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("invalid %s block: %w", key, err)
	}
	return out, nil
}

// Retired yaml keys folded into the settings seed with a warning
func applyLegacySettings(v *viper.Viper, cfg *Config) {
	seed := cfg.Settings
	ensure := func() *v1.Settings {
		if seed == nil {
			seed = &v1.Settings{}
			cfg.Settings = seed
		}
		return seed
	}

	if host := v.GetString("server.hostname"); host != "" {
		s := ensure()
		if s.GetServer().GetPublicHostname() == "" {
			if s.Server == nil {
				s.Server = &v1.ServerSettings{}
			}
			s.Server.PublicHostname = proto.String(host)
		}
	}
	if v.IsSet("tls.enabled") && v.GetBool("tls.enabled") {
		s := ensure()
		if s.GetTls().GetMode() == v1.TLSMode_TLS_MODE_UNSPECIFIED {
			if s.Tls == nil {
				s.Tls = &v1.TLSSettings{}
			}
			s.Tls.Mode = v1.TLSMode_TLS_MODE_HTTPS_ONLY.Enum()
		}
	}
	if v.IsSet("tls.acme") {
		s := ensure()
		if s.Acme == nil {
			s.Acme = &v1.ACMESettings{}
		}
		a := s.Acme
		if a.Enabled == nil && v.IsSet("tls.acme.enabled") {
			a.Enabled = proto.Bool(v.GetBool("tls.acme.enabled"))
		}
		if a.Email == nil && v.GetString("tls.acme.email") != "" {
			a.Email = proto.String(v.GetString("tls.acme.email"))
		}
		if a.DirectoryUrl == nil && v.GetString("tls.acme.directory_url") != "" {
			a.DirectoryUrl = proto.String(v.GetString("tls.acme.directory_url"))
		}
		if a.ChallengePort == nil && v.IsSet("tls.acme.http_port") {
			a.ChallengePort = proto.String(v.GetString("tls.acme.http_port"))
		}
		if a.RedirectHttp == nil && v.IsSet("tls.acme.redirect_http") {
			a.RedirectHttp = proto.Bool(v.GetBool("tls.acme.redirect_http"))
		}
		cfg.LegacyACMEDomains = v.GetStringSlice("tls.acme.domains")
	}

	for _, key := range []string{
		"auth.session_timeout", "auth.token_expiry", "auth.anonymous_access",
		"auth.local", "auth.oidc", "gc", "rate_limit", "webhooks", "security",
		"artifacts.max_file_size_mb", "artifacts.v1_compat", "artifacts.retention",
		"artifacts.reaper", "artifacts.stale_upload_cleanup_hours",
	} {
		if v.IsSet(key) {
			fmt.Fprintf(os.Stderr,
				"WARNING: config key %q is now a runtime setting, move it to the settings or overrides block\n", key)
		}
	}
}

// Appends one env defined bootstrap user
func applyEnvBootstrapUser(cfg *Config) {
	username := os.Getenv("DISTROFACE_BOOTSTRAP_USERNAME")
	password := os.Getenv("DISTROFACE_BOOTSTRAP_PASSWORD")
	if username == "" || password == "" {
		return
	}
	for _, u := range cfg.Bootstrap.Users {
		if u.Username == username {
			return
		}
	}
	var roles []string
	for r := range strings.SplitSeq(os.Getenv("DISTROFACE_BOOTSTRAP_ROLES"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			roles = append(roles, r)
		}
	}
	cfg.Bootstrap.Users = append(cfg.Bootstrap.Users, BootstrapUser{
		Username: username,
		Password: password,
		Email:    os.Getenv("DISTROFACE_BOOTSTRAP_EMAIL"),
		Roles:    roles,
	})
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", "8080")
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.read_timeout", 15)
	v.SetDefault("server.write_timeout", 15)
	v.SetDefault("server.idle_timeout", 60)
	v.SetDefault("server.trusted_proxies", []string{
		"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
	})

	v.SetDefault("database.max_connections", 25)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("database.conn_max_lifetime", 300)

	v.SetDefault("storage.data_dir", "./data")

	v.SetDefault("logging.enabled", true)
	v.SetDefault("logging.default_module", "distroface-app")
	v.SetDefault("logging.max_size", 10)
	v.SetDefault("logging.max_backups", 5)
	v.SetDefault("logging.max_age", 30)
	v.SetDefault("logging.compress", true)
}

// Paths left unset land under the data dir
func applyDerivedPaths(cfg *Config) {
	if cfg.Database.Path == "" {
		cfg.Database.Path = filepath.Join(cfg.Storage.DataDir, "distroface.db")
	}
	if cfg.Registry.StoragePath == "" {
		cfg.Registry.StoragePath = filepath.Join(cfg.Storage.DataDir, "registry")
	}
	if cfg.Artifacts.StoragePath == "" {
		cfg.Artifacts.StoragePath = filepath.Join(cfg.Storage.DataDir, "artifacts")
	}
	if cfg.Logging.Dir == "" {
		cfg.Logging.Dir = filepath.Join(cfg.Storage.DataDir, "logs")
	}
}

func validateConfig(cfg *Config) error {
	var err error
	cfg.Database.Path, err = filepath.Abs(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("invalid database path: %w", err)
	}

	cfg.Storage.DataDir, err = filepath.Abs(cfg.Storage.DataDir)
	if err != nil {
		return fmt.Errorf("invalid data directory: %w", err)
	}

	cfg.Registry.StoragePath, err = filepath.Abs(cfg.Registry.StoragePath)
	if err != nil {
		return fmt.Errorf("invalid registry storage path: %w", err)
	}

	cfg.Artifacts.StoragePath, err = filepath.Abs(cfg.Artifacts.StoragePath)
	if err != nil {
		return fmt.Errorf("invalid artifact storage path: %w", err)
	}

	cfg.Logging.Dir, err = filepath.Abs(cfg.Logging.Dir)
	if err != nil {
		return fmt.Errorf("invalid logging directory: %w", err)
	}

	return nil
}
