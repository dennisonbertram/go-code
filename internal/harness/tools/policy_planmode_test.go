package tools

import (
	"context"
	"encoding/json"
	"testing"
)

type inactivePlanGate struct{}

func (inactivePlanGate) Active() bool                                   { return false }
func (inactivePlanGate) AllowMutation(Definition, json.RawMessage) bool { return false }

func TestApplyPolicyWithoutActivePlanGatePreservesApprovalBehavior(t *testing.T) {
	def := Definition{Name: "write", Action: ActionWrite, Mutating: true}
	for _, tc := range []struct {
		name   string
		policy Policy
	}{{"allowed", allowPolicy{}}, {"denied", denyPolicy{reason: "blocked"}}} {
		t.Run(tc.name, func(t *testing.T) {
			h := func(context.Context, json.RawMessage) (string, error) { return "handler", nil }
			baseline, err := ApplyPolicy(def, ApprovalModePermissions, tc.policy, h)(context.Background(), json.RawMessage(`{"path":"x"}`))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), ContextKeyPlanModeGate, inactivePlanGate{})
			got, err := ApplyPolicy(def, ApprovalModePermissions, tc.policy, h)(ctx, json.RawMessage(`{"path":"x"}`))
			if err != nil {
				t.Fatal(err)
			}
			if got != baseline {
				t.Fatalf("inactive gate changed policy output: got %q want %q", got, baseline)
			}
		})
	}
}
