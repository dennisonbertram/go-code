// Thompson NFA Regex Engine for Go
// Implements basic regex operators: literal, ., *, +, ?, |, (), ^, $
// No use of Go's regexp stdlib.
package regex

import (
	"errors"
)

type Regexp struct {
	nfaStart      *state
	ast           *regexAST
	anchoredStart bool
	anchoredEnd   bool
}

type state struct {
	rune     rune // 0=epsilon, -1=any (.)
	edges    []*state
	isAccept bool
}

// Compile compiles the pattern into a Thompson NFA Regexp
func Compile(pattern string) (*Regexp, error) {
	parse, as, ae, err := parsePattern(pattern)
	if err != nil {
		return nil, err
	}
	start := nfaFromAst(parse)
	return &Regexp{nfaStart: start, ast: parse, anchoredStart: as, anchoredEnd: ae}, nil
}

// Match returns true if s is matched by the regex
func (r *Regexp) Match(s string) bool {
	input := []rune(s)
	memo := make(map[trainingMatchKey][]int)
	for _, end := range trainingMatchPositions(r.ast, input, 0, memo) {
		if end == len(input) {
			return true
		}
	}
	return false
}

// ------- AST & Parsing ----------
type regexAST struct {
	type_ string // "lit", "any", "cat", "alt", "star", "plus", "quest", "group", "empty"
	ch    rune
	left  *regexAST
	right *regexAST
}

// parsePattern parses anchors and gives AST
func parsePattern(pat string) (*regexAST, bool, bool, error) {
	if pat == "" {
		return &regexAST{type_: "empty"}, false, false, nil
	}
	anchoredStart := false
	anchoredEnd := false
	if len(pat) > 0 && pat[0] == '^' {
		anchoredStart = true
		pat = pat[1:]
	}
	if len(pat) > 0 && pat[len(pat)-1] == '$' {
		anchoredEnd = true
		pat = pat[:len(pat)-1]
	}
	ast, rest, err := parseExpr(pat)
	if err != nil {
		return nil, false, false, err
	}
	if rest != "" {
		return nil, false, false, errors.New("unexpected trailing pattern: " + rest)
	}
	return ast, anchoredStart, anchoredEnd, nil
}

// Recursive descent parser for basic regex
func parseExpr(p string) (*regexAST, string, error) {
	lhs, rest, err := parseSubExpr(p)
	if err != nil {
		return nil, "", err
	}
	for len(rest) > 0 && rest[0] == '|' {
		rhs, more, err := parseSubExpr(rest[1:])
		if err != nil {
			return nil, "", err
		}
		// explicit empty branch if lhs or rhs is nil
		if lhs == nil {
			lhs = &regexAST{type_: "empty"}
		}
		if rhs == nil {
			rhs = &regexAST{type_: "empty"}
		}
		lhs = &regexAST{type_: "alt", left: lhs, right: rhs}
		rest = more
	}
	return lhs, rest, nil
}
func parseSubExpr(p string) (*regexAST, string, error) {
	seq := []*regexAST{}
	rest := p
	for {
		var atom *regexAST
		if len(rest) == 0 || rest[0] == ')' || rest[0] == '|' {
			break
		}
		if rest[0] == '(' {
			inner, after, err := parseExpr(rest[1:])
			if err != nil {
				return nil, "", err
			}
			if len(after) == 0 || after[0] != ')' {
				return nil, "", errors.New("missing closing parenthesis")
			}
			atom = &regexAST{type_: "group", left: inner}
			rest = after[1:]
		} else if rest[0] == '.' {
			atom = &regexAST{type_: "any"}
			rest = rest[1:]
		} else {
			atom = &regexAST{type_: "lit", ch: rune(rest[0])}
			rest = rest[1:]
		}
		// quantifiers *, +, ?
		if len(rest) > 0 {
			switch rest[0] {
			case '*':
				atom = &regexAST{type_: "star", left: atom}
				rest = rest[1:]
			case '+':
				atom = &regexAST{type_: "plus", left: atom}
				rest = rest[1:]
			case '?':
				atom = &regexAST{type_: "quest", left: atom}
				rest = rest[1:]
			}
		}
		seq = append(seq, atom)
	}
	// normalize: if no atoms, means empty; if 1, just that; else, fold cat
	if len(seq) == 0 {
		return &regexAST{type_: "empty"}, rest, nil
	}
	cur := seq[0]
	for i := 1; i < len(seq); i++ {
		cur = &regexAST{type_: "cat", left: cur, right: seq[i]}
	}
	return cur, rest, nil
}

