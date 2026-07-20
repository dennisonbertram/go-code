package main

import (
	"errors"
)

type OpKind int

const (
	PUSH OpKind = iota
	POP
	ADD
	SUB
	MUL
	DIV
	HALT
)

type Op struct {
	Kind  OpKind
	Value int64 // only used for PUSH
}

type VM struct {
	ops   []Op
	stack []int64
	ip    int
}

func NewVM() *VM {
	return &VM{}
}

func (vm *VM) Load(ops []Op) {
	vm.ops = make([]Op, len(ops))
	copy(vm.ops, ops)
	vm.stack = []int64{}
	vm.ip = 0
}

// Optimize performs peephole optimizations and returns optimized ops
func (vm *VM) Optimize() []Op {
	ops := make([]Op, len(vm.ops))
	copy(ops, vm.ops)
	changed := true
	for changed {
		changed = false
		newOps := []Op{}
		i := 0
		for i < len(ops) {
			// Constant folding: PUSH x, PUSH y, <op> -> PUSH (x op y)
			if i+2 < len(ops) &&
				ops[i].Kind == PUSH &&
				ops[i+1].Kind == PUSH &&
				(ops[i+2].Kind == ADD || ops[i+2].Kind == SUB || ops[i+2].Kind == MUL || ops[i+2].Kind == DIV) {
				a := ops[i].Value
				b := ops[i+1].Value
				var result int64
				canFold := true
				switch ops[i+2].Kind {
				case ADD:
					result = a + b
				case SUB:
					result = a - b
				case MUL:
					result = a * b
				case DIV:
					if b == 0 {
						canFold = false
					} else {
						result = a / b
					}
				}
				if canFold {
					newOps = append(newOps, Op{Kind: PUSH, Value: result})
					changed = true
					i += 3
					continue
				}
			}

			// Dead POP elimination: PUSH x, POP -> nothing
			if i+1 < len(ops) && ops[i].Kind == PUSH && ops[i+1].Kind == POP {
				changed = true
				i += 2
				continue
			}

			newOps = append(newOps, ops[i])
			i++
		}
		ops = newOps
	}
	return ops
}

func (vm *VM) Run() (int64, error) {
	stack := []int64{}
	for ip := 0; ip < len(vm.ops); ip++ {
		op := vm.ops[ip]
		switch op.Kind {
		case PUSH:
			stack = append(stack, op.Value)
		case POP:
			if len(stack) == 0 {
				return 0, errors.New("stack underflow on pop")
			}
			stack = stack[:len(stack)-1]
		case ADD, SUB, MUL, DIV:
			if len(stack) < 2 {
				return 0, errors.New("stack underflow on arithmetic")
			}
			b, a := stack[len(stack)-1], stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			var res int64
			switch op.Kind {
			case ADD:
				res = a + b
			case SUB:
				res = a - b
			case MUL:
				res = a * b
			case DIV:
				if b == 0 {
					return 0, errors.New("division by zero")
				}
				res = a / b
			}
			stack = append(stack, res)
		case HALT:
			if len(stack) == 0 {
				return 0, nil
			}
			return stack[len(stack)-1], nil
		default:
			return 0, errors.New("unknown op")
		}
	}
	if len(stack) == 0 {
		return 0, nil
	}
	return stack[len(stack)-1], nil
}
