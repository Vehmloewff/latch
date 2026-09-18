package latchwire

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"go.opentelemetry.io/otel/attribute"

	"github.com/vehmloewff/latchwire/wire"
)

// writeTimeout bounds a single outbound WebSocket frame write.
const writeTimeout = 10 * time.Second

// CloseCode is a WebSocket close status code (RFC 6455 §7.4).
type CloseCode int

const (
	CloseNormal          CloseCode = 1000
	CloseGoingAway       CloseCode = 1001
	ClosePolicyViolation CloseCode = 1008
	CloseMessageTooBig   CloseCode = 1009
	CloseInternalError   CloseCode = 1011
)

// ConnKey identifies a piece of connection-scoped application state of type
// T. Create one with NewConnKey and use it with the package-level Set/Get
// functions — Go does not allow a generic method to introduce a new type
// parameter, so state access is exposed as functions rather than methods on
// Conn.
type ConnKey[T any] struct{}

// NewConnKey creates a new, distinct typed key for connection-scoped state.
// Each call returns a unique key, even if T is the same as a previous call.
func NewConnKey[T any]() *ConnKey[T] {
	return &ConnKey[T]{}
}

// connState is implemented by *Conn[C] for every C, letting the top-level
// Set/Get functions store typed state without themselves depending on C.
type connState interface {
	setState(key any, value any)
	getState(key any) (any, bool)
}

// Set attaches value to conn under key. Safe to call from any goroutine.
func Set[T any](conn connState, key *ConnKey[T], value T) {
	conn.setState(key, value)
}

// Get retrieves the value previously attached to conn under key. ok is false
// if nothing was ever set for key on this connection.
func Get[T any](conn connState, key *ConnKey[T]) (value T, ok bool) {
	raw, found := conn.getState(key)
	if !found {
		return value, false
	}
	return raw.(T), true
}

// outboundFrame pairs an envelope with an optional callback used only for
// closing the connection after a write failure.
type outboundFrame struct {
	env wire.Envelope
}

// Conn is a single, live, type-safe Latchwire connection. C is the server's
// connect-parameter type, already validated and decoded by the time
// application code ever observes a Conn.
type Conn[C any] struct {
	server *Server[C]
	ws     *websocket.Conn

	params C

	ctx    context.Context
	cancel context.CancelFunc

	outbound chan outboundFrame

	sendMu        sync.Mutex
	connectedSent bool
	pendingEvents []wire.Envelope

	stateMu sync.Mutex
	state   map[any]any

	closeOnce sync.Once
	closed    chan struct{}
	pumpDone  chan struct{}

	onCloseMu     sync.Mutex
	onCloseFns    []func()
	onCloseClosed bool

	inflightMu sync.Mutex
	inflight   map[string]context.CancelFunc

	sem chan struct{}
}

// newConn creates a Conn with a zero-valued Params(). The caller (the
// handshake in protocol.go) sets c.params directly once the connect payload
// has been decoded and validated, before any application code — OnConnect,
// a method handler, another goroutine — can observe the Conn.
func newConn[C any](srv *Server[C], ws *websocket.Conn, parentCtx context.Context) *Conn[C] {
	ctx, cancel := context.WithCancel(parentCtx)
	maxConcurrent := srv.opts.MaxConcurrentRequests
	queueSize := srv.opts.OutboundQueueSize

	c := &Conn[C]{
		server:   srv,
		ws:       ws,
		ctx:      ctx,
		cancel:   cancel,
		outbound: make(chan outboundFrame, queueSize),
		state:    make(map[any]any),
		closed:   make(chan struct{}),
		pumpDone: make(chan struct{}),
		inflight: make(map[string]context.CancelFunc),
		sem:      make(chan struct{}, maxConcurrent),
	}
	return c
}

// Params returns the connection's setup parameters. They have already been
// decoded from JSON and validated against the connect-parameter JSON Schema
// by the time any application code (OnConnect, a method handler) observes
// them.
func (c *Conn[C]) Params() C {
	return c.params
}

// Context is canceled when the connection closes, for any reason: the peer
// disconnects, the server calls Close, or the server shuts down.
func (c *Conn[C]) Context() context.Context {
	return c.ctx
}

// Done returns a channel that is closed when the connection closes.
// Equivalent to Context().Done().
func (c *Conn[C]) Done() <-chan struct{} {
	return c.ctx.Done()
}

// OnClose registers fn to run exactly once when the connection closes, for
// any reason. Callbacks run in LIFO order, like defer. A panicking callback
// is recovered and logged; it does not prevent other callbacks from running.
func (c *Conn[C]) OnClose(fn func()) {
	c.onCloseMu.Lock()
	if c.onCloseClosed {
		// Already closed: run immediately, under no lock, so callers can't
		// miss cleanup by registering slightly too late.
		c.onCloseMu.Unlock()
		c.runOnCloseFn(fn)
		return
	}
	c.onCloseFns = append(c.onCloseFns, fn)
	c.onCloseMu.Unlock()
}

