package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// InstalledPlugin is durable lifecycle state. Enabled controls visibility;
// Trusted independently controls executable surfaces such as hooks and MCP.
type InstalledPlugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
	Remote  bool   `json:"remote"`
	Enabled bool   `json:"enabled"`
	Trusted bool   `json:"trusted"`
}

type pluginState struct {
	Plugins map[string]InstalledPlugin `json:"plugins"`
}

// StateStore stores lifecycle state in one user-owned JSON file.
type StateStore struct {
	path string
	mu   sync.Mutex
}

func NewStateStore(path string) *StateStore { return &StateStore{path: path} }

func (s *StateStore) RecordInstall(plugin InstalledPlugin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if strings.TrimSpace(plugin.Name) == "" || strings.TrimSpace(plugin.Version) == "" {
		return fmt.Errorf("plugin name and version are required")
	}
	plugin.Enabled = true
	// Remote content crosses a trust boundary. Local installs remain trusted
	// unless a caller explicitly records otherwise through SetTrusted.
	plugin.Trusted = !plugin.Remote
	state.Plugins[plugin.Name] = plugin
	return s.save(state)
}

func (s *StateStore) List() ([]InstalledPlugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return nil, err
	}
	result := make([]InstalledPlugin, 0, len(state.Plugins))
	for _, plugin := range state.Plugins {
		result = append(result, plugin)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (s *StateStore) SetEnabled(name string, enabled bool) error {
	return s.update(name, func(plugin *InstalledPlugin) { plugin.Enabled = enabled })
}

func (s *StateStore) SetTrusted(name string, trusted bool) error {
	return s.update(name, func(plugin *InstalledPlugin) { plugin.Trusted = trusted })
}

func (s *StateStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := state.Plugins[name]; !ok {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	delete(state.Plugins, name)
	return s.save(state)
}

func (s *StateStore) update(name string, mutate func(*InstalledPlugin)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	plugin, ok := state.Plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	mutate(&plugin)
	state.Plugins[name] = plugin
	return s.save(state)
}

func (s *StateStore) load() (pluginState, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return pluginState{Plugins: make(map[string]InstalledPlugin)}, nil
	}
	if err != nil {
		return pluginState{}, fmt.Errorf("read plugin state: %w", err)
	}
	var state pluginState
	if err := json.Unmarshal(data, &state); err != nil {
		return pluginState{}, fmt.Errorf("parse plugin state: %w", err)
	}
	if state.Plugins == nil {
		state.Plugins = make(map[string]InstalledPlugin)
	}
	return state, nil
}

func (s *StateStore) save(state pluginState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create plugin state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plugin state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".plugins-state-")
	if err != nil {
		return fmt.Errorf("create plugin state temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write plugin state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace plugin state: %w", err)
	}
	return nil
}
