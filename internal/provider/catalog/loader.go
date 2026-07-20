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
	for name, entry := range cat.Providers {
		if entry.ModelsFrom == "" {
			continue
		}
		source, ok := cat.Providers[entry.ModelsFrom]
		if !ok {
			return nil, fmt.Errorf("provider %q: models_from %q not found", name, entry.ModelsFrom)
		}
		entry.Models = cloneModels(source.Models)
		entry.Aliases = cloneAliases(source.Aliases)
		cat.Providers[name] = entry
	}

	if err := validateCatalog(&cat); err != nil {
		return nil, err
	}

	return &cat, nil
}

func cloneModels(models map[string]Model) map[string]Model {
	out := make(map[string]Model, len(models))
	for name, model := range models {
		out[name] = model
	}
	return out
}

func cloneAliases(aliases map[string]string) map[string]string {
	if aliases == nil {
		return nil
	}
	out := make(map[string]string, len(aliases))
	for from, to := range aliases {
		out[from] = to
	}
	return out
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