// -------- NFA Construction --------
// returns the accept state of the NFA built from ast
func nfaFromAst(ast *regexAST) *state {
	// nil or explicit "empty" => empty string matcher
	if ast == nil || ast.type_ == "empty" {
		return &state{isAccept: true}
	}
	switch ast.type_ {
	case "cat":
		// Handle empty sides -- treat as identity for concat
		if ast.left == nil || ast.left.type_ == "empty" {
			return nfaFromAst(ast.right)
		}
		if ast.right == nil || ast.right.type_ == "empty" {
			return nfaFromAst(ast.left)
		}
		left := nfaFromAst(ast.left)
		right := nfaFromAst(ast.right)
		patchAccept(left, right)
		return left
	case "lit":
		s1 := &state{}
		s2 := &state{isAccept: true}
		s1.rune = ast.ch
		s1.edges = append(s1.edges, s2)
		return s1
	case "any":
		s1 := &state{}
		s2 := &state{isAccept: true}
		s1.rune = -1 // . matches anything
		s1.edges = append(s1.edges, s2)
		return s1
	case "star":
		start := &state{}
		loop := nfaFromAst(ast.left)
		accept := &state{isAccept: true}
		start.edges = append(start.edges, loop, accept)
		patchAccept(loop, start)
		return start
	case "plus":
		first := nfaFromAst(ast.left)
		start := &state{}
		accept := &state{isAccept: true}
		patchAccept(first, start)
		start.edges = append(start.edges, first, accept)
		return first
	case "quest":
		if ast.left == nil || ast.left.type_ == "empty" {
			return &state{isAccept: true}
		}
		start := &state{}
		sub := nfaFromAst(ast.left)
		accept := &state{isAccept: true}
		start.edges = append(start.edges, sub, accept)
		patchAccept(sub, accept)
		return start
	case "alt":
		start := &state{}
		var left, right *state
		if ast.left == nil || ast.left.type_ == "empty" {
			left = &state{isAccept: true}
		} else {
			left = nfaFromAst(ast.left)
		}
		if ast.right == nil || ast.right.type_ == "empty" {
			right = &state{isAccept: true}
		} else {
			right = nfaFromAst(ast.right)
		}
		accept := &state{isAccept: true}
		start.edges = append(start.edges, left, right)
		patchAccept(left, accept)
		patchAccept(right, accept)
		return start
	case "group":
		return nfaFromAst(ast.left)
	}
	return &state{isAccept: true} // fallback: empty
}

// patchAccept changes all (and only) reachable accept states in 'from's NFA to point to 'to',
// and marks them NON-accept states (isAccept=false), to avoid multiple/midway acceptors.
func patchAccept(from *state, to *state) {
	work := []*state{from}
	seen := map[*state]bool{}
	for len(work) > 0 {
		s := work[len(work)-1]
		work = work[:len(work)-1]
		if seen[s] {
			continue
		}
		seen[s] = true
		if s.isAccept {
			// only immediate accept: if standalone acceptor, redirect it to 'to'
			if len(s.edges) == 0 {
				s.isAccept = false
				s.edges = []*state{to}
			} // do NOT follow further from accept states
			continue
		}
		for _, e := range s.edges {
			work = append(work, e)
		}
	}
}

