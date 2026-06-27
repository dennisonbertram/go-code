package replay

import "testing"

func TestReplayToolDispatchOutput(t *testing.T) {
	t.Parallel()

	dispatch := &ReplayToolDispatch{outputs: map[string]string{"call-1": "recorded output"}}
	out, ok := dispatch.Output("call-1")
	if !ok {
		t.Fatal("expected recorded output")
	}
	if out != "recorded output" {
		t.Fatalf("output = %q, want recorded output", out)
	}
	if _, ok := dispatch.Output("missing"); ok {
		t.Fatal("expected missing output to be absent")
	}
}