func (c *Conn[C]) runOnCloseFn(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			spanError(c.ctx, "latchwire: OnClose callback panicked", panicReport(r),
				attribute.String("callback", "OnClose"))
		}
	}()
	fn()
}

// Close closes the connection with the given WebSocket close code and
// reason, cancels Context, and runs OnClose callbacks. It is safe to call
// multiple times and from multiple goroutines; only the first call has any
// effect.
func (c *Conn[C]) Close(code CloseCode, reason string) error {
	var closeErr error
	c.closeOnce.Do(func() {
		close(c.closed)
		c.cancel()

		// Let the writer pump finish draining any already-queued frames
		// (e.g. a connection_error or the final response) before we send
		// the WebSocket close frame, so a client never sees the close race
		// ahead of a frame Latchwire already committed to sending.
		select {
		case <-c.pumpDone:
		case <-time.After(2 * time.Second):
		}

		closeErr = c.ws.Close(websocket.StatusCode(code), reason)

		c.server.untrack(c)

		c.onCloseMu.Lock()
		fns := c.onCloseFns
		c.onCloseFns = nil
		c.onCloseClosed = true
		c.onCloseMu.Unlock()

		for i := len(fns) - 1; i >= 0; i-- {
			c.runOnCloseFn(fns[i])
		}
	})
	return closeErr
}

func (c *Conn[C]) setState(key any, value any) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.state[key] = value
}

func (c *Conn[C]) getState(key any) (any, bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	v, ok := c.state[key]
	return v, ok
}

// sendEventFrame implements ConnSender. It marshals payload, optionally
// validates it (debug mode), and either buffers it (if OnConnect is still
// running) or hands it to the outbound writer pump.
func (c *Conn[C]) sendEventFrame(name string, payload any) error {
	select {
	case <-c.closed:
		return fmt.Errorf("latchwire: cannot send event %q: connection is closed", name)
	default:
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("latchwire: marshal event %q payload: %w", name, err)
	}

	if v := c.server.eventValidator(name); v != nil {
		if err := v.ValidateJSON(raw); err != nil {
			return fmt.Errorf("latchwire: event %q payload failed schema validation: %w", name, err)
		}
	}

	env := wire.Envelope{Type: wire.FrameEvent, Event: name, Payload: raw}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if !c.connectedSent {
		c.pendingEvents = append(c.pendingEvents, env)
		return nil
	}
	return c.enqueue(env)
}

// flushAfterConnect sends the "connected" frame followed, in order, by any
// events buffered while OnConnect was running. It must be called with no
// concurrent sendEventFrame in flight racing it — that invariant is upheld
// because sendMu serializes against sendEventFrame for the whole operation.
func (c *Conn[C]) flushAfterConnect(connected wire.Envelope) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if err := c.enqueue(connected); err != nil {
		return err
	}
	for _, env := range c.pendingEvents {
		if err := c.enqueue(env); err != nil {
			return err
		}
	}
	c.pendingEvents = nil
	c.connectedSent = true
	return nil
}

// enqueue pushes env onto the bounded outbound queue without blocking. On
// overflow, per Latchwire's backpressure policy, it terminates the
// connection rather than silently dropping a typed frame. Used both for
// buffered/live event sends and for server-dispatch frames (responses,
// request errors, the connection_error frame).
func (c *Conn[C]) enqueue(env wire.Envelope) error {
	select {
	case c.outbound <- outboundFrame{env: env}:
		return nil
	default:
		go c.Close(ClosePolicyViolation, "outbound queue overflow")
		return fmt.Errorf("latchwire: outbound queue overflow; closing connection")
	}
}

// writePump serializes every outgoing WebSocket write through this single
// goroutine, so application code and server dispatch code never write to
// the socket directly or concurrently.
func (c *Conn[C]) writePump() {
	defer close(c.pumpDone)
	for {
		select {
		case f := <-c.outbound:
			if err := c.writeEnvelope(f.env); err != nil {
				spanError(c.ctx, "latchwire: write failed, closing connection", err)
				go c.Close(CloseInternalError, "write error")
				return
			}
			continue
		default:
		}

		select {
		case f := <-c.outbound:
			if err := c.writeEnvelope(f.env); err != nil {
				spanError(c.ctx, "latchwire: write failed, closing connection", err)
				go c.Close(CloseInternalError, "write error")
				return
			}
		case <-c.closed:
			// Best-effort final drain: flush whatever was already queued
			// (e.g. a connection_error frame enqueued right before Close)
			// before giving up the writer.
			for {
				select {
				case f := <-c.outbound:
					_ = c.writeEnvelope(f.env)
				default:
					return
				}
			}
		}
	}
}

func (c *Conn[C]) writeEnvelope(env wire.Envelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, raw)
}