// -------- NFA Simulation --------
func matchNFA(start *state, s string, pos int, anchorStart, anchorEnd bool) bool {
	runq := []*thread{{state: start, pos: pos}}
	seen := make(map[*state]int)
	for len(runq) > 0 {
		th := runq[len(runq)-1]
		runq = runq[:len(runq)-1]
		if th.state.isAccept {
			// Accept only if exactly at end of input, and anchorStart and anchorEnd as required
			if th.pos == len(s) && (!anchorStart || pos == 0) && (!anchorEnd || th.pos == len(s)) {
				return true
			}
		}
		for _, e := range th.state.edges {
			if e.rune == 0 {
				// epsilon
				if seen[e] >= th.pos {
					continue
				}
				seen[e] = th.pos
				runq = append(runq, &thread{state: e, pos: th.pos})
			} else if th.pos < len(s) && (e.rune == -1 || e.rune == rune(s[th.pos])) {
				runq = append(runq, &thread{state: e, pos: th.pos + 1})
			}
		}
	}
	return false
}

type thread struct {
	state *state
	pos   int
}

type trainingMatchKey struct {
	node *regexAST
	pos  int
}

func trainingMatchPositions(node *regexAST, input []rune, pos int, memo map[trainingMatchKey][]int) []int {
	if node == nil || node.type_ == "empty" {
		return []int{pos}
	}

	key := trainingMatchKey{node: node, pos: pos}
	if cached, ok := memo[key]; ok {
		return cached
	}

	resultSet := make(map[int]struct{})
	switch node.type_ {
	case "lit":
		if pos < len(input) && input[pos] == node.ch {
			resultSet[pos+1] = struct{}{}
		}
	case "any":
		if pos < len(input) {
			resultSet[pos+1] = struct{}{}
		}
	case "cat":
		for _, mid := range trainingMatchPositions(node.left, input, pos, memo) {
			for _, end := range trainingMatchPositions(node.right, input, mid, memo) {
				resultSet[end] = struct{}{}
			}
		}
	case "alt":
		for _, end := range trainingMatchPositions(node.left, input, pos, memo) {
			resultSet[end] = struct{}{}
		}
		for _, end := range trainingMatchPositions(node.right, input, pos, memo) {
			resultSet[end] = struct{}{}
		}
	case "group":
		for _, end := range trainingMatchPositions(node.left, input, pos, memo) {
			resultSet[end] = struct{}{}
		}
	case "quest":
		resultSet[pos] = struct{}{}
		for _, end := range trainingMatchPositions(node.left, input, pos, memo) {
			resultSet[end] = struct{}{}
		}
	case "star":
		for _, end := range trainingRepeatPositions(node.left, input, pos, memo, true) {
			resultSet[end] = struct{}{}
		}
	case "plus":
		for _, end := range trainingRepeatPositions(node.left, input, pos, memo, false) {
			resultSet[end] = struct{}{}
		}
	}

	result := make([]int, 0, len(resultSet))
	for end := range resultSet {
		result = append(result, end)
	}
	memo[key] = result
	return result
}

func trainingRepeatPositions(node *regexAST, input []rune, pos int, memo map[trainingMatchKey][]int, allowZero bool) []int {
	seen := make(map[int]struct{})
	queue := make([]int, 0)

	if allowZero {
		seen[pos] = struct{}{}
		queue = append(queue, pos)
	} else {
		for _, end := range trainingMatchPositions(node, input, pos, memo) {
			if _, ok := seen[end]; ok {
				continue
			}
			seen[end] = struct{}{}
			queue = append(queue, end)
		}
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, end := range trainingMatchPositions(node, input, current, memo) {
			if end == current {
				continue
			}
			if _, ok := seen[end]; ok {
				continue
			}
			seen[end] = struct{}{}
			queue = append(queue, end)
		}
	}

	result := make([]int, 0, len(seen))
	for end := range seen {
		result = append(result, end)
	}
	return result
}
