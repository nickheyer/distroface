package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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
	Git       GitConfig       `mapstructure:"git"`
	TLS       TLSConfig       `mapstructure:"tls"`
	Auth      AuthConfig      `mapstructure:"auth"`
	Bootstrap BootstrapConfig `mapstructure:"bootstrap"`
	// Shared cache behind every instance, empty keeps process memory
	Redis RedisConfig `mapstructure:"redis"`

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
	// Key behind stored secrets, generated into the data dir when unset
	SealingKey string `mapstructure:"sealing_key"`
}

type DatabaseConfig struct {
	Driver          string `mapstructure:"driver"` // Supported: sqlite, postgres
	Path            string `mapstructure:"path"`
	DSN             string `mapstructure:"dsn"`
	MaxConnections  int    `mapstructure:"max_connections"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
}

const (
	DatabaseSQLite   = "sqlite"
	DatabasePostgres = "postgres"
)

// Normalizes the driver name and fills dsn for sqlite
func (d *DatabaseConfig) resolve() error {
	switch strings.ToLower(strings.TrimSpace(d.Driver)) {
	case "":
		d.Driver = DatabaseSQLite
		if dsnLooksLikePostgres(d.DSN) {
			d.Driver = DatabasePostgres
		}
	case "sqlite", "sqlite3":
		d.Driver = DatabaseSQLite
	case "postgres", "postgresql", "pg", "pgx":
		d.Driver = DatabasePostgres
	default:
		return fmt.Errorf("unsupported database.driver %q, want sqlite or postgres", d.Driver)
	}
	if d.Driver == DatabaseSQLite {
		if d.DSN == "" {
			d.DSN = d.Path
		}
		return nil
	}
	if d.DSN == "" {
		return fmt.Errorf("database.dsn is required when database.driver is %s", d.Driver)
	}
	return nil
}

func dsnLooksLikePostgres(dsn string) bool {
	lower := strings.ToLower(strings.TrimSpace(dsn))
	return strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") ||
		strings.Contains(lower, "host=") || strings.Contains(lower, "dbname=")
}

// Connection string safe for logs, credentials blanked
func (d *DatabaseConfig) Redacted() string {
	if d.Driver == DatabaseSQLite {
		return d.Path
	}
	if u, err := url.Parse(d.DSN); err == nil && u.Host != "" {
		if u.User != nil {
			u.User = url.User(u.User.Username())
		}
		u.RawQuery = redactQuery(u.Query())
		return u.Redacted()
	}
	return passwordKV.ReplaceAllString(d.DSN, "${1}=***")
}

// Libpq also accepts the password as a url parameter
func redactQuery(q url.Values) string {
	for key := range q {
		if passwordKey.MatchString(key) {
			q.Set(key, "***")
		}
	}
	return q.Encode()
}

var (
	passwordKV  = regexp.MustCompile(`(?i)\b(password|passfile)=\S+`)
	passwordKey = regexp.MustCompile(`(?i)^(password|passfile)$`)
)

type StorageConfig struct {
	DataDir string `mapstructure:"data_dir"`
}

type RegistryConfig struct {
	StoragePath          string        `mapstructure:"storage_path"`
	BlobCacheSize        int           `mapstructure:"blob_cache_size"`
	ManifestURLsAllow    []string      `mapstructure:"manifest_urls_allow"`
	ManifestURLsDeny     []string      `mapstructure:"manifest_urls_deny"`
	IndexPlatforms       string        `mapstructure:"index_platforms"`
	IndexPlatformList    []string      `mapstructure:"index_platform_list"`
	UploadPurgeAge       time.Duration `mapstructure:"upload_purge_age"`
	UploadPurgeInterval  time.Duration `mapstructure:"upload_purge_interval"`
	TagLookupConcurrency int           `mapstructure:"tag_lookup_concurrency"`
	// How much of each registry write waits for the disk
	Fsync string `mapstructure:"fsync"`
}

// Registry write durability modes, see the localfs storage driver
const (
	FsyncAlways = "always"
	FsyncCommit = "commit"
	FsyncNever  = "never"
)

// Checks registry knobs
func (r *RegistryConfig) Normalize() error {
	if r.BlobCacheSize < 0 {
		return errors.New("registry.blob_cache_size must not be negative")
	}
	for _, expr := range slices.Concat(r.ManifestURLsAllow, r.ManifestURLsDeny) {
		if _, err := regexp.Compile(expr); err != nil {
			return fmt.Errorf("registry manifest url pattern %q: %w", expr, err)
		}
	}
	r.IndexPlatforms = strings.ToLower(strings.TrimSpace(r.IndexPlatforms))
	switch r.IndexPlatforms {
	case "", "all", "none":
		if len(r.IndexPlatformList) > 0 {
			return errors.New("registry.index_platform_list needs index_platforms set to list")
		}
	case "list":
		if len(r.IndexPlatformList) == 0 {
			return errors.New("registry.index_platforms list needs at least one index_platform_list entry")
		}
		for _, entry := range r.IndexPlatformList {
			osName, arch, ok := strings.Cut(entry, "/")
			if !ok || osName == "" || arch == "" {
				return fmt.Errorf("registry.index_platform_list entry %q must look like os/arch", entry)
			}
		}
	default:
		return fmt.Errorf("registry.index_platforms must be all, none or list, got %q", r.IndexPlatforms)
	}
	if r.UploadPurgeAge < 0 || r.UploadPurgeInterval < 0 {
		return errors.New("registry upload purge durations must not be negative")
	}
	if r.TagLookupConcurrency < 0 {
		return errors.New("registry.tag_lookup_concurrency must not be negative")
	}
	r.Fsync = strings.ToLower(strings.TrimSpace(r.Fsync))
	switch r.Fsync {
	case "":
		r.Fsync = FsyncCommit
	case FsyncAlways, FsyncCommit, FsyncNever:
	default:
		return fmt.Errorf("registry.fsync must be always, commit or never, got %q", r.Fsync)
	}
	return nil
}

// Connection wiring for the optional shared redis cache
type RedisConfig struct {
	URL      string   `mapstructure:"url"`
	Addrs    []string `mapstructure:"addrs"`
	Username string   `mapstructure:"username"`
	Password string   `mapstructure:"password"`
	DB       int      `mapstructure:"db"`
	// Resp version, zero keeps the client default of three
	Protocol         int           `mapstructure:"protocol"`
	MasterName       string        `mapstructure:"master_name"`
	SentinelUsername string        `mapstructure:"sentinel_username"`
	SentinelPassword string        `mapstructure:"sentinel_password"`
	TLS              bool          `mapstructure:"tls"`
	TLSCAFile        string        `mapstructure:"tls_ca_file"`
	TLSCertFile      string        `mapstructure:"tls_cert_file"`
	TLSKeyFile       string        `mapstructure:"tls_key_file"`
	MaxRetries       int           `mapstructure:"max_retries"`
	DialTimeout      time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout      time.Duration `mapstructure:"read_timeout"`
	WriteTimeout     time.Duration `mapstructure:"write_timeout"`
	PoolTimeout      time.Duration `mapstructure:"pool_timeout"`
	PoolSize         int           `mapstructure:"pool_size"`
	MinIdleConns     int           `mapstructure:"min_idle_conns"`
	MaxIdleConns     int           `mapstructure:"max_idle_conns"`
	MaxActiveConns   int           `mapstructure:"max_active_conns"`
	ConnMaxIdleTime  time.Duration `mapstructure:"conn_max_idle_time"`
	ConnMaxLifetime  time.Duration `mapstructure:"conn_max_lifetime"`
	// Cluster read routing
	RouteByLatency bool `mapstructure:"route_by_latency"`
	RouteRandomly  bool `mapstructure:"route_randomly"`
}

// Reports whether a redis cache was configured
func (r *RedisConfig) Enabled() bool {
	return len(r.Addrs) > 0
}

// Splits comma joined addresses and consumes the url
func (r *RedisConfig) resolve() error {
	var addrs []string
	for _, raw := range r.Addrs {
		for part := range strings.SplitSeq(raw, ",") {
			if part = strings.TrimSpace(part); part != "" {
				addrs = append(addrs, part)
			}
		}
	}
	r.Addrs = addrs
	r.URL = strings.TrimSpace(r.URL)
	if r.URL != "" {
		if len(r.Addrs) > 0 {
			return errors.New("redis.url and redis.addrs are exclusive, set one")
		}
		if err := r.applyURL(); err != nil {
			return err
		}
		r.URL = ""
	}
	if !r.Enabled() {
		return nil
	}
	if r.DB < 0 {
		return errors.New("redis.db must not be negative")
	}
	if (r.TLSCertFile == "") != (r.TLSKeyFile == "") {
		return errors.New("redis.tls_cert_file and redis.tls_key_file go together")
	}
	if r.TLSCAFile != "" || r.TLSCertFile != "" {
		r.TLS = true
	}
	return nil
}

func (r *RedisConfig) applyURL() error {
	parsed, err := redis.ParseURL(r.URL)
	if err != nil {
		return fmt.Errorf("invalid redis.url: %w", err)
	}
	if parsed.Network != "tcp" {
		return errors.New("redis.url must start with redis:// or rediss://")
	}
	r.Addrs = []string{parsed.Addr}
	r.TLS = r.TLS || parsed.TLSConfig != nil
	r.Username = cmp.Or(r.Username, parsed.Username)
	r.Password = cmp.Or(r.Password, parsed.Password)
	r.DB = cmp.Or(r.DB, parsed.DB)
	r.Protocol = cmp.Or(r.Protocol, parsed.Protocol)
	r.MaxRetries = cmp.Or(r.MaxRetries, parsed.MaxRetries)
	r.DialTimeout = cmp.Or(r.DialTimeout, parsed.DialTimeout)
	r.ReadTimeout = cmp.Or(r.ReadTimeout, parsed.ReadTimeout)
	r.WriteTimeout = cmp.Or(r.WriteTimeout, parsed.WriteTimeout)
	r.PoolTimeout = cmp.Or(r.PoolTimeout, parsed.PoolTimeout)
	r.PoolSize = cmp.Or(r.PoolSize, parsed.PoolSize)
	r.MinIdleConns = cmp.Or(r.MinIdleConns, parsed.MinIdleConns)
	r.MaxIdleConns = cmp.Or(r.MaxIdleConns, parsed.MaxIdleConns)
	r.MaxActiveConns = cmp.Or(r.MaxActiveConns, parsed.MaxActiveConns)
	r.ConnMaxIdleTime = cmp.Or(r.ConnMaxIdleTime, parsed.ConnMaxIdleTime)
	r.ConnMaxLifetime = cmp.Or(r.ConnMaxLifetime, parsed.ConnMaxLifetime)
	return nil
}

// Connection summary safe for logs, credentials left out
func (r *RedisConfig) Redacted() string {
	if !r.Enabled() {
		return "disabled"
	}
	var b strings.Builder
	if r.TLS {
		b.WriteString("rediss://")
	} else {
		b.WriteString("redis://")
	}
	if r.Username != "" {
		b.WriteString(r.Username)
		b.WriteString("@")
	}
	b.WriteString(strings.Join(r.Addrs, ","))
	if r.DB != 0 {
		b.WriteString("/")
		b.WriteString(strconv.Itoa(r.DB))
	}
	if r.MasterName != "" {
		b.WriteString(" master=")
		b.WriteString(r.MasterName)
	}
	return b.String()
}

type ArtifactsConfig struct {
	StoragePath string `mapstructure:"storage_path"`
}

type GitConfig struct {
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
	Username string   `mapstructure:"username"`
	Password string   `mapstructure:"password"`
	Email    string   `mapstructure:"email"`
	Roles    []string `mapstructure:"roles"`
}

type BootstrapOrg struct {
	Name        string               `mapstructure:"name"`
	DisplayName string               `mapstructure:"display_name"`
	Description string               `mapstructure:"description"`
	Members     []BootstrapOrgMember `mapstructure:"members"`
}

type BootstrapOrgMember struct {
	Username string `mapstructure:"username"`
	Role     string `mapstructure:"role"`
}

type MigrateConfig struct {
	V1DB        string // Path to v1 distro.db
	V1Root      string // V1 storage root (contains blobs/, repositories/, artifacts/)
	V2DB        string // V2 sqlite path or postgres dsn (direct writes: users, orgs, visibility, webhook toggle)
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

// Schema behind the settings and overrides blocks
var settingsDescriptor = (&v1.Settings{}).ProtoReflect().Descriptor()

func Load(configPath string) (*Config, error) {
	// Struct fields are the key set, env included
	v := viper.NewWithOptions(viper.ExperimentalBindStruct())

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

	// Viper cannot walk a proto, register its leaves
	bindMessageKeys(v, "settings.", settingsDescriptor)
	bindMessageKeys(v, "overrides.", settingsDescriptor)

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
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

// Env json payload replaces the whole block
func settingsBlock(v *viper.Viper, key, envKey string) (*v1.Settings, error) {
	var data []byte
	if env := os.Getenv(envKey); env != "" {
		data = []byte(env)
	} else if raw, ok := v.AllSettings()[key]; ok {
		var err error
		if data, err = json.Marshal(coerceMessage(settingsDescriptor, raw)); err != nil {
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

// Registers scalar leaves so viper enumerates them
func bindMessageKeys(v *viper.Viper, prefix string, md protoreflect.MessageDescriptor) {
	fields := md.Fields()
	for i := range fields.Len() {
		f := fields.Get(i)
		key := prefix + string(f.Name())
		switch {
		case f.IsMap():
		case f.Kind() == protoreflect.MessageKind:
			bindMessageKeys(v, key+".", f.Message())
		default:
			_ = v.BindEnv(key)
		}
	}
}

// Env strings become json bools and lists
func coerceMessage(md protoreflect.MessageDescriptor, raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	for name, val := range m {
		f := md.Fields().ByName(protoreflect.Name(name))
		if f == nil {
			continue
		}
		str, isString := val.(string)
		switch {
		case f.IsMap():
		case f.Kind() == protoreflect.MessageKind:
			m[name] = coerceMessage(f.Message(), val)
		case f.IsList() && isString:
			m[name] = splitList(str)
		case f.Kind() == protoreflect.BoolKind && isString:
			if b, err := strconv.ParseBool(str); err == nil {
				m[name] = b
			}
		}
	}
	return m
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
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
	if cfg.Git.StoragePath == "" {
		cfg.Git.StoragePath = filepath.Join(cfg.Storage.DataDir, "git")
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
	if err := cfg.Database.resolve(); err != nil {
		return err
	}

	cfg.Storage.DataDir, err = filepath.Abs(cfg.Storage.DataDir)
	if err != nil {
		return fmt.Errorf("invalid data directory: %w", err)
	}

	cfg.Registry.StoragePath, err = filepath.Abs(cfg.Registry.StoragePath)
	if err != nil {
		return fmt.Errorf("invalid registry storage path: %w", err)
	}
	if err := cfg.Registry.Normalize(); err != nil {
		return err
	}
	if err := cfg.Redis.resolve(); err != nil {
		return err
	}

	cfg.Artifacts.StoragePath, err = filepath.Abs(cfg.Artifacts.StoragePath)
	if err != nil {
		return fmt.Errorf("invalid artifact storage path: %w", err)
	}

	cfg.Git.StoragePath, err = filepath.Abs(cfg.Git.StoragePath)
	if err != nil {
		return fmt.Errorf("invalid git storage path: %w", err)
	}

	cfg.Logging.Dir, err = filepath.Abs(cfg.Logging.Dir)
	if err != nil {
		return fmt.Errorf("invalid logging directory: %w", err)
	}

	return nil
}
