package acp

import "testing"

func TestRPCErrorError(t *testing.T) {
	e := &rpcError{Code: CodeParseError, Message: "parse error"}
	if e.Error() != "parse error" {
		t.Fatalf("Error() = %q, want %q", e.Error(), "parse error")
	}
}
