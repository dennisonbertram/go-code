package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ACP stop reasons (https://agentclientprotocol.com/protocol/prompt-turn).
const (
	stopReasonEndTurn         = "end_turn"
	stopReasonMaxTurnRequests = "max_turn_requests"
	stopReasonRefusal         = "refusal"
	stopReasonCancelled       = "cancelled"
)

// stopReasonFor maps a run's terminal outcome onto the ACP stop reason the
// session/prompt response must carry.
func stopReasonFor(o terminalOutcome) string {
	switch o.eventType {
	case eventTypeRunCompleted:
		if o.costLimit {
			return stopReasonMaxTurnRequests
		}
		return stopReasonEndTurn
	case eventTypeRunCancelled:
		return stopReasonCancelled
	default: // run.failed and anything unexpected
		return stopReasonRefusal
	}
}

// contentBlock is one element of a session/prompt prompt array. Only the
// fields the adapter consumes are decoded; unknown members are ignored.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// extractPromptText flattens ACP content blocks into the plain-text prompt
// sent to harnessd. Text blocks contribute their text; resource_link blocks
// contribute their URI (the baseline content type every ACP agent must
// accept). Blocks of unsupported types (image, audio, embedded resources —
// all advertised as unsupported) are skipped. An empty result is invalid
// params.
func extractPromptText(blocks []contentBlock) (string, *rpcError) {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if s := strings.TrimSpace(b.Text); s != "" {
				parts = append(parts, s)
			}
		case "resource_link":
			if s := strings.TrimSpace(b.URI); s != "" {
				parts = append(parts, s)
			}
		default:
			// Unsupported content types are skipped; clients were told via
			// promptCapabilities not to send them.
		}
	}
	if len(parts) == 0 {
		return "", &rpcError{Code: CodeInvalidParams, Message: "Invalid params: prompt contains no usable text or resource_link content"}
	}
	return strings.Join(parts, "\n"), nil
}

// acpSession is one ACP session. One session maps to at most one go-code run
// (multi-turn is a later epic).
type acpSession struct {
	id   string
	cwd  string
	run  string // harnessd run id, set once a prompt starts it
	used bool   // true once a prompt turn has run
	// cancelRequested is set when session/cancel arrives before the run id
	// is stored (the run is still being created); handleSessionPrompt issues
	// the cancel as soon as it has the run id.
	cancelRequested bool
}

// sessionStore is a mutex-guarded sessionId -> session map.
type sessionStore struct {
	mu   sync.Mutex
	byID map[string]*acpSession
}

func newSessionStore() *sessionStore {
	return &sessionStore{byID: map[string]*acpSession{}}
}

// EnableSessions registers the ACP session methods (session/new,
// session/prompt, session/cancel) backed by the given harnessd runs client.
// Without it, session methods answer -32601 like any other unknown method.
func (s *Server) EnableSessions(client *RunsClient) {
	ss := &sessionHandlers{client: client, store: newSessionStore(), diag: s.diag, srv: s}
	s.Handle("session/new", ss.handleSessionNew)
	s.Handle("session/prompt", ss.handleSessionPrompt)
	s.Handle("session/cancel", ss.handleSessionCancel)
}

// sessionHandlers holds the state shared by the session method handlers.
type sessionHandlers struct {
	client *RunsClient
	store  *sessionStore
	diag   io.Writer
	srv    *Server // for writeNotification (session/update)
}

