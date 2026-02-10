package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"server" json:"server"`
	Database DatabaseConfig `mapstructure:"database" json:"database"`
	Storage  StorageConfig  `mapstructure:"storage" json:"storage"`
	Logging  LoggingConfig  `mapstructure:"logging" json:"logging"`
}

type ServerConfig struct {
	Port         string `mapstructure:"port" json:"port"`
	Host         string `mapstructure:"host" json:"host"`
	ReadTimeout  int    `mapstructure:"read_timeout" json:"read_timeout"`
	WriteTimeout int    `mapstructure:"write_timeout" json:"write_timeout"`
	IdleTimeout  int    `mapstructure:"idle_timeout" json:"idle_timeout"`
}

type DatabaseConfig struct {
	Path            string `mapstructure:"path" json:"path"`
	MaxConnections  int    `mapstructure:"max_connections" json:"max_connections"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns" json:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime" json:"conn_max_lifetime"`
}

type StorageConfig struct {
	DataDir string `mapstructure:"data_dir" json:"data_dir"`
}

type LoggingConfig struct {
	Enabled    bool   `mapstructure:"enabled" json:"enabled"`
	FilePath   string `mapstructure:"file_path" json:"file_path"`
	MaxSize    int    `mapstructure:"max_size" json:"max_size"`
	MaxBackups int    `mapstructure:"max_backups" json:"max_backups"`
	MaxAge     int    `mapstructure:"max_age" json:"max_age"`
	Compress   bool   `mapstructure:"compress" json:"compress"`
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

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("config validation error: %w", err)
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", "8080")
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.read_timeout", 15)
	v.SetDefault("server.write_timeout", 15)
	v.SetDefault("server.idle_timeout", 60)

	v.SetDefault("database.path", "./data/distroface.db")
	v.SetDefault("database.max_connections", 25)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("database.conn_max_lifetime", 300)

	dataDir, err := filepath.Abs("./data")
	if err != nil {
		panic("unable to resolve data dir")
	}
	v.SetDefault("storage.data_dir", dataDir)

	v.SetDefault("logging.enabled", true)
	v.SetDefault("logging.file_path", "./data/distroface.log")
	v.SetDefault("logging.max_size", 10)
	v.SetDefault("logging.max_backups", 5)
	v.SetDefault("logging.max_age", 30)
	v.SetDefault("logging.compress", true)
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

	return nil
}
