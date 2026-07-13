package systemprompt

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	catalogFilename = "catalog.yaml"
	basePromptPath  = "base/main.md"
)

type catalog struct {
	Version       int                   `yaml:"version"`
	Defaults      catalogDefaults       `yaml:"defaults"`
	Intents       map[string]string     `yaml:"intents"`
	ModelProfiles []catalogModelProfile `yaml:"model_profiles"`
	Extensions    catalogExtensions     `yaml:"extensions"`
}

type catalogDefaults struct {
	Intent       string `yaml:"intent"`
	ModelProfile string `yaml:"model_profile"`
}

type catalogModelProfile struct {
	Name  string `yaml:"name"`
	Match string `yaml:"match"`
	File  string `yaml:"file"`
}

type catalogExtensions struct {
	BehaviorsDir string `yaml:"behaviors_dir"`
	TalentsDir   string `yaml:"talents_dir"`
}

type compiledModelProfile struct {
	Name    string
	Match   string
	Content string
}

type FileEngine struct {
	rootDir      string
	behaviorsDir string
	talentsDir   string

	defaults struct {
		intent       string
		modelProfile string
	}

	basePrompt string
	intents    map[string]string

	modelProfiles  []compiledModelProfile
	profileByName  map[string]compiledModelProfile
	behaviorByID   map[string]string
	talentByID     map[string]string
	skillResolver  SkillResolver
	profileOrder   []string
	intentKeys     []string
	behaviorKeys   []string
	talentKeys     []string
}

// ExtensionDirs returns the absolute paths to the behaviors and talents directories.
func (e *FileEngine) ExtensionDirs() (behaviorsDir, talentsDir string) {
	return e.behaviorsDir, e.talentsDir
}

// SetSkillResolver configures a skill resolver for resolving skill extensions.
func (e *FileEngine) SetSkillResolver(r SkillResolver) {
	e.skillResolver = r
}

func NewFileEngine(rootDir string) (*FileEngine, error) {
	trimmedRoot := strings.TrimSpace(rootDir)
	if trimmedRoot == "" {
		return nil, invalid("prompts_root", rootDir, "path is required")
	}

	cfg, err := loadCatalog(filepath.Join(trimmedRoot, catalogFilename))
	if err != nil {
		return nil, err
	}
	if err := validateCatalog(cfg); err != nil {
		return nil, err
	}

	basePrompt, err := loadMarkdownFile(filepath.Join(trimmedRoot, basePromptPath))
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", basePromptPath, err)
	}

	intents := make(map[string]string, len(cfg.Intents))
	intentKeys := make([]string, 0, len(cfg.Intents))
	for name, rel := range cfg.Intents {
		content, err := loadMarkdownFile(filepath.Join(trimmedRoot, rel))
		if err != nil {
			return nil, fmt.Errorf("load intent %q (%s): %w", name, rel, err)
		}
		intents[name] = content
		intentKeys = append(intentKeys, name)
	}
	sort.Strings(intentKeys)

	profiles := make([]compiledModelProfile, 0, len(cfg.ModelProfiles))
	profileByName := make(map[string]compiledModelProfile, len(cfg.ModelProfiles))
	profileOrder := make([]string, 0, len(cfg.ModelProfiles))
	for _, entry := range cfg.ModelProfiles {
		content, err := loadMarkdownFile(filepath.Join(trimmedRoot, entry.File))
		if err != nil {
			return nil, fmt.Errorf("load model profile %q (%s): %w", entry.Name, entry.File, err)
		}
		compiled := compiledModelProfile{Name: entry.Name, Match: entry.Match, Content: content}
		profiles = append(profiles, compiled)
		profileByName[entry.Name] = compiled
		profileOrder = append(profileOrder, entry.Name)
	}

	resolvedBehaviorsDir := filepath.Join(trimmedRoot, cfg.Extensions.BehaviorsDir)
	behaviorByID, behaviorKeys, err := loadExtensionDirectory(resolvedBehaviorsDir)
	if err != nil {
		return nil, fmt.Errorf("load behaviors directory %q: %w", cfg.Extensions.BehaviorsDir, err)
	}
	resolvedTalentsDir := filepath.Join(trimmedRoot, cfg.Extensions.TalentsDir)
	talentByID, talentKeys, err := loadExtensionDirectory(resolvedTalentsDir)
	if err != nil {
		return nil, fmt.Errorf("load talents directory %q: %w", cfg.Extensions.TalentsDir, err)
	}

	engine := &FileEngine{
		rootDir:       trimmedRoot,
		behaviorsDir:  resolvedBehaviorsDir,
		talentsDir:    resolvedTalentsDir,
		basePrompt:    basePrompt,
		intents:       intents,
		modelProfiles: profiles,
		profileByName: profileByName,
		behaviorByID:  behaviorByID,
		talentByID:    talentByID,
		profileOrder:  profileOrder,
		intentKeys:    intentKeys,
		behaviorKeys:  behaviorKeys,
		talentKeys:    talentKeys,
	}
	engine.defaults.intent = cfg.Defaults.Intent
	engine.defaults.modelProfile = cfg.Defaults.ModelProfile
	return engine, nil
}

