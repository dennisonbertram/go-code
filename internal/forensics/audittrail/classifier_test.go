package audittrail_test

import (
	"testing"

	"go-agent-harness/internal/forensics/audittrail"
)

func TestIsStateModifying_StateModifyingTools(t *testing.T) {
	stateModifying := []string{
		"file_write",
		"file_delete",
		"bash",
		"git_commit",
		"git_push",
		"write_file",
		"delete_file",
		"create_file",
		"modify_config",
		"file_write_patch",
		"create_directory",
	}

	for _, tool := range stateModifying {
		t.Run(tool, func(t *testing.T) {
			if !audittrail.IsStateModifying(tool) {
				t.Errorf("IsStateModifying(%q) = false, want true", tool)
			}
		})
	}
}

func TestIsStateModifying_ReadOnlyTools(t *testing.T) {
	readOnly := []string{
		"file_read",
		"grep",
		"glob",
		"find_tool",
		"list_directory",
		"read_file",
		"search_code",
		"get_run_summary",
		"list_runs",
		"ask_user_question",
	}

	for _, tool := range readOnly {
		t.Run(tool, func(t *testing.T) {
			if audittrail.IsStateModifying(tool) {
				t.Errorf("IsStateModifying(%q) = true, want false", tool)
			}
		})
	}
}

func TestIsStateModifying_EmptyTool(t *testing.T) {
	if audittrail.IsStateModifying("") {
		t.Error("IsStateModifying(\"\") = true, want false")
	}
}

func TestIsStateModifying_ExactMatches(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		expected bool
	}{
		{"bash exact", "bash", true},
		{"file_write exact", "file_write", true},
		{"file_delete exact", "file_delete", true},
		{"git_commit exact", "git_commit", true},
		{"git_push exact", "git_push", true},
		{"file_read not modifying", "file_read", false},
		{"grep not modifying", "grep", false},
		{"glob not modifying", "glob", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := audittrail.IsStateModifying(tc.tool)
			if got != tc.expected {
				t.Errorf("IsStateModifying(%q) = %v, want %v", tc.tool, got, tc.expected)
			}
		})
	}
}

func TestIsStateModifying_SubstringKeywords(t *testing.T) {
	// Tools with write/delete/create/modify in name are state-modifying
	tests := []struct {
		tool     string
		expected bool
	}{
		{"custom_write_tool", true},
		{"custom_delete_tool", true},
		{"custom_create_tool", true},
		{"custom_modify_tool", true},
		{"write_something", true},
		{"delete_something", true},
		{"create_something", true},
		{"modify_something", true},
		// These should NOT match
		{"writer", false}, // "write" substring but not keyword separated
		{"readwriter", false},
	}

	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			got := audittrail.IsStateModifying(tc.tool)
			if got != tc.expected {
				t.Errorf("IsStateModifying(%q) = %v, want %v", tc.tool, got, tc.expected)
			}
		})
	}
}

// TestIsStateModifying_CamelCaseNames verifies that camelCase tool names are
// correctly classified by splitting on uppercase letter boundaries (HIGH-4 fix).
func TestIsStateModifying_CamelCaseNames(t *testing.T) {
	tests := []struct {
		tool     string
		expected bool
	}{
		{"applyPatch", true},     // camelCase "apply" matches keyword
		{"commitChanges", true},  // camelCase "commit" matches keyword
		{"persistRecord", true},  // camelCase "persist" matches keyword
		{"deleteRecord", true},   // camelCase "delete" matches keyword
		{"updateConfig", true},   // camelCase "update" matches keyword
		{"readFile", false},      // "read" is not a keyword
		{"listDirectory", false}, // neither token is a keyword
		{"getStatus", false},     // neither token is a keyword
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			got := audittrail.IsStateModifying(tc.tool)
			if got != tc.expected {
				t.Errorf("IsStateModifying(%q) = %v, want %v", tc.tool, got, tc.expected)
			}
		})
	}
}

// TestIsStateModifying_HyphenSeparated verifies that hyphen-delimited tool
// names are correctly classified (HIGH-4 fix).
func TestIsStateModifying_HyphenSeparated(t *testing.T) {
	tests := []struct {
		tool     string
		expected bool
	}{
		{"put-object", true},     // "put" matches keyword
		{"persist-record", true}, // "persist" matches keyword
		{"patch-file", true},     // "patch" matches keyword
		{"read-file", false},     // "read" is not a keyword
		{"list-dir", false},      // neither is a keyword
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			got := audittrail.IsStateModifying(tc.tool)
			if got != tc.expected {
				t.Errorf("IsStateModifying(%q) = %v, want %v", tc.tool, got, tc.expected)
			}
		})
	}
}

// TestIsStateModifying_ExtendedKeywords verifies the extended keyword set
// covers previously-unclassified state-modifying patterns (HIGH-4 fix).
func TestIsStateModifying_ExtendedKeywords(t *testing.T) {
	tests := []struct {
		tool     string
		expected bool
	}{
		{"object_update", true},
		{"record_insert", true},
		{"buffer_append", true},
		{"file_exec", true},
		{"task_deploy", true},
		{"db_commit", true},
		{"record_persist", true},
		{"blob_upload", true},
		{"msg_send", true},
		{"config_save", true},
		{"data_store", true},
		{"obj_remove", true},
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			got := audittrail.IsStateModifying(tc.tool)
			if got != tc.expected {
				t.Errorf("IsStateModifying(%q) = %v, want %v", tc.tool, got, tc.expected)
			}
		})
	}
}
