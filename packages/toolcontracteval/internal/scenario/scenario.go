package scenario

import (
	"encoding/json"
	"fmt"
	"os"

	"go-agent-harness/internal/harness"
)

type Suite struct {
	ID          string                   `json:"id"`
	Description string                   `json:"description,omitempty"`
	MaxTurns    int                      `json:"max_turns,omitempty"`
	Tools       []harness.ToolDefinition `json:"tools,omitempty"`
	Scenarios   []Scenario               `json:"scenarios"`
}

type Scenario struct {
	ID                          string            `json:"id"`
	Prompt                      string            `json:"prompt"`
	ToolNames                   []string          `json:"tool_names,omitempty"`
	RequiredTools               []string          `json:"required_tools,omitempty"`
	ForbiddenTools              []string          `json:"forbidden_tools,omitempty"`
	ForbiddenArgumentSubstrings []string          `json:"forbidden_argument_substrings,omitempty"`
	MinToolCalls                int               `json:"min_tool_calls,omitempty"`
	MaxToolCalls                int               `json:"max_tool_calls,omitempty"`
	MaxTurns                    int               `json:"max_turns,omitempty"`
	WorkspaceFiles              map[string]string `json:"workspace_files,omitempty"`
	Expectations                []Expectation     `json:"expectations,omitempty"`
	WorkspaceExpectations       []FileExpectation `json:"workspace_expectations,omitempty"`
	SuccessSignals              []string          `json:"success_signals,omitempty"`
	ForbiddenSuccessSignals     []string          `json:"forbidden_success_signals,omitempty"`
}

type Expectation struct {
	Tool          string         `json:"tool"`
	RequiredKeys  []string       `json:"required_keys,omitempty"`
	ForbiddenKeys []string       `json:"forbidden_keys,omitempty"`
	ExactArgs     map[string]any `json:"exact_args,omitempty"`
	AnyOf         []Expectation  `json:"any_of,omitempty"`
}

type FileExpectation struct {
	Path           string   `json:"path"`
	Exists         *bool    `json:"exists,omitempty"`
	Equals         string   `json:"equals,omitempty"`
	MustContain    []string `json:"must_contain,omitempty"`
	MustNotContain []string `json:"must_not_contain,omitempty"`
}

func Load(path string) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		return nil, err
	}
	if suite.ID == "" {
		return nil, fmt.Errorf("suite id is required")
	}
	if len(suite.Scenarios) == 0 {
		return nil, fmt.Errorf("suite %q has no scenarios", suite.ID)
	}
	if suite.MaxTurns <= 0 {
		suite.MaxTurns = 4
	}
	for i := range suite.Scenarios {
		if suite.Scenarios[i].ID == "" {
			return nil, fmt.Errorf("scenario %d id is required", i)
		}
		if suite.Scenarios[i].Prompt == "" {
			return nil, fmt.Errorf("scenario %q prompt is required", suite.Scenarios[i].ID)
		}
	}
	return &suite, nil
}
