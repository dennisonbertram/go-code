// Package plugins defines safe, installable plugin bundle metadata and layout.
// It intentionally does not replace compile-time Go plugins in the repository's
// top-level plugins directory.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const ManifestFilename = "plugin.json"

var pluginNameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var pluginVersionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// Manifest describes an installable bundle. Declared paths are relative to the
// bundle root and are checked before their contents are used.
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Description   string `json:"description"`
	Skills        string `json:"skills,omitempty"`
	Commands      string `json:"commands,omitempty"`
	Agents        string `json:"agents,omitempty"`
	Hooks         string `json:"hooks,omitempty"`
	MCP           string `json:"mcp,omitempty"`
}

// Bundle is a validated manifest together with absolute component paths.
type Bundle struct {
	Root        string
	Manifest    Manifest
	SkillsDir   string
	CommandsDir string
	AgentsDir   string
	HooksPath   string
	MCPPath     string
}

// LoadBundle parses plugin.json and validates every declared component path.
// It only reads metadata; it never executes bundle content.
func LoadBundle(root string) (*Bundle, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve bundle root: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(absRoot, ManifestFilename))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ManifestFilename, err)
	}
	var manifest Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ManifestFilename, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	bundle := &Bundle{Root: absRoot, Manifest: manifest}
	if bundle.SkillsDir, err = checkedPath(absRoot, manifest.Skills, true); err != nil {
		return nil, fmt.Errorf("skills: %w", err)
	}
	if bundle.CommandsDir, err = checkedPath(absRoot, manifest.Commands, true); err != nil {
		return nil, fmt.Errorf("commands: %w", err)
	}
	if bundle.AgentsDir, err = checkedPath(absRoot, manifest.Agents, true); err != nil {
		return nil, fmt.Errorf("agents: %w", err)
	}
	if bundle.HooksPath, err = checkedPath(absRoot, manifest.Hooks, false); err != nil {
		return nil, fmt.Errorf("hooks: %w", err)
	}
	if bundle.MCPPath, err = checkedPath(absRoot, manifest.MCP, false); err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	return bundle, nil
}

// Validate verifies the manifest independently from its filesystem layout.
func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("plugin manifest schema_version must be 1")
	}
	if !pluginNameRE.MatchString(m.Name) {
		return fmt.Errorf("plugin manifest name %q must be kebab-case", m.Name)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("plugin manifest version is required")
	}
	if !pluginVersionRE.MatchString(m.Version) || m.Version == "." || m.Version == ".." || filepath.IsAbs(m.Version) {
		return fmt.Errorf("plugin manifest version %q must be a safe path segment", m.Version)
	}
	return nil
}

func checkedPath(root, declared string, dir bool) (string, error) {
	if declared == "" {
		return "", nil
	}
	if filepath.IsAbs(declared) {
		return "", fmt.Errorf("path must be relative")
	}
	clean := filepath.Clean(declared)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes bundle root")
	}
	path := filepath.Join(root, clean)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("declared path %q: %w", declared, err)
	}
	if info.IsDir() != dir {
		if dir {
			return "", fmt.Errorf("declared path %q must be a directory", declared)
		}
		return "", fmt.Errorf("declared path %q must be a file", declared)
	}
	return path, nil
}
