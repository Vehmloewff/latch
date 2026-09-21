// Package client is Latchwire's Go client runtime: WebSocket transport,
// request/response correlation, and event dispatch.
// Every generated Go client (see codegen/golang) imports this package
// rather than reimplementing transport logic itself; generated code
// contains only DTOs, method wrappers, and the one event stream.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"

	"github.com/vehmloewff/latch/wire"
)

// eventBufferSize bounds the per-event channel returned by RegisterEvent.
// Latchwire events are best-effort (see docs/design-notes.md): once the
// buffer is full, further events for that stream are dropped rather than
// blocking the connection's single read goroutine.
const eventBufferSize = 64

// Error is Latchwire's structured application/protocol error, matching the
// wire error envelope's {code, message} shape.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type pendingResult struct {
	payload json.RawMessage
	err     *Error
}

// Conn is a live Latchwire connection. Generated code never constructs one
// directly: Connect dials the WebSocket and returns a Conn whose
// message loop has not yet started, so generated code can register the event
// stream before any frame can
// possibly be dispatched — see Start.
type Conn struct {
	ws *websocket.Conn

	writeMu sync.Mutex

	nextID atomic.Int64

	pendingMu sync.Mutex
	pending   map[string]chan pendingResult

	eventMu       sync.Mutex
	eventHandlers map[string]func(json.RawMessage)
	eventClosers  []func()

	closed    chan struct{}
	closeOnce sync.Once
}

// Connect dials rawURL and returns a live Conn. The protocol version is added
// as the "version" query parameter; no initial setup frame is sent.
//
// Connect does not start reading further frames — the caller (generated
// code) must finish wiring up event handlers via RegisterEvent and then
// call Start.
func Connect(ctx context.Context, rawURL, version string) (*Conn, error) {
	target, err := addVersionQuery(rawURL, version)
	if err != nil {
		return nil, fmt.Errorf("latchwire: build connection URL: %w", err)
	}
	ws, _, err := websocket.Dial(ctx, target, nil)
	if err != nil {
		return nil, fmt.Errorf("latchwire: dial: %w", err)
	}
	return &Conn{
		ws:            ws,
		pending:       make(map[string]chan pendingResult),
		eventHandlers: make(map[string]func(json.RawMessage)),
		closed:        make(chan struct{}),
	}, nil
}

func addVersionQuery(rawURL, version string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("version", version)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// Start begins the connection's read loop, dispatching responses and the
// single event stream. Call it exactly once, after registering the stream.
func (c *Conn) Start() {
	go c.readLoop()
}

// Close closes the underlying WebSocket connection and fails every pending
// Call and open event channel.
func (c *Conn) Close() error {
	err := c.ws.Close(websocket.StatusNormalClosure, "")
	c.terminate()
	return err
}

// Closed returns a channel that is closed once the connection has closed,
// for any reason.
func (c *Conn) Closed() <-chan struct{} {
	return c.closed
}

func (c *Conn) readLoop() {
	defer c.terminate()

	for {
		_, raw, err := c.ws.Read(context.Background())
		if err != nil {
			return
		}

		var env wire.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}

		switch env.Type {
		case wire.FrameResponse:
			c.deliver(env.ID, env.Payload, nil)
		case wire.FrameError:
			c.deliver(env.ID, nil, wireErrToError(env.Error, "internal_error", "internal error"))
		case wire.FrameEvent:
			c.dispatchEvent(env.Payload)
		case wire.FrameConnectionError:
			c.failAll(wireErrToError(env.Error, "internal_error", "connection error"))
			return
		}
	}
}

func wireErrToError(message, defaultCode, defaultMessage string) *Error {
	if message == "" {
		message = defaultMessage
	}
	return &Error{Code: defaultCode, Message: message}
}

func (c *Conn) deliver(id string, payload json.RawMessage, err *Error) {
	if id == "" {
		return
	}
	c.pendingMu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if ok {
		ch <- pendingResult{payload: payload, err: err}
	}
}

func (c *Conn) dispatchEvent(raw json.RawMessage) {
	c.eventMu.Lock()
	h := c.eventHandlers[""]
	c.eventMu.Unlock()
	if h != nil {
		h(raw)
	}
}

func (c *Conn) failAll(err *Error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan pendingResult)
	c.pendingMu.Unlock()
	for _, ch := range pending {
		ch <- pendingResult{err: err}
	}
}

func (c *Conn) terminate() {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.failAll(&Error{Code: "connection_closed", Message: "the connection is closed"})

		c.eventMu.Lock()
		closers := c.eventClosers
		c.eventClosers = nil
		c.eventMu.Unlock()
		for _, closeFn := range closers {
			closeFn()
		}
	})
}

// call sends a request frame for method and blocks until its response,
// error, ctx cancellation, or connection close.
func (c *Conn) call(ctx context.Context, method string, req any) (json.RawMessage, error) {
	select {
	case <-c.closed:
		return nil, &Error{Code: "connection_closed", Message: "the connection is closed"}
	default:
	}

	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("latchwire: marshal request: %w", err)
	}

	id := fmt.Sprintf("%d", c.nextID.Add(1))
	ch := make(chan pendingResult, 1)

	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	envRaw, err := json.Marshal(wire.Envelope{Type: wire.FrameRequest, ID: id, Method: method, Payload: raw})
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("latchwire: marshal request frame: %w", err)
	}

	c.writeMu.Lock()
	writeErr := c.ws.Write(ctx, websocket.MessageText, envRaw)
	c.writeMu.Unlock()
	if writeErr != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("latchwire: write request: %w", writeErr)
	}

	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.payload, nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-c.closed:
		return nil, &Error{Code: "connection_closed", Message: "the connection is closed"}
	}
}

// Call invokes method on conn with req, decoding its response into TResp.
// Generated method wrappers are thin calls to this function; it is a
// top-level generic function, not a method, because Go does not allow a
// method to introduce a new type parameter.
func Call[TResp any](ctx context.Context, conn *Conn, method string, req any) (TResp, error) {
	var zero TResp
	raw, err := conn.call(ctx, method, req)
	if err != nil {
		return zero, err
	}
	var resp TResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return zero, fmt.Errorf("latchwire: decode response for %q: %w", method, err)
	}
	return resp, nil
}

// RegisterEvent registers conn's handler for the single event stream and
// returns a receive-only channel of decoded payloads. The optional name is
// ignored and exists only for source compatibility with older generated
// clients. Call it before conn.Start().
//
// The returned channel is closed when the connection closes. Delivery is
// best-effort: if the consumer isn't keeping up, once the internal buffer
// (capacity eventBufferSize) is full, further events for this stream are
// dropped rather than blocking the connection's single read goroutine.
func RegisterEvent[T any](conn *Conn, _ ...string) <-chan T {
	ch := make(chan T, eventBufferSize)

	conn.eventMu.Lock()
	conn.eventHandlers[""] = func(raw json.RawMessage) {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return
		}
		select {
		case ch <- v:
		default:
		}
	}
	conn.eventClosers = append(conn.eventClosers, func() { close(ch) })
	conn.eventMu.Unlock()

	return ch
}
