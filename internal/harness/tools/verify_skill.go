package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go-agent-harness/internal/harness/tools/descriptions"

	"gopkg.in/yaml.v3"
)

// verifySkillMinBodyLen is the minimum number of characters required in a skill body.
const verifySkillMinBodyLen = 50

// computeVerdict returns "PASS", "FAIL", or "PARTIAL" based on the overall passed
// flag and the individual check results.
func computeVerdict(passed bool, checks []verifySkillCheck) string {
	if passed {
		return "PASS"
	}
	anyPassed := false
	for _, c := range checks {
		if c.Passed {
			anyPassed = true
			break
		}
	}
	if anyPassed {
		return "PARTIAL"
	}
	return "FAIL"
}

// verifySkillCheck is a single verification check result.
type verifySkillCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// verifySkillFrontmatter is a minimal struct for parsing SKILL.md frontmatter during verification.
type verifySkillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     int    `yaml:"version"`
}

// VerifySkillTool creates a deferred tool that validates a skill's SKILL.md structure
// and marks it as verified in the store if all checks pass.
// This is the public constructor used by the deferred package and external consumers.
func VerifySkillTool(verifier SkillVerifier) Tool {
	return verifySkillTool(verifier)
}

// verifySkillTool is the internal constructor.
func verifySkillTool(verifier SkillVerifier) Tool {
	def := Definition{
		Name:         "verify_skill",
		Description:  descriptions.Load("verify_skill"),
		Action:       ActionRead,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         TierDeferred,
		Tags:         []string{"skill", "verify", "quality", "validation"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Name of the skill to verify",
				},
			},
			"required":             []string{"name"},
			"additionalProperties": false,
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse verify_skill args: %w", err)
		}
		if strings.TrimSpace(args.Name) == "" {
			return "", fmt.Errorf("name is required")
		}

		return runSkillVerification(ctx, verifier, strings.TrimSpace(args.Name))
	}

	return Tool{Definition: def, Handler: handler}
}

// runSkillVerification executes all checks for the named skill and returns a JSON report.
func runSkillVerification(ctx context.Context, verifier SkillVerifier, name string) (string, error) {
	// Check 1: skill exists in the registry
	_, ok := verifier.GetSkill(name)
	if !ok {
		checks := []verifySkillCheck{
			{Name: "skill_exists", Passed: false, Message: fmt.Sprintf("skill %q not found in registry", name)},
		}
		result := map[string]any{
			"skill":   name,
			"passed":  false,
			"verdict": computeVerdict(false, checks),
			"error":   fmt.Sprintf("skill %q not found in registry", name),
			"checks":  checks,
		}
		return MarshalToolResult(result)
	}

	// Check 2: file path is available
	filePath, hasPath := verifier.GetSkillFilePath(name)
	if !hasPath || filePath == "" {
		checks := []verifySkillCheck{
			{Name: "skill_exists", Passed: true},
			{Name: "file_readable", Passed: false, Message: "skill file path not registered; cannot perform structural validation"},
		}
		result := map[string]any{
			"skill":   name,
			"passed":  false,
			"verdict": computeVerdict(false, checks),
			"error":   "skill file path is not available for structural validation",
			"checks":  checks,
		}
		return MarshalToolResult(result)
	}

	// Execute all file-based checks
	checks, passed := runSkillFileChecks(name, filePath)

	if !passed {
		result := map[string]any{
			"skill":   name,
			"passed":  false,
			"verdict": computeVerdict(false, checks),
			"checks":  checks,
		}
		return MarshalToolResult(result)
	}

	// All checks passed: mark the skill as verified
	now := time.Now().UTC()
	if err := verifier.UpdateSkillVerification(ctx, name, true, now, "automated"); err != nil {
		return "", fmt.Errorf("update skill verification for %q: %w", name, err)
	}

	result := map[string]any{
		"skill":       name,
		"passed":      true,
		"verdict":     computeVerdict(true, checks),
		"verified_by": "automated",
		"verified_at": now.Format(time.RFC3339),
		"checks":      checks,
	}
	return MarshalToolResult(result)
}

// runSkillFileChecks reads the SKILL.md file and performs all structural checks.
// Returns the list of check results and whether all checks passed.
func runSkillFileChecks(name, filePath string) ([]verifySkillCheck, bool) {
	checks := []verifySkillCheck{
		{Name: "skill_exists", Passed: true},
	}

	// Check: file is readable
	data, err := os.ReadFile(filePath)
	if err != nil {
		checks = append(checks, verifySkillCheck{
			Name:    "file_readable",
			Passed:  false,
			Message: fmt.Sprintf("cannot read SKILL.md: %v", err),
		})
		return checks, false
	}
	checks = append(checks, verifySkillCheck{Name: "file_readable", Passed: true})

	// Check: frontmatter is present and parseable
	content := string(data)
	fm, body, parseErr := splitAndParseVerifyFrontmatter(content)
	if parseErr != nil {
		checks = append(checks, verifySkillCheck{
			Name:    "frontmatter_valid",
			Passed:  false,
			Message: fmt.Sprintf("frontmatter parse error: %v", parseErr),
		})
		return checks, false
	}
	checks = append(checks, verifySkillCheck{Name: "frontmatter_valid", Passed: true})

	// Check: required field "name" is non-empty
	if strings.TrimSpace(fm.Name) == "" {
		checks = append(checks, verifySkillCheck{
			Name:    "required_fields",
			Passed:  false,
			Message: "frontmatter 'name' field is empty or missing",
		})
		return checks, false
	}

	// Check: required field "description" is non-empty
	if strings.TrimSpace(fm.Description) == "" {
		checks = append(checks, verifySkillCheck{
			Name:    "required_fields",
			Passed:  false,
			Message: "frontmatter 'description' field is empty or missing",
		})
		return checks, false
	}
	checks = append(checks, verifySkillCheck{Name: "required_fields", Passed: true})

	// Check: body is non-empty and substantive
	trimmedBody := strings.TrimSpace(body)
	if len(trimmedBody) < verifySkillMinBodyLen {
		checks = append(checks, verifySkillCheck{
			Name:    "body_content",
			Passed:  false,
			Message: fmt.Sprintf("skill body is too short (%d chars); minimum is %d chars", len(trimmedBody), verifySkillMinBodyLen),
		})
		return checks, false
	}
	checks = append(checks, verifySkillCheck{Name: "body_content", Passed: true})

	return checks, true
}

// splitAndParseVerifyFrontmatter splits SKILL.md content into frontmatter and body,
// then parses the YAML frontmatter. Used by verifySkillTool.
func splitAndParseVerifyFrontmatter(content string) (verifySkillFrontmatter, string, error) {
	const delimiter = "---"

	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, delimiter) {
		return verifySkillFrontmatter{}, "", fmt.Errorf("SKILL.md must start with --- frontmatter delimiter")
	}

	rest := trimmed[len(delimiter):]
	idx := strings.Index(rest, "\n"+delimiter)
	if idx < 0 {
		return verifySkillFrontmatter{}, "", fmt.Errorf("SKILL.md missing closing --- frontmatter delimiter")
	}

	fmStr := rest[:idx]
	body := strings.TrimSpace(rest[idx+len("\n"+delimiter):])

	var fm verifySkillFrontmatter
	if err := yaml.Unmarshal([]byte(fmStr), &fm); err != nil {
		return verifySkillFrontmatter{}, "", fmt.Errorf("invalid YAML: %w", err)
	}

	return fm, body, nil
}
