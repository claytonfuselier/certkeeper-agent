package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// DeploymentConfig holds per-deployment user configuration.
type DeploymentConfig struct {
	Enabled    bool   `yaml:"enabled"`
	PostDeploy string `yaml:"post_deploy,omitempty"`
}

// Config represents the agent's persistent configuration (config.yml).
type Config struct {
	ServerURL   string                       `yaml:"server_url"`
	Deployments map[int]*DeploymentConfig    `yaml:"deployments,omitempty"`

	// path is the file path this config was loaded from (not serialized).
	path string
}

// DefaultConfigDir returns the platform-specific default config directory.
func DefaultConfigDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "ceagent")
	}
	return "/etc/ceagent"
}

// DefaultConfigPath returns the default config.yml path.
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "config.yml")
}

// Load reads and parses a config.yml file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := &Config{
		Deployments: make(map[int]*DeploymentConfig),
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("config: server_url is required")
	}

	cfg.path = path
	return cfg, nil
}

// Save writes the config back to disk.
func (c *Config) Save() error {
	if c.path == "" {
		return fmt.Errorf("config: no file path set")
	}

	return c.saveTo(c.path)
}

func (c *Config) saveTo(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Ensure directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	// Atomic write: write to temp file, then rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("renaming config: %w", err)
	}

	c.path = path
	return nil
}

// Path returns the file path this config was loaded from / saved to.
func (c *Config) Path() string {
	return c.path
}

// Dir returns the directory containing the config file.
func (c *Config) Dir() string {
	if c.path == "" {
		return DefaultConfigDir()
	}
	return filepath.Dir(c.path)
}

// DeploymentsDir returns the path to the deployments directory.
func (c *Config) DeploymentsDir() string {
	return filepath.Join(c.Dir(), "deployments")
}

// DeploymentDir returns the path for a specific deployment's cert files.
func (c *Config) DeploymentDir(id int) string {
	return filepath.Join(c.DeploymentsDir(), fmt.Sprintf("%d", id))
}

// ClientCertPath returns the path to the agent's mTLS certificate.
func (c *Config) ClientCertPath() string {
	return filepath.Join(c.Dir(), "client.crt")
}

// ClientKeyPath returns the path to the agent's private key.
func (c *Config) ClientKeyPath() string {
	return filepath.Join(c.Dir(), "client.key")
}

// CACertPath returns the path to the CA certificate.
func (c *Config) CACertPath() string {
	return filepath.Join(c.Dir(), "ca.crt")
}

// New creates a new Config with the given server URL and sets the path.
func New(serverURL, path string) *Config {
	return &Config{
		ServerURL:   serverURL,
		Deployments: make(map[int]*DeploymentConfig),
		path:        path,
	}
}
