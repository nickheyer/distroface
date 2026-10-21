// Package runner executes Actions jobs for dfcli
package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// Image value that selects the host executor
	HostExecutor = "host"
	// Label every fresh runner serves
	DefaultLabel = "ubuntu-latest"
	// Image behind the default label, node, git, docker cli and build tools included
	DefaultImage = "ghcr.io/catthehacker/ubuntu:act-latest"
	// Slim alternative for the images map
	SlimImage = "docker.io/library/ubuntu:24.04"
)

// On disk runner identity and executor setup
type Config struct {
	Server      string            `json:"server"`
	Token       string            `json:"token"`
	RunnerID    string            `json:"runner_id"`
	Name        string            `json:"name"`
	Scope       string            `json:"scope,omitempty"`
	Labels      []string          `json:"labels"`
	Images      map[string]string `json:"images"`
	Concurrency int               `json:"concurrency"`
	HostEnabled bool              `json:"host_enabled"`
	// Jobs may mount the docker socket, run privileged, and bind host paths
	PrivilegedJobs bool   `json:"privileged_jobs,omitempty"`
	WorkDir        string `json:"work_dir,omitempty"`
	DockerHost     string `json:"docker_host,omitempty"`
	// Rootless buildkit image behind native image builds
	BuildKitImage string `json:"buildkit_image,omitempty"`
	// Build sandbox nameservers, probed from the daemon when empty
	BuildKitDNS []string `json:"buildkit_dns,omitempty"`
	// PEM bundle buildkit trusts for the instance registry
	RegistryCA string `json:"registry_ca,omitempty"`
}

// Lives at ~/.dfcli/runner.json unless DFCLI_RUNNER_CONFIG names another file
func DefaultPath() string {
	if p := os.Getenv("DFCLI_RUNNER_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".dfcli", "runner.json")
}

// Stock label mappings
func DefaultImages() map[string]string {
	return map[string]string{DefaultLabel: DefaultImage, HostExecutor: HostExecutor}
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runner config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse runner config %s: %w", path, err)
	}
	cfg.normalize()
	return &cfg, nil
}

func (c *Config) Save(path string) error {
	if path == "" {
		path = DefaultPath()
	}
	c.normalize()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (c *Config) normalize() {
	if c.Concurrency <= 0 {
		c.Concurrency = 1
	}
	if c.Images == nil {
		c.Images = DefaultImages()
	}
	if c.Labels == nil {
		c.Labels = []string{}
	}
	c.Server = strings.TrimRight(c.Server, "/")
}

// Rejects configs that cannot start
func (c *Config) Validate() error {
	if c.Server == "" {
		return fmt.Errorf("runner config has no server, run 'dfcli runner register'")
	}
	if c.Token == "" {
		return fmt.Errorf("runner config has no token, run 'dfcli runner register'")
	}
	return nil
}

// First mapped label wins, host only when enabled
func (c *Config) ImageFor(labels []string) (image string, host bool, ok bool) {
	for _, l := range labels {
		for k, v := range c.Images {
			if !strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(l)) {
				continue
			}
			if strings.EqualFold(v, HostExecutor) {
				if c.HostEnabled {
					return "", true, true
				}
				continue
			}
			return v, false, true
		}
	}
	return "", false, false
}

// Used when no label maps, default label first
func (c *Config) FallbackImage() string {
	if v, ok := c.Images[DefaultLabel]; ok && !strings.EqualFold(v, HostExecutor) {
		return v
	}
	for _, v := range c.Images {
		if !strings.EqualFold(v, HostExecutor) {
			return v
		}
	}
	return DefaultImage
}

// Parent of every job directory, DFCLI_RUNNER_WORK_DIR when the config has none
func (c *Config) JobRoot() string {
	if c.WorkDir != "" {
		return c.WorkDir
	}
	if p := os.Getenv("DFCLI_RUNNER_WORK_DIR"); p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "dfcli-runner")
}
