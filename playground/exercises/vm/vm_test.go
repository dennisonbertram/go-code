package main

import (
	"reflect"
	"testing"
)

func opsEq(a, b []Op) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

func TestVMCorrectness(t *testing.T) {
	tests := []struct {
		name        string
		ops         []Op
		expect      int64
		errExpected bool
	}{
		{
			"simple add",
			[]Op{{PUSH, 2}, {PUSH, 3}, {ADD}, {HALT}},
			5, false,
		},
		{
			"sub, mul, div",
			[]Op{{PUSH, 10}, {PUSH, 5}, {SUB}, {PUSH, 4}, {MUL}, {PUSH, 2}, {DIV}, {HALT}},
			-10, false,
		},
		{
			"pop is dead",
			[]Op{{PUSH, 8}, {POP}, {PUSH, 7}, {HALT}},
			7, false,
		},
		{
			"stack underflow",
			[]Op{{POP}, {HALT}},
			0, true,
		},
		{
			"divide by zero",
			[]Op{{PUSH, 1}, {PUSH, 0}, {DIV}, {HALT}},
			0, true,
		},
	}

	for _, tt := range tests {
		vm := NewVM()
		vm.Load(tt.ops)
		result, err := vm.Run()
		if tt.errExpected && err == nil {
			t.Errorf("%s: expected error, got nil", tt.name)
		}
		if !tt.errExpected && err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
		}
		if !tt.errExpected && result != tt.expect {
			t.Errorf("%s: expected %d got %d", tt.name, tt.expect, result)
		}
	}
}

func TestOptimizerReducesAndKeepsValue(t *testing.T) {
	ops := []Op{
		{PUSH, 2}, {PUSH, 3}, {ADD},
		{PUSH, 5}, {POP},
		{PUSH, 2}, {PUSH, 2}, {MUL},
		{HALT},
	}
	vmUnopt := NewVM()
	vmUnopt.Load(ops)
	res1, err1 := vmUnopt.Run()
	if err1 != nil {
		t.Fatalf("unexpected error (unoptimized): %v", err1)
	}

	vmOpt := NewVM()
	vmOpt.Load(ops)
	opt := vmOpt.Optimize()
	// Load optimized ops and run
	vmOpt.Load(opt)
	res2, err2 := vmOpt.Run()
	if err2 != nil {
		t.Fatalf("unexpected error (optimized): %v", err2)
	}

	if res1 != res2 {
		t.Errorf("result mismatch: pre=%d post=%d", res1, res2)
	}
	if len(opt) >= len(ops) {
		t.Errorf("optimizer did not reduce instruction count: before=%d after=%d", len(ops), len(opt))
	}

	// Ensure POP/PUSH elimination and folding actually occurred
	optimizedExpected := []Op{
		{PUSH, 5}, // 2+3
		{PUSH, 4}, // 2*2
		{HALT},
	}
	if !opsEq(opt, optimizedExpected) {
		t.Errorf("unexpected optimized ops: got=%v want=%v", opt, optimizedExpected)
	}
}
