package harness

import (
	"encoding/json"
	"fmt"
	"strings"

	"go-agent-harness/internal/store"
)

func buildWorkflowRecap(run Run, messages []Message, events []Event) *store.WorkflowRecap {
	recap := &store.WorkflowRecap{
		Goal:                   strings.TrimSpace(run.Prompt),
		ChangedFiles:           changedFilesFromTrace(messages, events),
		TestsRun:               testCommandsFromTrace(events),
		FailureCause:           strings.TrimSpace(run.Error),
		UsefulCommands:         usefulCommandsFromTrace(events),
		NextContinuationPrompt: nextContinuationPrompt(run),
	}
	recap.FixPattern = inferFixPattern(recap, run)
	return recap
}

func cloneWorkflowRecap(recap *store.WorkflowRecap) *store.WorkflowRecap {
	if recap == nil {
		return nil
	}
	cp := *recap
	cp.ChangedFiles = append([]string(nil), recap.ChangedFiles...)
	cp.TestsRun = append([]string(nil), recap.TestsRun...)
	cp.UsefulCommands = append([]string(nil), recap.UsefulCommands...)
	return &cp
}

func changedFilesFromTrace(messages []Message, events []Event) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(path string) {
		path = cleanTracePath(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}

	for _, ev := range events {
		if ev.Type != EventToolCallStarted {
			continue
		}
		args := toolArgsMap(ev)
		tool, _ := ev.Payload["tool"].(string)
		for _, key := range []string{"path", "file", "file_path", "filename", "target_file"} {
			if v, ok := args[key].(string); ok {
				add(v)
			}
		}
		if tool == "apply_patch" {
			if patch, ok := args["patch"].(string); ok {
				for _, p := range patchFiles(patch) {
					add(p)
				}
			}
		}
	}

	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			if call.Name != "apply_patch" {
				continue
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				continue
			}
			if patch, ok := args["patch"].(string); ok {
				for _, p := range patchFiles(patch) {
					add(p)
				}
			}
		}
	}
	return out
}

func usefulCommandsFromTrace(events []Event) []string {
	seen := make(map[string]bool)
	var out []string
	for _, ev := range events {
		if ev.Type != EventToolCallStarted {
			continue
		}
		tool, _ := ev.Payload["tool"].(string)
		if tool != "bash" && tool != "shell" && tool != "exec_command" {
			continue
		}
		args := toolArgsMap(ev)
		cmd := firstStringArg(args, "cmd", "command", "script")
		cmd = strings.TrimSpace(cmd)
		if cmd == "" || seen[cmd] {
			continue
		}
		seen[cmd] = true
		out = append(out, cmd)
	}
	return out
}

func testCommandsFromTrace(events []Event) []string {
	var out []string
	for _, cmd := range usefulCommandsFromTrace(events) {
		if looksLikeTestCommand(cmd) {
			out = append(out, cmd)
		}
	}
	return out
}

func looksLikeTestCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	return strings.Contains(lower, "go test") ||
		strings.Contains(lower, "test-regression") ||
		strings.Contains(lower, "npm test") ||
		strings.Contains(lower, "pnpm test") ||
		strings.Contains(lower, "yarn test") ||
		strings.Contains(lower, "pytest") ||
		strings.Contains(lower, "cargo test")
}

func inferFixPattern(recap *store.WorkflowRecap, run Run) string {
	if recap.FailureCause != "" {
		if len(recap.TestsRun) > 0 {
			return "captured failure cause and ran regression checks"
		}
		return "captured failure cause for continuation"
	}
	if len(recap.ChangedFiles) > 0 && len(recap.TestsRun) > 0 {
		return "changed files and ran regression checks"
	}
	if len(recap.ChangedFiles) > 0 {
		return "changed files for requested task"
	}
	if len(recap.TestsRun) > 0 {
		return "ran verification checks"
	}
	if strings.TrimSpace(run.Output) != "" {
		return "completed requested task"
	}
	return "recorded terminal run state"
}

func nextContinuationPrompt(run Run) string {
	goal := strings.TrimSpace(run.Prompt)
	if len([]rune(goal)) > 160 {
		runes := []rune(goal)
		goal = string(runes[:157]) + "..."
	}
	if goal == "" {
		goal = "the prior task"
	}
	return fmt.Sprintf("Continue run %s. Goal: %s. Review the recap, changed files, tests run, and last output before taking the next smallest verified step.", run.ID, goal)
}

func toolArgsMap(ev Event) map[string]any {
	raw, _ := ev.Payload["arguments"].(string)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

func firstStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := args[key].(string); ok {
			return v
		}
	}
	return ""
}

func patchFiles(patch string) []string {
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: "} {
			if strings.HasPrefix(line, prefix) {
				out = append(out, strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			}
		}
	}
	return out
}

func cleanTracePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "\"'")
	if path == "" || strings.Contains(path, "\x00") {
		return ""
	}
	return path
}
