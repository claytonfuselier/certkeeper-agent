package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// State holds runtime state that persists across restarts (state.json).
type State struct {
	ConfigVersion     int               `json:"config_version"`
	HeartbeatInterval int               `json:"heartbeat_interval"`
	DeploymentsHash   string            `json:"deployments_hash,omitempty"`
	DeploymentHashes  map[int]string    `json:"deployment_hashes,omitempty"`

	path string
	mu   sync.Mutex
}

// DefaultHeartbeatInterval is used before the first successful heartbeat.
const DefaultHeartbeatInterval = 180

// Load reads and parses a state.json file.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No state file yet — return defaults.
			return &State{
				ConfigVersion:     0,
				HeartbeatInterval: DefaultHeartbeatInterval,
				DeploymentHashes:  make(map[int]string),
				path:              path,
			}, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	s := &State{}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}

	if s.DeploymentHashes == nil {
		s.DeploymentHashes = make(map[int]string)
	}
	if s.HeartbeatInterval <= 0 {
		s.HeartbeatInterval = DefaultHeartbeatInterval
	}

	s.path = path
	return s, nil
}

// Save writes the state to disk atomically.
func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		return fmt.Errorf("state: no file path set")
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	// Ensure directory exists.
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	// Atomic write.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("renaming state: %w", err)
	}

	return nil
}

// Path returns the current file path.
func (s *State) Path() string {
	return s.path
}

// StatePath returns the default state.json path given a config directory.
func StatePath(configDir string) string {
	return filepath.Join(configDir, "state.json")
}
