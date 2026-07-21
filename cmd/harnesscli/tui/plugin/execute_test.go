package plugin

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ExecuteBash behavioral tests
// ---------------------------------------------------------------------------

// BT-001: Given bash plugin with command='echo hello', output contains 'hello'
// and IsError is false.
func TestExecuteBash_Success(t *testing.T) {
	def := PluginDef{
		Name:    "greet",
		Handler: HandlerBash,
		Command: "echo hello",
	}
	result := ExecuteBash(def, nil)
	if result.IsError {
		t.Fatalf("expected IsError=false, got true; output: %q", result.Output)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", result.Output)
	}
}

// BT-002: Given bash plugin with command that exits non-zero, IsError is true
// and Output contains error info.
func TestExecuteBash_NonZeroExit(t *testing.T) {
	def := PluginDef{
		Name:    "fail",
		Handler: HandlerBash,
		Command: "exit 1",
	}
	result := ExecuteBash(def, nil)
	if !result.IsError {
		t.Fatalf("expected IsError=true for non-zero exit, got false; output: %q", result.Output)
	}
	if result.Output == "" {
		t.Error("expected non-empty Output for error result")
	}
}

// BT-003: Given bash plugin with a command that would run >10s (simulated via
// a very short timeout), IsError is true and Output mentions timeout.
func TestExecuteBash_Timeout(t *testing.T) {
	def := PluginDef{
		Name:    "sleeper",
		Handler: HandlerBash,
		Command: "sleep 60",
	}
	// We call ExecuteBashWithTimeout with 1 millisecond to guarantee expiry.
	result := ExecuteBashWithTimeout(def, nil, 1)
	if !result.IsError {
		t.Fatalf("expected IsError=true for timeout, got false; output: %q", result.Output)
	}
	lower := strings.ToLower(result.Output)
	if !strings.Contains(lower, "timeout") && !strings.Contains(lower, "timed out") && !strings.Contains(lower, "deadline") {
		t.Errorf("expected output to mention timeout, got %q", result.Output)
	}
}

// Table-driven regression tests for bash.
func TestExecuteBash_Table(t *testing.T) {
	cases := []struct {
		name         string
		command      string
		args         []string
		wantError    bool
		wantContains string
	}{
		{
			name:         "success echo",
			command:      "echo success",
			args:         nil,
			wantError:    false,
			wantContains: "success",
		},
		{
			name:         "non-zero exit",
			command:      "sh -c 'exit 2'",
			args:         nil,
			wantError:    true,
			wantContains: "",
		},
		{
			name:         "empty output",
			command:      "true",
			args:         nil,
			wantError:    false,
			wantContains: "",
		},
		{
			name:         "args appended",
			command:      "echo",
			args:         []string{"world"},
			wantError:    false,
			wantContains: "world",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := PluginDef{
				Name:    "test",
				Handler: HandlerBash,
				Command: tc.command,
			}
			result := ExecuteBash(def, tc.args)
			if result.IsError != tc.wantError {
				t.Errorf("IsError: want %v got %v; output: %q", tc.wantError, result.IsError, result.Output)
			}
			if tc.wantContains != "" && !strings.Contains(result.Output, tc.wantContains) {
				t.Errorf("expected output to contain %q, got %q", tc.wantContains, result.Output)
			}
		})
	}
}

// Regression: output larger than 30KB is capped with head+tail truncation.
func TestExecuteBash_OutputCappedAt30KB(t *testing.T) {
	// Generate ~60KB of output.
	def := PluginDef{
		Name:    "bigout",
		Handler: HandlerBash,
		Command: "python3 -c \"print('A'*1024)\" 2>/dev/null || dd if=/dev/zero bs=1024 count=60 2>/dev/null | tr '\\000' 'A'",
	}
	// Use a command that definitely produces > 30KB.
	def.Command = "for i in $(seq 1 2000); do echo \"line $i: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"; done"
	result := ExecuteBash(def, nil)
	if result.IsError {
		t.Fatalf("unexpected error: %q", result.Output)
	}
	const maxBytes = 30 * 1024
	if len(result.Output) > maxBytes+200 { // small slack for truncation marker
		t.Errorf("output length %d exceeds 30KB cap (with marker slack)", len(result.Output))
	}
	if !strings.Contains(result.Output, "truncated") {
		t.Errorf("expected truncation marker in output when exceeding 30KB, got output of len %d", len(result.Output))
	}
}

