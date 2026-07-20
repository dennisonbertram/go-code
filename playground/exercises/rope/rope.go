package main

import (
	"strings"
)

// Rope is a data structure for efficient string manipulation.
type Rope struct {
	left   *Rope
	right  *Rope
	weight int    // byte count in left subtree
	data   string // only for leaf nodes
}

// NewRope creates a rope from a string.
func NewRope(s string) *Rope {
	return &Rope{data: s, weight: len(s)}
}

// Index returns the byte at position i.
func (r *Rope) Index(i int) byte {
	if r.left == nil && r.right == nil {
		return r.data[i]
	}
	if i < r.weight {
		return r.left.Index(i)
	} else {
		return r.right.Index(i - r.weight)
	}
}

// String returns the concatenated string represented by the rope.
func (r *Rope) String() string {
	if r.left == nil && r.right == nil {
		return r.data
	}
	var b strings.Builder
	if r.left != nil {
		b.WriteString(r.left.String())
	}
	if r.right != nil {
		b.WriteString(r.right.String())
	}
	return b.String()
}

// Concat concatenates two ropes and returns a new rope.
func (r *Rope) Concat(other *Rope) *Rope {
	return &Rope{
		left:   r,
		right:  other,
		weight: r.Len(),
	}
}

// Len returns the total length (bytes) of the rope.
func (r *Rope) Len() int {
	if r.left == nil && r.right == nil {
		return len(r.data)
	}
	return r.weight + r.right.Len()
}

// Split returns two ropes: left part [0, i), right part [i, end)
func (r *Rope) Split(i int) (*Rope, *Rope) {
	if r.left == nil && r.right == nil {
		return &Rope{data: r.data[:i], weight: i}, &Rope{data: r.data[i:], weight: len(r.data) - i}
	}
	if i < r.weight {
		leftR, rightR := r.left.Split(i)
		return leftR, r.right.Concat(rightR)
	} else {
		leftR, rightR := r.right.Split(i - r.weight)
		return r.left.Concat(leftR), rightR
	}
}
