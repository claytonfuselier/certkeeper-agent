package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	// Create a config and save it.
	cfg := New("https://certkeeper.example.com:3000", path)
	cfg.Deployments[1] = &DeploymentConfig{Enabled: true, PostDeploy: "./scripts/deploy.sh"}
	cfg.Deployments[5] = &DeploymentConfig{Enabled: false}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Load it back.
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.ServerURL != "https://certkeeper.example.com:3000" {
		t.Errorf("ServerURL = %q, want %q", loaded.ServerURL, "https://certkeeper.example.com:3000")
	}

	if len(loaded.Deployments) != 2 {
		t.Fatalf("len(Deployments) = %d, want 2", len(loaded.Deployments))
	}

	d1 := loaded.Deployments[1]
	if d1 == nil || !d1.Enabled || d1.PostDeploy != "./scripts/deploy.sh" {
		t.Errorf("Deployment 1: got %+v", d1)
	}

	d5 := loaded.Deployments[5]
	if d5 == nil || d5.Enabled {
		t.Errorf("Deployment 5: got %+v", d5)
	}
}

func TestLoad_MissingServerURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	// Write a config without server_url.
	if err := os.WriteFile(path, []byte("deployments: {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load() expected error for missing server_url")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.yml")
	if err == nil {
		t.Error("Load() expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	if err := os.WriteFile(path, []byte("{{{{invalid yaml"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load() expected error for invalid YAML")
	}
}

func TestConfigPaths(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	cfg := New("https://example.com", cfgPath)

	if got := cfg.Dir(); got != dir {
		t.Errorf("Dir() = %q, want %q", got, dir)
	}
	if got := cfg.ClientCertPath(); got != filepath.Join(dir, "client.crt") {
		t.Errorf("ClientCertPath() = %q, want %q", got, filepath.Join(dir, "client.crt"))
	}
	if got := cfg.ClientKeyPath(); got != filepath.Join(dir, "client.key") {
		t.Errorf("ClientKeyPath() = %q, want %q", got, filepath.Join(dir, "client.key"))
	}
	if got := cfg.CACertPath(); got != filepath.Join(dir, "ca.crt") {
		t.Errorf("CACertPath() = %q, want %q", got, filepath.Join(dir, "ca.crt"))
	}
	if got := cfg.DeploymentDir(42); got != filepath.Join(dir, "deployments", "42") {
		t.Errorf("DeploymentDir(42) = %q, want %q", got, filepath.Join(dir, "deployments", "42"))
	}
}

func TestSave_NoPath(t *testing.T) {
	cfg := &Config{
		ServerURL:   "https://example.com",
		Deployments: make(map[int]*DeploymentConfig),
	}

	err := cfg.Save()
	if err == nil {
		t.Error("Save() expected error when path is empty")
	}
}

func TestNew(t *testing.T) {
	cfg := New("https://example.com:3000", "/tmp/test/config.yml")
	if cfg.ServerURL != "https://example.com:3000" {
		t.Errorf("ServerURL = %q", cfg.ServerURL)
	}
	if cfg.Path() != "/tmp/test/config.yml" {
		t.Errorf("Path() = %q", cfg.Path())
	}
	if cfg.Deployments == nil {
		t.Error("Deployments should be initialized")
	}
}
