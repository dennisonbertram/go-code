// Package hooks implements config-driven lifecycle hooks: end users attach
// shell commands or HTTP calls to the runner's four lifecycle events
// (pre_message, post_message, pre_tool_use, post_tool_use) via JSON hook
// files, without writing Go code.
//
// The package is additive on top of the existing Go-level hook mechanism in
// internal/harness: adapters in this package implement the existing
// harness.PreMessageHook / PostMessageHook / PreToolUseHook / PostToolUseHook
// interfaces and are appended to RunnerConfig hook slices at harnessd
// startup, exactly like compiled-in plugins (see plugins/conclusion-watcher).
//
// Discovery directories:
//   - user-global: ~/.harness/hooks/      (trusted implicitly)
//   - project:     <workspace>/.harness/hooks/  (requires explicit trust)
//
// Trust: project-level hook files never execute until the user trusts them
// via Trust(); trust is keyed by (file path, SHA-256 of content), so editing
// a trusted file automatically un-trusts it. The trust store lives under the
// user-global directory — a project must never be able to trust itself.
package hooks

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Lifecycle event names. These map 1:1 to the four hook call sites in
// internal/harness/runner.go (applyPreHooks, applyPostHooks,
// applyPreToolUseHooks, applyPostToolUseHooks). The runner has no
// SessionStart/Stop call sites, so no such events exist here.
const (
	EventPreMessage  = "pre_message"
	EventPostMessage = "post_message"
	EventPreToolUse  = "pre_tool_use"
	EventPostToolUse = "post_tool_use"
)

// Hook kinds.
const (
	// KindCommand executes an argv command with the JSON event on stdin and
	// reads a JSON decision from stdout.
	KindCommand = "command"
	// KindHTTP POSTs the JSON event to a URL and reads a JSON decision from
	// the response body.
	KindHTTP = "http"
)

// DefaultTimeout bounds a hook subprocess/HTTP call when the hook file does
// not set timeout_seconds.
const DefaultTimeout = 10 * time.Second

// Source classifies where a hook file was discovered.
type Source string

const (
	// SourceUser marks hooks from the user-global directory (~/.harness/hooks).
	// They are trusted implicitly: the user wrote them into their own config.
	SourceUser Source = "user"
	// SourceProject marks hooks from any other directory (the project hooks
	// dir or extra configured dirs). They require explicit trust before they
	// load. Extra configured dirs classify as project on purpose: a malicious
	// project-level config must not be able to bypass trust by naming a dir.
	SourceProject Source = "project"
)

// HookDef is one hook definition loaded from a JSON hook file.
//
// JSON schema (unknown fields are rejected at load time):
//
//	{
//	  "name": "deny-rm",                  // optional; defaults to file base name
//	  "event": "pre_tool_use",            // required; one of the four events
//	  "kind": "command",                  // required; "command" or "http"
//	  "command": ["/path/to/script.sh"],  // required for kind=command (argv)
//	  "url": "https://example.com/hook",  // required for kind=http (http/https)
//	  "matcher": "bash",                  // optional; exact or glob tool-name
//	                                      // matcher (tool-use events only)
//	  "timeout_seconds": 5,               // optional; default 10
//	  "include_messages": false           // optional; pre/post_message payloads
//	                                      // include full messages only when true
//	}
type HookDef struct {
	Name            string   `json:"name,omitempty"`
	Event           string   `json:"event"`
	Kind            string   `json:"kind"`
	Command         []string `json:"command,omitempty"`
	URL             string   `json:"url,omitempty"`
	Matcher         string   `json:"matcher,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	IncludeMessages bool     `json:"include_messages,omitempty"`

	// Source records whether the file came from the user-global directory or
	// a project/extra directory. Populated by the loader, not from JSON.
	Source Source `json:"-"`
	// SourceDir is the directory the file was discovered in.
	SourceDir string `json:"-"`
	// FilePath is the full path of the hook file.
	FilePath string `json:"-"`
}

// SkipRecord explains why one hook file did not load. Skip records are
// returned alongside valid defs — one bad file never aborts the load — and
// flow into startup logs, GET /v1/hooks, and the TUI /hooks command.
type SkipRecord struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// Skip reasons used by the trust model (documented here so the listing
// surfaces and docs agree on the exact strings).
const (
	SkipReasonUntrusted            = "untrusted"
	SkipReasonModifiedSinceTrusted = "modified_since_trusted"
)

// Timeout returns the per-hook timeout, applying DefaultTimeout when unset.
func (d HookDef) Timeout() time.Duration {
	if d.TimeoutSeconds <= 0 {
		return DefaultTimeout
	}
	return time.Duration(d.TimeoutSeconds) * time.Second
}

// MatchesTool reports whether the def's tool-name matcher matches toolName.
// An empty matcher matches every tool. Matching uses path.Match glob
// semantics; a matcher without glob metacharacters is an exact match.
// Matchers apply to tool-use events only; message-event adapters ignore them.
func (d HookDef) MatchesTool(toolName string) bool {
	if d.Matcher == "" {
		return true
	}
	matched, err := path.Match(d.Matcher, toolName)
	if err != nil {
		// Unreachable: matchers are validated at load time. Fail closed.
		return false
	}
	return matched
}

// validate checks a decoded HookDef and returns an error naming the offending
// field. fileName is used to derive the default Name.
func (d *HookDef) validate(fileName string) error {
	switch d.Event {
	case EventPreMessage, EventPostMessage, EventPreToolUse, EventPostToolUse:
	default:
		return fmt.Errorf("unknown event %q: must be one of pre_message, post_message, pre_tool_use, post_tool_use", d.Event)
	}
	switch d.Kind {
	case KindCommand:
		if len(d.Command) == 0 {
			return fmt.Errorf("kind %q requires a non-empty command (argv)", KindCommand)
		}
		if strings.TrimSpace(d.Command[0]) == "" {
			return fmt.Errorf("command argv[0] must not be empty")
		}
	case KindHTTP:
		if strings.TrimSpace(d.URL) == "" {
			return fmt.Errorf("kind %q requires a url", KindHTTP)
		}
		if !strings.HasPrefix(d.URL, "http://") && !strings.HasPrefix(d.URL, "https://") {
			return fmt.Errorf("url %q must use http or https scheme", d.URL)
		}
	default:
		return fmt.Errorf("unknown kind %q: must be %q or %q", d.Kind, KindCommand, KindHTTP)
	}
	if d.TimeoutSeconds < 0 {
		return fmt.Errorf("timeout_seconds must not be negative (got %d)", d.TimeoutSeconds)
	}
	if d.Matcher != "" {
		if _, err := path.Match(d.Matcher, ""); err != nil {
			return fmt.Errorf("matcher %q is not a valid glob: %v", d.Matcher, err)
		}
	}
	if d.Name == "" {
		d.Name = strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	}
	return nil
}

// UserHooksDir returns the user-global hook discovery directory for the given
// home directory: ~/.harness/hooks.
func UserHooksDir(home string) string {
	return filepath.Join(home, ".harness", "hooks")
}

// ProjectHooksDir returns the project-level hook discovery directory for the
// given workspace root: <workspace>/.harness/hooks.
func ProjectHooksDir(workspace string) string {
	return filepath.Join(workspace, ".harness", "hooks")
}

// TrustStorePath returns the default trust-store path for the given home
// directory: ~/.harness/hooks-trust.json. The store lives under the
// user-global directory so a project can never trust itself.
func TrustStorePath(home string) string {
	return filepath.Join(home, ".harness", "hooks-trust.json")
}
