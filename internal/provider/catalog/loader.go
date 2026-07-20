package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// LoadCatalog reads and validates a catalog JSON file from the given path.
func LoadCatalog(path string) (*Catalog, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil, fmt.Errorf("catalog path is required")
	}

	raw, err := os.ReadFile(trimmedPath)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}

	return LoadCatalogFromBytes(raw)
}

// LoadCatalogFromBytes parses and validates catalog JSON from raw bytes.
func LoadCatalogFromBytes(data []byte) (*Catalog, error) {
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if err := deriveProviderModels(&cat); err != nil {
		return nil, err
	}

	if err := validateCatalog(&cat); err != nil {
		return nil, err
	}

	return &cat, nil
}

func deriveProviderModels(cat *Catalog) error {
	visiting := make(map[string]bool)
	resolved := make(map[string]bool)
	var resolve func(string) error
	resolve = func(name string) error {
		if resolved[name] {
			return nil
		}
		entry, ok := cat.Providers[name]
		if !ok {
			return fmt.Errorf("provider %q: models_from source is not defined", name)
		}
		if entry.ModelsFrom == "" {
			resolved[name] = true
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("provider %q: models_from cycle", name)
		}
		visiting[name] = true
		if err := resolve(entry.ModelsFrom); err != nil {
			return fmt.Errorf("provider %q: models_from %q: %w", name, entry.ModelsFrom, err)
		}
		source, ok := cat.Providers[entry.ModelsFrom]
		if !ok {
			return fmt.Errorf("provider %q: models_from %q is not defined", name, entry.ModelsFrom)
		}
		entry.Models = cloneModels(source.Models)
		if len(entry.Aliases) == 0 {
			entry.Aliases = cloneAliases(source.Aliases)
		}
		cat.Providers[name] = entry
		visiting[name] = false
		resolved[name] = true
		return nil
	}
	for name := range cat.Providers {
		if err := resolve(name); err != nil {
			return err
		}
	}
	return nil
}

func cloneModels(models map[string]Model) map[string]Model {
	cloned := make(map[string]Model, len(models))
	for name, model := range models {
		cloned[name] = cloneModel(model)
	}
	return cloned
}

func cloneAliases(aliases map[string]string) map[string]string {
	if len(aliases) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(aliases))
	for alias, target := range aliases {
		cloned[alias] = target
	}
	return cloned
}

func validateCatalog(cat *Catalog) error {
	if strings.TrimSpace(cat.CatalogVersion) == "" {
		return fmt.Errorf("catalog_version is required")
	}
	if len(cat.Providers) == 0 {
		return fmt.Errorf("catalog must have at least one provider")
	}
	for name, p := range cat.Providers {
		if strings.TrimSpace(p.BaseURL) == "" {
			return fmt.Errorf("provider %q: base_url is required", name)
		}
		if strings.TrimSpace(p.APIKeyEnv) == "" && !p.APIKeyOptional {
			return fmt.Errorf("provider %q: api_key_env is required", name)
		}
		if len(p.Models) == 0 {
			return fmt.Errorf("provider %q: must have at least one model", name)
		}
		for modelName, m := range p.Models {
			if m.ContextWindow <= 0 {
				return fmt.Errorf("provider %q model %q: context_window must be > 0", name, modelName)
			}
		}
	}
	return nil
}
