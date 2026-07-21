package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Handler implements one JSON-RPC method. It returns either a result value
// (marshaled into the response's "result" member) or a *rpcError describing
// the failure. The returned rpcError is nil on success.
type Handler func(ctx context.Context, params json.RawMessage) (any, *rpcError)

// Server is a JSON-RPC 2.0 server speaking newline-delimited JSON over a
// Conn. It answers the ACP `initialize` handshake out of the box; later
// slices register session methods on top.
//
// Handlers run concurrently: session/prompt holds its response open until the
// run terminates, and a mid-turn session/cancel must still be read and
// processed. Responses may therefore arrive in any order (JSON-RPC clients
// correlate by id); writes stay serialized by the Conn's mutex.
type Server struct {
	conn     *Conn
	diag     io.Writer
	handlers map[string]Handler
	wg       sync.WaitGroup // in-flight handler goroutines

	pendingMu sync.Mutex
	pending   map[string]chan clientResponse // in-flight editor-bound calls, by id
	nextCall  int
}

// clientResponse is the editor's answer to a server-initiated request.
type clientResponse struct {
	result json.RawMessage
	rpcErr *rpcError
}

// NewServer returns a Server reading requests from r, writing protocol
// responses to w, and writing human-readable diagnostics to diag (kept nil-
// safe; pass io.Discard to silence). stdout stays a pure protocol channel:
// nothing but JSON-RPC messages is ever written to w.
func NewServer(r io.Reader, w io.Writer, diag io.Writer) *Server {
	if diag == nil {
		diag = io.Discard
	}
	s := &Server{
		conn:     NewConn(r, w),
		diag:     diag,
		handlers: make(map[string]Handler),
		pending:  make(map[string]chan clientResponse),
	}
	s.Handle("initialize", handleInitialize)
	return s
}

// Handle registers h for method, replacing any previous registration.
func (s *Server) Handle(method string, h Handler) {
	s.handlers[method] = h
}

// Serve reads and dispatches messages until the input reaches EOF (clean
// shutdown: it then waits for in-flight handlers to finish writing their
// responses before returning nil), the context is cancelled, or an I/O error
// occurs.
func (s *Server) Serve(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := s.conn.ReadLine()
		if errors.Is(err, ErrMessageTooLarge) {
			if werr := s.writeError(nullID, CodeInvalidRequest, "Invalid Request: message exceeds maximum size"); werr != nil {
				return werr
			}
			continue
		}
		if errors.Is(err, io.EOF) {
			s.wg.Wait() // drain in-flight handlers so their responses land
			return nil
		}
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue // tolerate blank lines between messages
		}
		if err := s.dispatch(ctx, line); err != nil {
			return err
		}
	}
}

// dispatch handles one raw message line: validate, route, respond.
func (s *Server) dispatch(ctx context.Context, line []byte) error {
	if !json.Valid(line) {
		return s.writeError(nullID, CodeParseError, "Parse error")
	}

	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		// Valid JSON but not an object-shaped request (array, bare string, ...).
		return s.writeError(nullID, CodeInvalidRequest, "Invalid Request")
	}

	// A message carrying result/error but no method is a JSON-RPC response
	// from the client — an answer to a server-initiated call (e.g.
	// session/request_permission). Route it to the pending waiter by id;
	// responses for unknown or already-completed ids are dropped quietly.
	if req.Method == "" && (bytes.Contains(line, []byte(`"result"`)) || bytes.Contains(line, []byte(`"error"`))) {
		var r struct {
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		_ = json.Unmarshal(line, &r)
		idKey := ""
		if req.ID != nil {
			// The pending map is keyed by the bare id value; the wire form
			// is JSON-encoded, so unquote it (falling back to raw bytes for
			// non-string ids).
			if err := json.Unmarshal(*req.ID, &idKey); err != nil {
				idKey = string(*req.ID)
			}
		}
		s.pendingMu.Lock()
		ch, ok := s.pending[idKey]
		if ok {
			delete(s.pending, idKey)
		}
		s.pendingMu.Unlock()
		if !ok {
			fmt.Fprintf(s.diag, "acp: ignoring client response with no pending request (id %s)\n", idForLog(req.ID))
			return nil
		}
		ch <- clientResponse{result: r.Result, rpcErr: r.Error}
		return nil
	}

	if req.JSONRPC != "2.0" || req.Method == "" {
		return s.writeError(requestID(req.ID), CodeInvalidRequest, "Invalid Request")
	}

	h, ok := s.handlers[req.Method]
	if !ok {
		if req.ID == nil {
			fmt.Fprintf(s.diag, "acp: ignoring notification for unknown method %q\n", req.Method)
			return nil
		}
		return s.writeError(requestID(req.ID), CodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}

	// Run the handler in its own goroutine so a long-lived method (notably
	// session/prompt, which stays open until the run terminates) does not
	// block reading and dispatching later messages (notably session/cancel).
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		result, rpcErr := h(ctx, req.Params)
		if req.ID == nil {
			// Notifications never receive responses, success or error.
			if rpcErr != nil {
				fmt.Fprintf(s.diag, "acp: notification %q failed: %s\n", req.Method, rpcErr.Message)
			}
			return
		}
		if rpcErr != nil {
			if err := s.writeError(requestID(req.ID), rpcErr.Code, rpcErr.Message); err != nil {
				fmt.Fprintf(s.diag, "acp: write error response for %q: %v\n", req.Method, err)
			}
			return
		}
		if err := s.writeResult(requestID(req.ID), result); err != nil {
			fmt.Fprintf(s.diag, "acp: write result for %q: %v\n", req.Method, err)
		}
	}()
	return nil
}

// writeResult sends a success response.
func (s *Server) writeResult(id json.RawMessage, result any) error {
	return s.conn.WriteJSON(response{JSONRPC: "2.0", ID: id, Result: result})
}

// writeNotification sends a server-initiated notification (no id, no
// response expected). It is safe for concurrent use with responses.
func (s *Server) writeNotification(method string, params any) error {
	return s.conn.WriteJSON(notification{JSONRPC: "2.0", Method: method, Params: params})
}

// writeError sends an error response.
func (s *Server) writeError(id json.RawMessage, code int, message string) error {
	return s.conn.WriteJSON(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

// requestID returns the id to echo in a response: the request's id when one
// was supplied, null otherwise.
func requestID(id *json.RawMessage) json.RawMessage {
	if id == nil {
		return nullID
	}
	return *id
}

func idForLog(id *json.RawMessage) string {
	if id == nil {
		return "<absent>"
	}
	return string(*id)
}

// callClient sends a JSON-RPC request to the editor (e.g.
// session/request_permission) and waits for its response. The wait ends when
// the editor answers, ctx is cancelled (turn over or approval deadline
// passed — the pending call is deregistered so a late answer is ignored), or
// the write fails. A JSON-RPC error object from the editor is returned as
// rpcErr.
func (s *Server) callClient(ctx context.Context, method string, params any) (json.RawMessage, *rpcError) {
	s.pendingMu.Lock()
	s.nextCall++
	id := fmt.Sprintf("acp-%d", s.nextCall)
	ch := make(chan clientResponse, 1)
	s.pending[id] = ch
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
	}()

	if err := s.conn.WriteJSON(clientRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, &rpcError{Code: CodeInternalError, Message: "write client request: " + err.Error()}
	}

	select {
	case resp := <-ch:
		return resp.result, resp.rpcErr
	case <-ctx.Done():
		return nil, &rpcError{Code: CodeInternalError, Message: "client call cancelled: " + ctx.Err().Error()}
	}
}
