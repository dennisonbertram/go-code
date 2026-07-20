package causalgraph

// Builder accumulates causal graph events during a run and constructs the
// final CausalGraph when Build is called. It is not safe for concurrent use.
type Builder struct {
	nodes     map[string]Node // nodeID -> Node (deduplication)
	nodeOrder []string        // insertion order
	// contextEdges stores context dependency edges (Tier 1):
	// "contextID was in context when turnID was produced"
	contextEdges []Edge
	// results stores tool call results keyed by call ID for Tier 2 data flow.
	results map[string]string
	// args stores tool call arguments keyed by call ID for Tier 2 data flow.
	args map[string]string
	// ordering tracks tool call IDs in execution order for Tier 2.
	ordering []string
}

// NewBuilder creates a new empty Builder.
func NewBuilder() *Builder {
	return &Builder{
		nodes:   make(map[string]Node),
		results: make(map[string]string),
		args:    make(map[string]string),
	}
}

// RecordTurn records an LLM turn and which prior tool results/messages were
// in context for this turn. contextIDs are the IDs of messages that were
// passed to the provider for this turn.
func (b *Builder) RecordTurn(step int, turnID string, contextIDs []string) {
	if _, exists := b.nodes[turnID]; !exists {
		b.nodes[turnID] = Node{
			ID:   turnID,
			Type: NodeTypeLLMTurn,
			Step: step,
		}
		b.nodeOrder = append(b.nodeOrder, turnID)
	}

	// Create Tier 1 context edges: each contextID -> this turn
	for _, ctxID := range contextIDs {
		b.contextEdges = append(b.contextEdges, Edge{
			From: ctxID,
			To:   turnID,
			Type: EdgeTypeContext,
		})
	}
}

// RecordToolCall records a tool call made during a turn.
func (b *Builder) RecordToolCall(step int, callID string, toolName string, args string) {
	if _, exists := b.nodes[callID]; !exists {
		b.nodes[callID] = Node{
			ID:       callID,
			Type:     NodeTypeToolCall,
			Step:     step,
			ToolName: toolName,
		}
		b.nodeOrder = append(b.nodeOrder, callID)
	}
	b.args[callID] = args
	b.ordering = append(b.ordering, callID)
}

// RecordToolResult records the output of a tool call for data-flow analysis.
func (b *Builder) RecordToolResult(step int, callID string, result string) {
	b.results[callID] = result
}

// Build constructs the final causal graph from all recorded events.
// It merges Tier 1 (context) edges with Tier 2 (data flow) edges.
func (b *Builder) Build() CausalGraph {
	// Collect nodes in insertion order.
	nodes := make([]Node, 0, len(b.nodeOrder))
	for _, id := range b.nodeOrder {
		nodes = append(nodes, b.nodes[id])
	}

	// Tier 2: compute data flow edges.
	dataFlowEdges := FindDataFlowEdges(b.results, b.args, b.ordering)

	// Merge all edges.
	edges := make([]Edge, 0, len(b.contextEdges)+len(dataFlowEdges))
	edges = append(edges, b.contextEdges...)
	edges = append(edges, dataFlowEdges...)

	return CausalGraph{
		Nodes: nodes,
		Edges: edges,
	}
}
