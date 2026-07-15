package main

import (
	"errors"
	"sort"
)

// TopoSort returns a topological order of nodes such that for every edge
// {u, v} in edges, u must come before v in the returned slice.
//
// Contract:
//   - Return a topological order: for every edge {u, v}, u appears before v.
//   - Deterministic tie-break: whenever multiple nodes are ready (in-degree
//     0) at the same time, the lexicographically SMALLEST ready node name is
//     emitted next. This makes the output unique for a given input.
//   - Every node in `nodes` must appear in the output, including nodes with
//     no incident edges (disconnected nodes), ordered by the same
//     lexicographic ready-set rule.
//   - If the graph has a cycle, return a nil slice and a non-nil error.
//   - Every endpoint that appears in edges is assumed to also appear in
//     nodes.
//
// Reference solution: the node universe comes from `nodes` (so disconnected
// nodes are included), the ready set is kept sorted so ties always resolve
// to the lexicographically smallest name, and a cycle is detected by
// comparing the number of emitted nodes against the full node universe.
func TopoSort(nodes []string, edges [][2]string) ([]string, error) {
	inDegree := make(map[string]int, len(nodes))
	adj := make(map[string][]string, len(nodes))

	for _, n := range nodes {
		inDegree[n] = 0
	}
	for _, e := range edges {
		u, v := e[0], e[1]
		adj[u] = append(adj[u], v)
		inDegree[v]++
	}

	ready := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if inDegree[n] == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)

	order := make([]string, 0, len(nodes))
	for len(ready) > 0 {
		u := ready[0]
		ready = ready[1:]
		order = append(order, u)

		for _, v := range adj[u] {
			inDegree[v]--
			if inDegree[v] == 0 {
				// Insert v keeping `ready` sorted so the next pick is
				// always the lexicographically smallest ready node.
				pos := sort.SearchStrings(ready, v)
				ready = append(ready, "")
				copy(ready[pos+1:], ready[pos:])
				ready[pos] = v
			}
		}
	}

	if len(order) != len(nodes) {
		return nil, errors.New("toposort: cycle detected")
	}
	return order, nil
}
