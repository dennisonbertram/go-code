package main

import (
	"testing"
)

func nodeEqual(a, b Node) bool {
	// For testing, do a structural deep equality
	switch x := a.(type) {
	case *NumberNode:
		y, ok := b.(*NumberNode)
		return ok && x.Value == y.Value
	case *IdentNode:
		y, ok := b.(*IdentNode)
		return ok && x.Name == y.Name
	case *BinOpNode:
		y, ok := b.(*BinOpNode)
		if !ok || x.Op != y.Op {
			return false
		}
		return nodeEqual(x.Left, y.Left) && nodeEqual(x.Right, y.Right)
	default:
		return false
	}
}

func TestConstFold_Table(t *testing.T) {
	tests := []struct {
		input  string
		expect Node
	}{
		{"1+2*3", &NumberNode{9}},
		{"(4/2)+x", &BinOpNode{"+", &NumberNode{2}, &IdentNode{"x"}}},
		{"x+y", &BinOpNode{"+", &IdentNode{"x"}, &IdentNode{"y"}}},
	}

	for _, tc := range tests {
		node, err := ParseExpr(tc.input)
		if err != nil {
			t.Fatalf("parse error for %q: %v", tc.input, err)
		}
		folded := ConstFold(node)
		if !nodeEqual(folded, tc.expect) {
			t.Errorf("ConstFold(%q): got %#v, want %#v", tc.input, folded, tc.expect)
		}
	}
}

func TestConstFold_EvalEquivalence(t *testing.T) {
	tests := []struct {
		input string
		env   map[string]float64
	}{
		{"1+2*3", nil},
		{"(4/2)+x", map[string]float64{"x": 5}},
		{"x+y", map[string]float64{"x": 3, "y": 2}},
	}

	for _, tc := range tests {
		node, err := ParseExpr(tc.input)
		if err != nil {
			t.Fatalf("parse error for %q: %v", tc.input, err)
		}
		folded := ConstFold(node)

		orig, err1 := Eval(node, tc.env)
		fold, err2 := Eval(folded, tc.env)
		if err1 != nil || err2 != nil {
			t.Fatalf("eval error: orig=%v, fold=%v", err1, err2)
		}
		if orig != fold {
			t.Errorf("Eval equivalence failed for %q: orig=%v, fold=%v", tc.input, orig, fold)
		}
	}
}
