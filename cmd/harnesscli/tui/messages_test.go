package tui_test

import (
	"encoding/json"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

func TestTUI009_MsgTypeCoverage(t *testing.T) {
	// Verify all expected message constructors exist by constructing them
	_ = tui.SSEEventMsg{EventType: "assistant.message.delta"}
	_ = tui.SSEErrorMsg{Err: nil}
	_ = tui.SSEDoneMsg{EventType: "run.completed"}
	_ = tui.SSEDropMsg{}
	_ = tui.ToolStartMsg{CallID: "x", Name: "bash"}
	_ = tui.ToolResultMsg{CallID: "x", Output: "out"}
	_ = tui.ToolErrorMsg{CallID: "x", Err: nil}
	_ = tui.AssistantDeltaMsg{Delta: "hello"}
	_ = tui.ThinkingDeltaMsg{Delta: "thinking..."}
	_ = tui.RunStartedMsg{RunID: "r1"}
	_ = tui.RunCompletedMsg{RunID: "r1"}
	_ = tui.RunFailedMsg{RunID: "r1", Error: "oops"}
	_ = tui.UsageDeltaMsg{InputTokens: 10, OutputTokens: 5, CostUSD: 0.001}
	_ = tui.SpinnerTickMsg{}
	_ = tui.CommandMsg{Input: "/help"}
	_ = tui.ClearMsg{}
	_ = tui.OverlayOpenMsg{Kind: "help"}
	_ = tui.OverlayCloseMsg{}
}

func TestTUI009_SSEEventMsgRoundTrip(t *testing.T) {
	payload := json.RawMessage(`{"delta":"hello world"}`)
	orig := tui.SSEEventMsg{EventType: "assistant.message.delta", Raw: payload, RunID: "run-1"}
	// Verify fields preserved
	if orig.EventType != "assistant.message.delta" {
		t.Error("EventType not preserved")
	}
	if string(orig.Raw) != `{"delta":"hello world"}` {
		t.Errorf("Raw not preserved: %s", orig.Raw)
	}
	if orig.RunID != "run-1" {
		t.Errorf("RunID not preserved: %s", orig.RunID)
	}
}

func TestTUI009_ZeroValueMsgsSafe(t *testing.T) {
	// Zero values should not panic when accessed
	var e tui.SSEErrorMsg
	_ = e.Err // nil is ok
	var ts tui.ToolStartMsg
	_ = ts.CallID
	var u tui.UsageDeltaMsg
	_ = u.CostUSD
}
