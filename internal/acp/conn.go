package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// ErrMessageTooLarge is returned by ReadLine when a single message exceeds
// maxMessageSize bytes. The remainder of the offending line is drained so the
// stream stays aligned for the next message.
var ErrMessageTooLarge = errors.New("acp: message exceeds maximum size")

// maxMessageSize bounds a single inbound JSON-RPC message. A misbehaving or
// hostile client cannot exhaust memory with an unbounded line. It is a var
// (not a const) so tests can shrink it.
var maxMessageSize = 16 * 1024 * 1024

// Conn frames JSON-RPC 2.0 messages as newline-delimited JSON over an
// io.Reader/io.Writer pair. Writes are goroutine-safe: concurrent WriteJSON
// calls are serialized so messages never interleave.
type Conn struct {
	br *bufio.Reader
	w  io.Writer
	mu sync.Mutex // guards w
}

// NewConn returns a Conn reading newline-delimited JSON from r and writing
// newline-delimited JSON to w.
func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{br: bufio.NewReader(r), w: w}
}

// ReadLine reads one message (a single line without its trailing newline).
// Lines split across multiple reads and multiple lines delivered in one read
// are both handled transparently. A final line without a trailing newline is
// delivered as a complete message. Returns io.EOF when the stream is
// exhausted and ErrMessageTooLarge (after draining the line) when a message
// exceeds maxMessageSize.
func (c *Conn) ReadLine() ([]byte, error) {
	var buf []byte
	for {
		frag, err := c.br.ReadSlice('\n')
		buf = append(buf, frag...)
		if len(buf) > maxMessageSize {
			if err != nil {
				// The newline has not been consumed yet; discard the rest
				// of the offending line. (When err == nil ReadSlice already
				// consumed through the newline, so nothing remains to drain.)
				c.drainLine()
			}
			return nil, ErrMessageTooLarge
		}
		switch {
		case err == nil:
			return bytes.TrimRight(buf, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue // line longer than the read buffer; keep accumulating
		case errors.Is(err, io.EOF) && len(buf) > 0:
			// Final unterminated line: deliver it as a complete message.
			return bytes.TrimRight(buf, "\r\n"), nil
		default:
			return nil, err
		}
	}
}

// drainLine discards bytes through the next newline so the stream stays
// aligned after an oversized message. EOF mid-drain is fine: the stream is
// over anyway.
func (c *Conn) drainLine() {
	for {
		_, err := c.br.ReadSlice('\n')
		if err == nil || errors.Is(err, io.EOF) {
			return
		}
		// bufio.ErrBufferFull: line still going; loop again.
	}
}

// WriteJSON marshals v as compact JSON and writes it as a single
// newline-terminated line. It is safe for concurrent use; each call writes
// exactly one message with no interleaving.
func (c *Conn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.w.Write(data); err != nil {
		return err
	}
	_, err = c.w.Write([]byte{'\n'})
	return err
}
