package catalog

import (
	"sort"

	"go-agent-harness/internal/harness"
)

func ProductionDefinitions(workspaceRoot string) []harness.ToolDefinition {
	registry := harness.NewDefaultRegistryWithOptions(workspaceRoot, harness.DefaultRegistryOptions{})
	return registry.Definitions()
}

func MergeAndSelect(production []harness.ToolDefinition, suiteDefined []harness.ToolDefinition, names []string) []harness.ToolDefinition {
	byName := make(map[string]harness.ToolDefinition, len(production)+len(suiteDefined))
	for _, def := range production {
		byName[def.Name] = def.Clone()
	}
	for _, def := range suiteDefined {
		byName[def.Name] = def.Clone()
	}

	if len(names) == 0 {
		out := make([]harness.ToolDefinition, 0, len(byName))
		for _, def := range byName {
			out = append(out, def.Clone())
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}

	out := make([]harness.ToolDefinition, 0, len(names))
	for _, name := range names {
		if def, ok := byName[name]; ok {
			out = append(out, def.Clone())
		}
	}
	return out
}

func DefinitionMap(defs []harness.ToolDefinition) map[string]harness.ToolDefinition {
	out := make(map[string]harness.ToolDefinition, len(defs))
	for _, def := range defs {
		out[def.Name] = def.Clone()
	}
	return out
}
