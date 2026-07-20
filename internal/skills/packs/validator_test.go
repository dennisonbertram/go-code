package packs

import (
	"strings"
	"testing"
)

func TestValidatePrereqs_AllMet(t *testing.T) {
	m := &SkillManifest{
		Name:        "test-pack",
		RequiresCLI: []string{"go"}, // go should be on PATH in test env
		RequiresEnv: []string{},
	}
	errs := ValidatePrereqs(m)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidatePrereqs_MissingCLI(t *testing.T) {
	m := &SkillManifest{
		Name:        "test-pack",
		RequiresCLI: []string{"definitely-does-not-exist-xyz-abc-123"},
	}
	errs := ValidatePrereqs(m)
	if len(errs) == 0 {
		t.Fatal("expected errors for missing CLI")
	}
	// Error should mention the missing tool
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "definitely-does-not-exist-xyz-abc-123") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("error should mention the missing CLI tool, got: %v", errs)
	}
}

func TestValidatePrereqs_MissingEnv(t *testing.T) {
	m := &SkillManifest{
		Name:        "test-pack",
		RequiresEnv: []string{"DEFINITELY_MISSING_ENV_VAR_XYZ_123"},
	}
	errs := ValidatePrereqs(m)
	if len(errs) == 0 {
		t.Fatal("expected errors for missing env var")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "DEFINITELY_MISSING_ENV_VAR_XYZ_123") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("error should mention the missing env var, got: %v", errs)
	}
}

func TestValidatePrereqs_MultipleMissing(t *testing.T) {
	m := &SkillManifest{
		Name:        "test-pack",
		RequiresCLI: []string{"missing-cli-1", "missing-cli-2"},
		RequiresEnv: []string{"MISSING_ENV_1", "MISSING_ENV_2"},
	}
	errs := ValidatePrereqs(m)
	if len(errs) < 4 {
		t.Errorf("expected at least 4 errors (2 CLI + 2 env), got %d: %v", len(errs), errs)
	}
}

func TestValidatePrereqs_EmptyRequirements(t *testing.T) {
	m := &SkillManifest{
		Name:        "no-reqs-pack",
		RequiresCLI: nil,
		RequiresEnv: nil,
	}
	errs := ValidatePrereqs(m)
	if len(errs) != 0 {
		t.Errorf("expected no errors for empty requirements, got: %v", errs)
	}
}

func TestPrereqError_CLIContainsHint(t *testing.T) {
	m := &SkillManifest{
		Name:        "test-pack",
		RequiresCLI: []string{"missing-tool-xyz"},
	}
	errs := ValidatePrereqs(m)
	if len(errs) == 0 {
		t.Fatal("expected an error")
	}
	// The error should contain something useful (install hint or description)
	errMsg := errs[0].Error()
	if !strings.Contains(errMsg, "missing-tool-xyz") {
		t.Errorf("error %q should mention missing-tool-xyz", errMsg)
	}
}

func TestPrereqError_EnvContainsHint(t *testing.T) {
	m := &SkillManifest{
		Name:        "test-pack",
		RequiresEnv: []string{"MISSING_SECRET_TOKEN"},
	}
	errs := ValidatePrereqs(m)
	if len(errs) == 0 {
		t.Fatal("expected an error")
	}
	errMsg := errs[0].Error()
	if !strings.Contains(errMsg, "MISSING_SECRET_TOKEN") {
		t.Errorf("error %q should mention MISSING_SECRET_TOKEN", errMsg)
	}
}
