package latchwire

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/vehmloewff/latchwire/jsonschema"
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

// outboundFrame pairs an envelope with an optional callback used only for
// closing the connection after a write failure.
type outboundFrame struct {
	env wire.Envelope
}

// Conn is a single, live, type-safe Latchwire connection. E is the server's
// one outbound event payload type.
type Conn struct {
	server connServer
	ws     *websocket.Conn
	logger *slog.Logger

	request *http.Request

	ctx    context.Context
	cancel context.CancelFunc

	outbound chan outboundFrame

	sendMu    sync.Mutex
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

type connServer interface {
	eventValidator() *jsonschema.Validator
	options() Options
	untrack(*Conn)
}

// newConn creates a Conn and keeps a copy of the HTTP upgrade request for
// connection setup and authorization code.
func newConn[S any](srv *Server[S], ws *websocket.Conn, req *http.Request) *Conn {
	parentCtx := context.Background()
	ctx, cancel := context.WithCancel(parentCtx)
	maxConcurrent := srv.opts.MaxConcurrentRequests
	queueSize := srv.opts.OutboundQueueSize

	c := &Conn{
		server:   srv,
		ws:       ws,
		logger:   srv.opts.Logger,
		request:  req.Clone(ctx),
		ctx:      ctx,
		cancel:   cancel,
		outbound: make(chan outboundFrame, queueSize),
		closed:   make(chan struct{}),
		pumpDone: make(chan struct{}),
		inflight: make(map[string]context.CancelFunc),
		sem:      make(chan struct{}, maxConcurrent),
	}
	return c
}

// Request returns the HTTP request that upgraded this connection. It is a
// detached copy whose context is canceled when the connection closes.
func (c *Conn) Request() *http.Request {
	return c.request
}

// Context is canceled when the connection closes, for any reason: the peer
// disconnects, the server calls Close, or the server shuts down.
func (c *Conn) Context() context.Context {
	return c.ctx
}

// Done returns a channel that is closed when the connection closes.
// Equivalent to Context().Done().
func (c *Conn) Done() <-chan struct{} {
	return c.ctx.Done()
}

// OnClose registers fn to run exactly once when the connection closes, for
// any reason. Callbacks run in LIFO order, like defer. A panicking callback
// is recovered and logged; it does not prevent other callbacks from running.
func (c *Conn) OnClose(fn func()) {
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

func (c *Conn) runOnCloseFn(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			c.logf(slog.LevelError, "latchwire: OnClose callback panicked", "panic", r)
		}
	}()
	fn()
}

// Close closes the connection with the given WebSocket close code and
// reason, cancels Context, and runs OnClose callbacks. It is safe to call
// multiple times and from multiple goroutines; only the first call has any
// effect.
func (c *Conn) Close(code CloseCode, reason string) error {
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

// sendEventFrame marshals and sends one server event.
func (c *Conn) sendEventFrame(payload any) error {
	select {
	case <-c.closed:
		return fmt.Errorf("latchwire: cannot send event: connection is closed")
	default:
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("latchwire: marshal event payload: %w", err)
	}

	if c.server.options().ValidateResponses {
		if v := c.server.eventValidator(); v != nil {
			if err := v.ValidateJSON(raw); err != nil {
				return fmt.Errorf("latchwire: event payload failed schema validation: %w", err)
			}
		}
	}

	env := wire.Envelope{Type: wire.FrameEvent, Payload: raw}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.enqueue(env)
}

// enqueue pushes env onto the bounded outbound queue without blocking. On
// overflow, per Latchwire's backpressure policy, it terminates the
// connection rather than silently dropping a typed frame. Used both for
// buffered/live event sends and for server-dispatch frames (responses,
// request errors, the connection_error frame).
func (c *Conn) enqueue(env wire.Envelope) error {
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
func (c *Conn) writePump() {
	defer close(c.pumpDone)
	for {
		select {
		case f := <-c.outbound:
			if err := c.writeEnvelope(f.env); err != nil {
				c.logf(slog.LevelWarn, "latchwire: write failed, closing connection", "error", err)
				go c.Close(CloseInternalError, "write error")
				return
			}
			continue
		default:
		}

		select {
		case f := <-c.outbound:
			if err := c.writeEnvelope(f.env); err != nil {
				c.logf(slog.LevelWarn, "latchwire: write failed, closing connection", "error", err)
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

func (c *Conn) writeEnvelope(env wire.Envelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, raw)
}

func (c *Conn) logf(level slog.Level, msg string, args ...any) {
	if c.logger == nil {
		return
	}
	c.logger.Log(c.ctx, level, msg, args...)
}
