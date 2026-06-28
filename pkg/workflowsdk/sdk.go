package workflowsdk

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// Run is the entrypoint signature for Go-authored workflows.
type Run func(ctx *Context) (any, error)

// Context exposes host-backed workflow operations to dynamically compiled
// workflow binaries.
type Context struct {
	Args any

	client *client
}

type AgentOpts struct {
	Label         string   `json:"label,omitempty"`
	Phase         string   `json:"phase,omitempty"`
	Schema        any      `json:"schema,omitempty"`
	Model         string   `json:"model,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Profile       string   `json:"profile,omitempty"`
	AllowedTools  []string `json:"allowed_tools,omitempty"`
	Isolation     string   `json:"isolation,omitempty"`
	CleanupPolicy string   `json:"cleanup_policy,omitempty"`
	AgentType     string   `json:"agent_type,omitempty"`
	MaxSteps      int      `json:"max_steps,omitempty"`
	MaxCostUSD    float64  `json:"max_cost_usd,omitempty"`
}

type AgentResult struct {
	Output string `json:"output"`
	Schema any    `json:"schema,omitempty"`
	Error  string `json:"error,omitempty"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Main connects a workflow binary to the host JSONL protocol and runs fn.
func Main(fn Run) {
	c, args, err := newClient(os.Stdin, os.Stdout)
	if err != nil {
		writeBootError(os.Stdout, err)
		os.Exit(1)
	}
	ctx := &Context{Args: args, client: c}
	result, runErr := fn(ctx)
	if runErr != nil {
		_ = c.sendTerminal("error", nil, runErr)
		os.Exit(1)
	}
	if err := c.sendTerminal("result", result, nil); err != nil {
		os.Exit(1)
	}
}

func (c *Context) Agent(prompt string, opts *AgentOpts) (*AgentResult, error) {
	var out AgentResult
	err := c.client.call("agent", map[string]any{
		"prompt": prompt,
		"opts":   opts,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Context) Phase(title string) error {
	return c.client.call("phase", map[string]any{"title": title}, nil)
}

func (c *Context) Log(message string) error {
	return c.client.call("log", map[string]any{"message": message}, nil)
}

func (c *Context) Feedback(kind, message string, data map[string]any) error {
	return c.client.call("feedback", map[string]any{
		"kind":    kind,
		"message": message,
		"data":    data,
	}, nil)
}

func (c *Context) Question(prompt string, choices []QuestionOption) (any, error) {
	var out any
	err := c.client.call("question", map[string]any{
		"prompt":  prompt,
		"choices": choices,
	}, &out)
	return out, err
}

func (c *Context) Workflow(name string, args any) (any, error) {
	var out any
	err := c.client.call("workflow", map[string]any{
		"name": name,
		"args": args,
	}, &out)
	return out, err
}

type client struct {
	enc    *json.Encoder
	encMu  sync.Mutex
	nextID atomic.Int64

	pendingMu sync.Mutex
	pending   map[string]chan response
}

type message struct {
	ID     string          `json:"id,omitempty"`
	Type   string          `json:"type"`
	Args   any             `json:"args,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Raw    json.RawMessage `json:"-"`
}

type response struct {
	ID     string          `json:"id,omitempty"`
	Type   string          `json:"type"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func newClient(stdin io.Reader, stdout io.Writer) (*client, any, error) {
	dec := json.NewDecoder(bufio.NewReader(stdin))
	var start response
	if err := dec.Decode(&start); err != nil {
		return nil, nil, fmt.Errorf("read workflow start message: %w", err)
	}
	if start.Type != "start" {
		return nil, nil, fmt.Errorf("expected start message, got %q", start.Type)
	}
	var args any
	if len(start.Result) > 0 {
		if err := json.Unmarshal(start.Result, &args); err != nil {
			return nil, nil, fmt.Errorf("decode workflow args: %w", err)
		}
	}
	c := &client{
		enc:     json.NewEncoder(stdout),
		pending: make(map[string]chan response),
	}
	go c.readLoop(dec)
	return c, args, nil
}

func (c *client) call(typ string, args any, out any) error {
	id := fmt.Sprintf("req_%d", c.nextID.Add(1))
	ch := make(chan response, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	c.encMu.Lock()
	err := c.enc.Encode(message{ID: id, Type: typ, Args: args})
	c.encMu.Unlock()
	if err != nil {
		c.dropPending(id)
		return err
	}

	resp := <-ch
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return err
		}
	}
	return nil
}

func (c *client) sendTerminal(typ string, result any, err error) error {
	msg := message{Type: typ, Result: result}
	if err != nil {
		msg.Error = err.Error()
	}
	c.encMu.Lock()
	defer c.encMu.Unlock()
	return c.enc.Encode(msg)
}

func (c *client) readLoop(dec *json.Decoder) {
	for {
		var resp response
		if err := dec.Decode(&resp); err != nil {
			c.failAll(err)
			return
		}
		if resp.ID == "" {
			continue
		}
		c.pendingMu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.pendingMu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *client) dropPending(id string) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *client) failAll(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- response{ID: id, Error: err.Error()}
	}
}

func writeBootError(stdout io.Writer, err error) {
	_ = json.NewEncoder(stdout).Encode(message{Type: "error", Error: err.Error()})
}
