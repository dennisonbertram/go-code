package main

import (
	"fmt"
	"strconv"
	"strings"
)

type Node interface{}

type NumberNode struct {
	Value float64
}

type IdentNode struct {
	Name string
}

type BinOpNode struct {
	Op          string
	Left, Right Node
}

// ParseExpr parses a very simple expression grammar: literals, variables, and binary ops +,-,*,/
func ParseExpr(src string) (Node, error) {
	tokens := tokenize(src)
	return parseExprTokens(tokens)
}

func tokenize(src string) []string {
	var tokens []string
	var buf strings.Builder
	for _, ch := range src {
		switch {
		case ch == '+' || ch == '-' || ch == '*' || ch == '/' || ch == '(' || ch == ')':
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
			tokens = append(tokens, string(ch))
		case ch == ' ':
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		default:
			buf.WriteRune(ch)
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}

func parseExprTokens(tokens []string) (Node, error) {
	return parseExpr1(&tokenStream{tokens, 0})
}

// Recursive descent, handles operator precedence
func parseExpr1(ts *tokenStream) (Node, error) { // +,-
	node, err := parseExpr2(ts)
	if err != nil {
		return nil, err
	}
	for {
		op := ts.peek()
		if op == "+" || op == "-" {
			ts.next()
			rhs, err := parseExpr2(ts)
			if err != nil {
				return nil, err
			}
			node = &BinOpNode{op, node, rhs}
		} else {
			break
		}
	}
	return node, nil
}

func parseExpr2(ts *tokenStream) (Node, error) { // *,/
	node, err := parseExpr3(ts)
	if err != nil {
		return nil, err
	}
	for {
		op := ts.peek()
		if op == "*" || op == "/" {
			ts.next()
			rhs, err := parseExpr3(ts)
			if err != nil {
				return nil, err
			}
			node = &BinOpNode{op, node, rhs}
		} else {
			break
		}
	}
	return node, nil
}

func parseExpr3(ts *tokenStream) (Node, error) { // atom
	tok := ts.peek()
	if tok == "(" {
		ts.next()
		node, err := parseExpr1(ts)
		if err != nil {
			return nil, err
		}
		if ts.peek() != ")" {
			return nil, fmt.Errorf("expected )")
		}
		ts.next()
		return node, nil
	}
	ts.next()
	if v, err := strconv.ParseFloat(tok, 64); err == nil {
		return &NumberNode{v}, nil
	}
	if len(tok) > 0 && ((tok[0] >= 'a' && tok[0] <= 'z') || (tok[0] >= 'A' && tok[0] <= 'Z')) {
		return &IdentNode{tok}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

type tokenStream struct {
	tokens []string
	pos    int
}

func (ts *tokenStream) peek() string {
	if ts.pos >= len(ts.tokens) {
		return ""
	}
	return ts.tokens[ts.pos]
}

func (ts *tokenStream) next() string {
	if ts.pos >= len(ts.tokens) {
		return ""
	}
	tok := ts.tokens[ts.pos]
	ts.pos++
	return tok
}
