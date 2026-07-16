package main

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
// BUG: this implementation has three independent defects (search "BUG:"
// below). All three must be fixed.
func TopoSort(nodes []string, edges [][2]string) ([]string, error) {
	inDegree := make(map[string]int)
	adj := make(map[string][]string)

	// BUG: the node universe driving the algorithm is derived only from the
	// edges below (inDegree only ever gets an entry for a node that shows up
	// as an edge endpoint). A node listed in `nodes` with no incident edges
	// never gets an inDegree entry, so it never becomes "ready" and is
	// silently dropped from the output instead of being included.
	for _, e := range edges {
		u, v := e[0], e[1]
		if _, ok := inDegree[u]; !ok {
			inDegree[u] = 0
		}
		adj[u] = append(adj[u], v)
		inDegree[v]++
	}

	// BUG: ready nodes are pushed onto a stack in discovery order and popped
	// LIFO below, instead of always selecting the lexicographically smallest
	// ready node. Whenever more than one node is ready at once, the emitted
	// order is not the required deterministic lexicographic order.
	var ready []string
	for _, n := range nodes {
		if deg, ok := inDegree[n]; ok && deg == 0 {
			ready = append(ready, n)
		}
	}

	var order []string
	for len(ready) > 0 {
		u := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		order = append(order, u)
		for _, v := range adj[u] {
			inDegree[v]--
			if inDegree[v] == 0 {
				ready = append(ready, v)
			}
		}
	}

	// BUG: no cycle detection. If the graph has a cycle, some nodes never
	// reach in-degree 0 and `order` ends up shorter than the full node
	// universe, but this always returns (order, nil) instead of noticing the
	// shortfall and reporting an error.
	return order, nil
}
