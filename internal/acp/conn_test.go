package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// chunkReader feeds its payload to the reader in fixed-size chunks so tests can
// simulate messages that arrive split across multiple reads.
type chunkReader struct {
	data []byte
	n    int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	if len(p) > r.n {
		p = p[:r.n]
	}
	copied := copy(p, r.data)
	r.data = r.data[copied:]
	return copied, nil
}

func TestConnReadLine(t *testing.T) {
	t.Run("single newline-terminated message", func(t *testing.T) {
		c := NewConn(strings.NewReader("{\"a\":1}\n"), io.Discard)
		line, err := c.ReadLine()
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if string(line) != `{"a":1}` {
			t.Fatalf("got %q", line)
		}
		if _, err := c.ReadLine(); err != io.EOF {
			t.Fatalf("expected io.EOF after message, got %v", err)
		}
	})

	t.Run("partial line split across reads", func(t *testing.T) {
		payload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n"
		c := NewConn(&chunkReader{data: []byte(payload), n: 3}, io.Discard)
		line, err := c.ReadLine()
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if string(line) != strings.TrimRight(payload, "\n") {
			t.Fatalf("got %q", line)
		}
	})

	t.Run("multiple messages delivered in one read", func(t *testing.T) {
		c := NewConn(strings.NewReader("{\"a\":1}\n{\"b\":2}\n{\"c\":3}\n"), io.Discard)
		want := []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}
		for i, w := range want {
			line, err := c.ReadLine()
			if err != nil {
				t.Fatalf("message %d: %v", i, err)
			}
			if string(line) != w {
				t.Fatalf("message %d: got %q want %q", i, line, w)
			}
		}
	})

	t.Run("oversized line is rejected and stream stays aligned", func(t *testing.T) {
		big := strings.Repeat("x", maxMessageSize+10)
		c := NewConn(strings.NewReader(big+"\n{\"ok\":true}\n"), io.Discard)
		if _, err := c.ReadLine(); !errors.Is(err, ErrMessageTooLarge) {
			t.Fatalf("expected ErrMessageTooLarge, got %v", err)
		}
		// The remainder of the oversized line must have been drained so the next
		// read returns the following message, not garbage from the middle.
		line, err := c.ReadLine()
		if err != nil {
			t.Fatalf("ReadLine after oversized: %v", err)
		}
		if string(line) != `{"ok":true}` {
			t.Fatalf("stream misaligned after oversized line, got %q", line[:min(len(line), 40)])
		}
	})

	t.Run("oversized line with unconsumed terminator drains remainder", func(t *testing.T) {
		// When the fragment that pushes the buffer over maxMessageSize does
		// not contain the line's newline, ReadLine must drain through the
		// terminator so the stream stays aligned. Shrink the cap below the
		// bufio buffer size so the crossing fragment ends mid-line.
		old := maxMessageSize
		maxMessageSize = 5000
		defer func() { maxMessageSize = old }()

		big := strings.Repeat("x", 9000)
		c := NewConn(strings.NewReader(big+"\n{\"ok\":true}\n"), io.Discard)
		if _, err := c.ReadLine(); !errors.Is(err, ErrMessageTooLarge) {
			t.Fatalf("expected ErrMessageTooLarge, got %v", err)
		}
		line, err := c.ReadLine()
		if err != nil {
			t.Fatalf("ReadLine after oversized: %v", err)
		}
		if string(line) != `{"ok":true}` {
			t.Fatalf("stream misaligned after drain, got %q", line[:min(len(line), 40)])
		}
	})

	t.Run("final line without trailing newline is delivered", func(t *testing.T) {
		c := NewConn(strings.NewReader(`{"a":1}`), io.Discard)
		line, err := c.ReadLine()
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if string(line) != `{"a":1}` {
			t.Fatalf("got %q", line)
		}
	})

	t.Run("CRLF line endings are normalized", func(t *testing.T) {
		c := NewConn(strings.NewReader("{\"a\":1}\r\n"), io.Discard)
		line, err := c.ReadLine()
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		if string(line) != `{"a":1}` {
			t.Fatalf("got %q", line)
		}
	})
}

func TestConnWriteJSON(t *testing.T) {
	t.Run("writes one compact newline-terminated JSON document", func(t *testing.T) {
		var buf bytes.Buffer
		c := NewConn(strings.NewReader(""), &buf)
		if err := c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"ok": true}}); err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}
		out := buf.String()
		if !strings.HasSuffix(out, "\n") {
			t.Fatalf("output not newline-terminated: %q", out)
		}
		if strings.Count(out, "\n") != 1 {
			t.Fatalf("output must be a single line, got %q", out)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(strings.TrimRight(out, "\n")), &decoded); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}
	})

	t.Run("concurrent writes never interleave", func(t *testing.T) {
		var buf bytes.Buffer
		c := NewConn(strings.NewReader(""), &buf)
		const goroutines = 8
		const perGoroutine = 50
		var wg sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < perGoroutine; i++ {
					if err := c.WriteJSON(map[string]int{"writer": g, "seq": i}); err != nil {
						t.Errorf("WriteJSON: %v", err)
						return
					}
				}
			}(g)
		}
		wg.Wait()

		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		if len(lines) != goroutines*perGoroutine {
			t.Fatalf("got %d lines, want %d — writes were lost or torn", len(lines), goroutines*perGoroutine)
		}
		seen := make(map[[2]int]bool)
		for _, ln := range lines {
			var m map[string]int
			if err := json.Unmarshal([]byte(ln), &m); err != nil {
				t.Fatalf("interleaved/torn write produced invalid JSON line %q: %v", ln, err)
			}
			key := [2]int{m["writer"], m["seq"]}
			if seen[key] {
				t.Fatalf("duplicate line for %v", key)
			}
			seen[key] = true
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