func loadCatalog(path string) (catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return catalog{}, err
	}
	var cfg catalog
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return catalog{}, fmt.Errorf("parse yaml: %w", err)
	}
	return cfg, nil
}

func validateCatalog(cfg catalog) error {
	if cfg.Version != 1 {
		return invalid("catalog.version", fmt.Sprintf("%d", cfg.Version), "expected version 1")
	}
	if isBlank(cfg.Defaults.ModelProfile) {
		return invalid("catalog.defaults.model_profile", "", "value is required")
	}
	if len(cfg.Intents) == 0 {
		return invalid("catalog.intents", "", "at least one intent is required")
	}
	// A blank default intent is allowed and means "base prompt only, no overlay".
	// A non-blank default must name a real intent.
	if !isBlank(cfg.Defaults.Intent) {
		if _, ok := cfg.Intents[cfg.Defaults.Intent]; !ok {
			return invalid("catalog.defaults.intent", cfg.Defaults.Intent, "intent not found in catalog.intents")
		}
	}
	if len(cfg.ModelProfiles) == 0 {
		return invalid("catalog.model_profiles", "", "at least one model profile is required")
	}
	profileNames := make(map[string]struct{}, len(cfg.ModelProfiles))
	for i, profile := range cfg.ModelProfiles {
		name := strings.TrimSpace(profile.Name)
		if name == "" {
			return invalid(fmt.Sprintf("catalog.model_profiles[%d].name", i), "", "value is required")
		}
		if _, exists := profileNames[name]; exists {
			return invalid("catalog.model_profiles.name", name, "duplicate profile name")
		}
		profileNames[name] = struct{}{}
		if isBlank(profile.Match) {
			return invalid(fmt.Sprintf("catalog.model_profiles[%d].match", i), "", "value is required")
		}
		if isBlank(profile.File) {
			return invalid(fmt.Sprintf("catalog.model_profiles[%d].file", i), "", "value is required")
		}
	}
	if _, ok := profileNames[cfg.Defaults.ModelProfile]; !ok {
		return invalid("catalog.defaults.model_profile", cfg.Defaults.ModelProfile, "profile not found in model_profiles")
	}
	if isBlank(cfg.Extensions.BehaviorsDir) {
		return invalid("catalog.extensions.behaviors_dir", "", "value is required")
	}
	if isBlank(cfg.Extensions.TalentsDir) {
		return invalid("catalog.extensions.talents_dir", "", "value is required")
	}
	return nil
}

func loadMarkdownFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func loadExtensionDirectory(dir string) (map[string]string, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	result := make(map[string]string)
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" {
			continue
		}
		id := strings.TrimSuffix(name, ext)
		content, err := loadMarkdownFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, err
		}
		result[id] = content
		keys = append(keys, id)
	}
	sort.Strings(keys)
	return result, keys, nil
}