// ---------------------------------------------------------------------------
// ExecutePrompt behavioral tests
// ---------------------------------------------------------------------------

// BT-004: Template with {args} placeholder is substituted.
func TestExecutePrompt_WithArgsPlaceholder(t *testing.T) {
	def := PluginDef{
		Name:           "deploy",
		Handler:        HandlerPrompt,
		PromptTemplate: "Deploy {args} to staging",
	}
	result := ExecutePrompt(def, "v1.2")
	if result.IsError {
		t.Fatalf("expected IsError=false, got true; output: %q", result.Output)
	}
	const want = "Deploy v1.2 to staging"
	if result.Output != want {
		t.Errorf("expected %q, got %q", want, result.Output)
	}
}

// BT-005: Template without {args} placeholder is used verbatim.
func TestExecutePrompt_NoArgsPlaceholder(t *testing.T) {
	def := PluginDef{
		Name:           "status",
		Handler:        HandlerPrompt,
		PromptTemplate: "Show system status",
	}
	result := ExecutePrompt(def, "ignored args")
	if result.IsError {
		t.Fatalf("expected IsError=false, got true; output: %q", result.Output)
	}
	const want = "Show system status"
	if result.Output != want {
		t.Errorf("expected %q, got %q", want, result.Output)
	}
}

// Table-driven regression tests for prompt.
func TestExecutePrompt_Table(t *testing.T) {
	cases := []struct {
		name     string
		template string
		args     string
		want     string
	}{
		{
			name:     "with placeholder single arg",
			template: "Deploy {args} to staging",
			args:     "v1.2",
			want:     "Deploy v1.2 to staging",
		},
		{
			name:     "with placeholder multiple args",
			template: "Run {args}",
			args:     "foo bar",
			want:     "Run foo bar",
		},
		{
			name:     "without placeholder ignores args",
			template: "Show status",
			args:     "ignored",
			want:     "Show status",
		},
		{
			name:     "empty args with placeholder",
			template: "Deploy {args} now",
			args:     "",
			want:     "Deploy  now",
		},
		{
			name:     "quoted multi-word arg is one token",
			template: "Run {args}",
			args:     `"hello world" --fast`,
			want:     "Run hello world --fast",
		},
		{
			name:     "single-quoted arg",
			template: "Run {args}",
			args:     `'a b' c`,
			want:     "Run a b c",
		},
		{
			name:     "unterminated quote consumes rest",
			template: "Run {args}",
			args:     `"a b`,
			want:     "Run a b",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := PluginDef{
				Name:           "test",
				Handler:        HandlerPrompt,
				PromptTemplate: tc.template,
			}
			result := ExecutePrompt(def, tc.args)
			if result.IsError {
				t.Fatalf("unexpected error: %q", result.Output)
			}
			if result.Output != tc.want {
				t.Errorf("want %q, got %q", tc.want, result.Output)
			}
		})
	}
}

// Regression: ExecutePrompt always returns IsError=false (it cannot fail).
func TestExecutePrompt_NeverErrors(t *testing.T) {
	def := PluginDef{
		Name:           "nofail",
		Handler:        HandlerPrompt,
		PromptTemplate: "always works",
	}
	result := ExecutePrompt(def, "")
	if result.IsError {
		t.Errorf("ExecutePrompt should never return IsError=true, got true with output: %q", result.Output)
	}
}
