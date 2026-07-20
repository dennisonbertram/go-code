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
	// from the client (e.g. an answer to a future session/request_permission
	// call). There is nothing to route it to yet; drop it quietly.
	if req.Method == "" && (bytes.Contains(line, []byte(`"result"`)) || bytes.Contains(line, []byte(`"error"`))) {
		fmt.Fprintf(s.diag, "acp: ignoring client response with no pending request (id %s)\n", idForLog(req.ID))
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