// handleSessionNew creates a fresh session and returns its id. The cwd and
// mcpServers params are accepted per the spec; the harness executes tools in
// its own workspace, so neither is acted on in this slice.
func (h *sessionHandlers) handleSessionNew(_ context.Context, params json.RawMessage) (any, *rpcError) {
	var req struct {
		CWD string `json:"cwd"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &rpcError{Code: CodeInvalidParams, Message: "Invalid params: " + err.Error()}
		}
	}
	id := "sess_" + randomHex(8)
	h.store.mu.Lock()
	h.store.byID[id] = &acpSession{id: id, cwd: req.CWD}
	h.store.mu.Unlock()
	return map[string]any{"sessionId": id}, nil
}

// handleSessionPrompt starts the session's run on harnessd and holds the
// response open until the run reaches a terminal state, then answers with
// the mapped ACP stop reason.
func (h *sessionHandlers) handleSessionPrompt(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var req struct {
		SessionID string         `json:"sessionId"`
		Prompt    []contentBlock `json:"prompt"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &rpcError{Code: CodeInvalidParams, Message: "Invalid params: " + err.Error()}
	}

	h.store.mu.Lock()
	sess, ok := h.store.byID[req.SessionID]
	if !ok {
		h.store.mu.Unlock()
		return nil, &rpcError{Code: CodeInvalidParams, Message: fmt.Sprintf("Invalid params: unknown sessionId %q", req.SessionID)}
	}
	if sess.used {
		h.store.mu.Unlock()
		return nil, &rpcError{Code: CodeInternalError, Message: "session already has a run; multi-turn sessions are not supported yet"}
	}
	sess.used = true
	h.store.mu.Unlock()

	prompt, rpcErr := extractPromptText(req.Prompt)
	if rpcErr != nil {
		return nil, rpcErr
	}

	runID, err := h.client.StartRun(ctx, prompt)
	if err != nil {
		return nil, &rpcError{Code: CodeInternalError, Message: "start run: " + err.Error()}
	}
	h.store.mu.Lock()
	sess.run = runID
	cancelRequested := sess.cancelRequested
	h.store.mu.Unlock()
	// A session/cancel that arrived while the run was being created is
	// applied now that the run id is known.
	if cancelRequested {
		if err := h.client.CancelRun(ctx, runID); err != nil {
			fmt.Fprintf(h.diag, "acp: cancel run %s: %v\n", runID, err)
		}
	}

	// Stream the run's events as session/update notifications. The queue and
	// its single writer keep notifications ordered and bound the buffering a
	// slow editor can force (deltas coalesce or drop; lifecycle updates win).
	queue := newUpdateQueue(updateQueueCapacity)
	writer := startUpdateWriter(queue, h.srv, req.SessionID, h.diag)
	outcome, err := h.client.WatchRun(ctx, runID, func(ev runEvent) {
		if update, kind, ok := translateRunEvent(ev); ok {
			queue.push(update, kind)
		}
	})
	// The terminal event has been seen: no more updates will be produced.
	// Drain the queue fully before responding — the spec requires all
	// session/update notifications to arrive before the session/prompt result.
	queue.close()
	writer.wait()
	if dropped := queue.droppedCount(); dropped > 0 {
		fmt.Fprintf(h.diag, "acp: session %s: dropped %d delta update(s) under backpressure\n", req.SessionID, dropped)
	}
	if err != nil {
		return nil, &rpcError{Code: CodeInternalError, Message: "wait for run: " + err.Error()}
	}
	return map[string]any{"stopReason": stopReasonFor(outcome)}, nil
}

// handleSessionCancel implements the session/cancel notification: cancel the
// session's run on harnessd. Cancelling a session with no run (or an unknown
// session) is a no-op logged to diagnostics — notifications get no response.
func (h *sessionHandlers) handleSessionCancel(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		fmt.Fprintf(h.diag, "acp: session/cancel with invalid params: %v\n", err)
		return nil, nil
	}
	h.store.mu.Lock()
	sess, ok := h.store.byID[req.SessionID]
	runID := ""
	if ok {
		runID = sess.run
		if runID == "" && sess.used {
			// A prompt turn is starting but hasn't stored its run id yet;
			// handleSessionPrompt applies the cancel as soon as it does.
			sess.cancelRequested = true
		}
	}
	h.store.mu.Unlock()
	if runID == "" {
		if ok && sess.used {
			fmt.Fprintf(h.diag, "acp: session/cancel for session %q before its run started; will cancel on start\n", req.SessionID)
		} else {
			fmt.Fprintf(h.diag, "acp: session/cancel for session %q with no active run; ignored\n", req.SessionID)
		}
		return nil, nil
	}
	if err := h.client.CancelRun(ctx, runID); err != nil {
		fmt.Fprintf(h.diag, "acp: cancel run %s: %v\n", runID, err)
	}
	return nil, nil
}

// randomHex returns n random bytes hex-encoded (2n chars).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("acp: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
