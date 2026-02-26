package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Load from non-existent file should return defaults.
	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error on new file: %v", err)
	}

	if st.ConfigVersion != 0 {
		t.Errorf("ConfigVersion = %d, want 0", st.ConfigVersion)
	}
	if st.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Errorf("HeartbeatInterval = %d, want %d", st.HeartbeatInterval, DefaultHeartbeatInterval)
	}
	if st.DeploymentHashes == nil {
		t.Error("DeploymentHashes should be initialized")
	}

	// Modify and save.
	st.ConfigVersion = 5
	st.HeartbeatInterval = 120
	st.DeploymentsHash = "abc123"
	st.DeploymentHashes[1] = "hash1"
	st.DeploymentHashes[7] = "hash7"

	if err := st.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Reload and verify.
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error after save: %v", err)
	}

	if loaded.ConfigVersion != 5 {
		t.Errorf("ConfigVersion = %d, want 5", loaded.ConfigVersion)
	}
	if loaded.HeartbeatInterval != 120 {
		t.Errorf("HeartbeatInterval = %d, want 120", loaded.HeartbeatInterval)
	}
	if loaded.DeploymentsHash != "abc123" {
		t.Errorf("DeploymentsHash = %q, want %q", loaded.DeploymentsHash, "abc123")
	}
	if loaded.DeploymentHashes[1] != "hash1" {
		t.Errorf("DeploymentHashes[1] = %q, want %q", loaded.DeploymentHashes[1], "hash1")
	}
	if loaded.DeploymentHashes[7] != "hash7" {
		t.Errorf("DeploymentHashes[7] = %q, want %q", loaded.DeploymentHashes[7], "hash7")
	}
}

func TestLoad_DefaultHeartbeatInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Write state with heartbeat_interval=0 (should be replaced with default).
	data, _ := json.Marshal(map[string]interface{}{
		"config_version":     2,
		"heartbeat_interval": 0,
	})
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if st.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Errorf("HeartbeatInterval = %d, want %d (default)", st.HeartbeatInterval, DefaultHeartbeatInterval)
	}
	if st.ConfigVersion != 2 {
		t.Errorf("ConfigVersion = %d, want 2", st.ConfigVersion)
	}
}

func TestLoad_NilDeploymentHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Write state without deployment_hashes key.
	data, _ := json.Marshal(map[string]interface{}{
		"config_version":     1,
		"heartbeat_interval": 60,
	})
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if st.DeploymentHashes == nil {
		t.Error("DeploymentHashes should be initialized even when missing from JSON")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := os.WriteFile(path, []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load() expected error for invalid JSON")
	}
}

func TestSave_NoPath(t *testing.T) {
	st := &State{
		DeploymentHashes: make(map[int]string),
	}

	err := st.Save()
	if err == nil {
		t.Error("Save() expected error when path is empty")
	}
}

func TestStatePath(t *testing.T) {
	got := StatePath("/etc/ceagent")
	want := filepath.Join("/etc/ceagent", "state.json")
	if got != want {
		t.Errorf("StatePath() = %q, want %q", got, want)
	}
}
