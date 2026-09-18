// Package client is Latchwire's Go client runtime: WebSocket transport, the
// connect handshake, request/response correlation, and event dispatch.
// Every generated Go client (see codegen/golang) imports this package
// rather than reimplementing transport logic itself; generated code
// contains only DTOs, method wrappers, and event registrations.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/vehmloewff/latchwire/wire"
	"github.com/vehmloewff/report"
)

// eventBufferSize bounds the per-event channel returned by RegisterEvent.
// Latchwire events are best-effort (see docs/design-notes.md): once the
// buffer is full, further events for that stream are dropped rather than
// blocking the connection's single read goroutine.
const eventBufferSize = 64

func recordSpanError(span trace.Span, message string, err error) {
	if err != nil {
		span.RecordError(err)
	}
	span.SetStatus(codes.Error, message)
}

type pendingResult struct {
	payload json.RawMessage
	err     report.Err
}

// Conn is a live Latchwire connection. Generated code never constructs one
// directly: Connect performs the handshake and returns a Conn whose
// message loop has not yet started, so that generated code can finish
// registering event handlers (via RegisterEvent) before any frame can
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

// Connect dials url, performs the Latchwire connect handshake with the
// given protocol name/version and connect payload, and returns a Conn once
// the server's "connected" frame arrives. If the server rejects the
// connection, the returned error is a report.Err carrying its message.
//
// Connect does not start reading further frames — the caller (generated
// code) must finish wiring up event handlers via RegisterEvent and then
// call Start.
func Connect(ctx context.Context, url string, protocolName string, protocolVersion string, payload any) (*Conn, error) {
	ctx, span := otel.Tracer("github.com/vehmloewff/latchwire/client").Start(ctx, "latchwire.client.connect",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("latchwire.protocol", protocolName),
			attribute.String("latchwire.version", protocolVersion),
		),
	)
	defer span.End()

	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "latchwire: dial failed")
		return nil, fmt.Errorf("latchwire: dial: %w", err)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "")
		recordSpanError(span, "latchwire: marshal connect payload failed", err)
		return nil, fmt.Errorf("latchwire: marshal connect payload: %w", err)
	}

	connectRaw, err := json.Marshal(wire.Envelope{
		Type:     wire.FrameConnect,
		Protocol: protocolName,
		Version:  protocolVersion,
		Payload:  raw,
	})
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "")
		recordSpanError(span, "latchwire: marshal connect frame failed", err)
		return nil, fmt.Errorf("latchwire: marshal connect frame: %w", err)
	}

	if err := ws.Write(ctx, websocket.MessageText, connectRaw); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "")
		recordSpanError(span, "latchwire: write connect frame failed", err)
		return nil, fmt.Errorf("latchwire: write connect frame: %w", err)
	}

	_, respRaw, err := ws.Read(ctx)
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "")
		recordSpanError(span, "latchwire: read handshake response failed", err)
		return nil, fmt.Errorf("latchwire: read handshake response: %w", err)
	}

	var env wire.Envelope
	if err := json.Unmarshal(respRaw, &env); err != nil {
		_ = ws.Close(websocket.StatusProtocolError, "malformed handshake response")
		recordSpanError(span, "latchwire: malformed handshake response", err)
		return nil, fmt.Errorf("latchwire: malformed handshake response: %w", err)
	}

	switch env.Type {
	case wire.FrameConnected:
		span.SetStatus(codes.Ok, "")
		return &Conn{
			ws:            ws,
			pending:       make(map[string]chan pendingResult),
			eventHandlers: make(map[string]func(json.RawMessage)),
			closed:        make(chan struct{}),
		}, nil

	case wire.FrameConnectionError:
		_ = ws.Close(websocket.StatusNormalClosure, "")
		if env.Error != "" {
			err := wireErrToError(env.Error, "connection rejected")
			recordSpanError(span, "latchwire: connection rejected", err)
			return nil, err
		}
		err := wireErrToError("", "connection rejected")
		recordSpanError(span, "latchwire: connection rejected", err)
		return nil, err

	default:
		_ = ws.Close(websocket.StatusProtocolError, "unexpected frame during handshake")
		recordSpanError(span, "latchwire: unexpected frame during handshake",
			report.New(fmt.Sprintf("unexpected frame type %q", env.Type)))
		return nil, fmt.Errorf("latchwire: unexpected frame type %q during handshake", env.Type)
	}
}

// Start begins the connection's read loop, dispatching responses to
// pending Call invocations and events to their registered handlers. Call
// it exactly once, after registering every event handler with
// RegisterEvent.
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
			c.deliver(env.ID, nil, wireErrToError(env.Error, "internal error"))
		case wire.FrameEvent:
			c.dispatchEvent(env.Event, env.Payload)
		case wire.FrameConnectionError:
			err := wireErrToError(env.Error, "connection error")
			c.failAll(err)
			return
		}
	}
}

func wireErrToError(message, defaultMessage string) report.Err {
	if message == "" {
		message = defaultMessage
	}
	return report.New(message).Wrap(message).Dump("source", "latchwire")
}

func (c *Conn) deliver(id string, payload json.RawMessage, err report.Err) {
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

func (c *Conn) dispatchEvent(name string, raw json.RawMessage) {
	c.eventMu.Lock()
	h := c.eventHandlers[name]
	c.eventMu.Unlock()
	if h != nil {
		h(raw)
	}
}

func (c *Conn) failAll(err report.Err) {
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
		c.failAll(report.New("the connection is closed"))

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
	ctx, span := otel.Tracer("github.com/vehmloewff/latchwire/client").Start(ctx, "latchwire.client.request",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("latchwire.method", method)),
	)
	defer span.End()

	select {
	case <-c.closed:
		err := report.New("the connection is closed")
		recordSpanError(span, "latchwire: connection closed", err)
		return nil, err
	default:
	}

	raw, err := json.Marshal(req)
	if err != nil {
		recordSpanError(span, "latchwire: marshal request failed", err)
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
		recordSpanError(span, "latchwire: marshal request frame failed", err)
		return nil, fmt.Errorf("latchwire: marshal request frame: %w", err)
	}

	c.writeMu.Lock()
	writeErr := c.ws.Write(ctx, websocket.MessageText, envRaw)
	c.writeMu.Unlock()
	if writeErr != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		recordSpanError(span, "latchwire: write request failed", writeErr)
		return nil, fmt.Errorf("latchwire: write request: %w", writeErr)
	}

	select {
	case res := <-ch:
		if res.err != nil {
			recordSpanError(span, "latchwire: request returned an error", res.err)
			return nil, res.err
		}
		span.SetStatus(codes.Ok, "")
		return res.payload, nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		recordSpanError(span, "latchwire: request canceled", ctx.Err())
		return nil, ctx.Err()
	case <-c.closed:
		err := report.New("the connection is closed")
		recordSpanError(span, "latchwire: connection closed", err)
		return nil, err
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

// RegisterEvent registers conn's handler for the named event and returns a
// receive-only channel of decoded payloads. It must be called for every
// event before conn.Start(); calling it afterward risks missing events
// delivered between Start() and the registration.
//
// The returned channel is closed when the connection closes. Delivery is
// best-effort: if the consumer isn't keeping up, once the internal buffer
// (capacity eventBufferSize) is full, further events for this stream are
// dropped rather than blocking the connection's single read goroutine.
func RegisterEvent[T any](conn *Conn, name string) <-chan T {
	ch := make(chan T, eventBufferSize)

	conn.eventMu.Lock()
	conn.eventHandlers[name] = func(raw json.RawMessage) {
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
