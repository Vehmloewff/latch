package latch

import (
	"context"

	"fmt"

	"log/slog"
	"net/http"
	"reflect"
	"sort"

	"github.com/coder/websocket"

	"github.com/vehmloewff/latch/wire"
)

// Methods returns a read-only snapshot of every registered method, sorted
// by name.
func (s *Server[S]) Methods() []MethodDescriptor {
	_ = s.finalize()
	s.mu.Lock()
	defer s.mu.Unlock()

	names := append([]string(nil), s.methodOrder...)
	sort.Strings(names)

	out := make([]MethodDescriptor, 0, len(names))
	for _, name := range names {
		m := s.methods[name]
		out = append(out, MethodDescriptor{
			Name:         name,
			RequestType:  m.adapter.RequestType,
			ResponseType: m.adapter.ResponseType,
		})
	}
	return out
}

// Events is retained for source compatibility with the removed named-event
// registry. Current servers have one event type, available in Manifest.IR.
//
// Deprecated: use the event stream generated from Server's E type.
func (s *Server[S]) Events() []EventDescriptor {
	_ = s.finalize()
	return nil
}

// ServeHTTP implements http.Handler, accepting a WebSocket connection,
// invoking OnConnect once, and then dispatching requests until the connection
// closes. There is no client handshake: the HTTP upgrade request is available
// through Conn.Request().
func (s *Server[S]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("version") == "" {
		http.Error(w, "latch: protocol version is required", http.StatusBadRequest)
		return
	}

	if err := s.finalize(); err != nil {
		s.logf(context.Background(), slog.LevelError, "latch: server misconfigured", "error", err)
		http.Error(w, "latch: server misconfigured", http.StatusInternalServerError)
		return
	}

	select {
	case <-s.shuttingDown:
		http.Error(w, "latch: server is shutting down", http.StatusServiceUnavailable)
		return
	default:
	}

	acceptOpts := &websocket.AcceptOptions{}
	if s.opts.CheckOrigin != nil {
		if !s.opts.CheckOrigin(r) {
			http.Error(w, "latch: origin not allowed", http.StatusForbidden)
			return
		}
		acceptOpts.InsecureSkipVerify = true
	} else {
		acceptOpts.OriginPatterns = s.opts.OriginPatterns
	}

	ws, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		s.logf(context.Background(), slog.LevelWarn, "latch: websocket accept failed", "error", err)
		return
	}
	ws.SetReadLimit(s.opts.MaxMessageBytes)

	conn := newConn(s, ws, r)
	go conn.writePump()

	s.track(conn)
	if s.onConnect != nil {
		state, err := s.callOnConnect(conn)
		if err != nil {
			_, message := errorCodeAndMessage(err, ErrCodeConnectRejected, "connection rejected", s.opts.Debug)
			_ = conn.enqueue(wire.Envelope{
				Type:      wire.FrameConnectionError,
				Error:     message,
				ErrorCode: ErrCodeConnectRejected,
			})
			_ = conn.Close(ClosePolicyViolation, "connection rejected")
			return
		}
		select {
		case <-conn.closed:
			return
		default:
		}
		s.connsMu.Lock()
		s.states[conn] = state
		s.connsMu.Unlock()
	} else {
		var zero S
		s.connsMu.Lock()
		s.states[conn] = zero
		s.connsMu.Unlock()
	}
	s.logf(conn.ctx, slog.LevelInfo, "latch: connection accepted")

	s.readLoop(conn)
}

func (s *Server[S]) callOnConnect(conn *Conn) (state any, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logf(conn.ctx, slog.LevelError, "latch: OnConnect panicked", "panic", r)
			err = NewError(ErrCodeConnectRejected, "connection rejected")
		}
	}()
	return s.onConnect(conn.ctx, s.onConnectEmitter.bindEmitter(conn), conn)
}

// readLoop reads frames until the connection closes or a protocol violation
// occurs, dispatching each "request" frame to its handler.
func (s *Server[S]) readLoop(conn *Conn) {
	defer conn.Close(CloseNormal, "")

	for {
		_, raw, err := conn.ws.Read(conn.ctx)
		if err != nil {
			return
		}

		var env wire.Envelope
		if err := env.UnmarshalBinary(raw); err != nil {
			s.protocolViolation(conn, "malformed binary envelope")
			return
		}

		switch env.Type {
		case wire.FrameRequest:
			if !s.handleRequest(conn, env) {
				s.protocolViolation(conn, "malformed request frame")
				return
			}
		default:
			s.protocolViolation(conn, fmt.Sprintf("unexpected frame type %q from client", env.Type))
			return
		}
	}
}

func (s *Server[S]) protocolViolation(conn *Conn, message string) {
	s.logf(conn.ctx, slog.LevelWarn, "latch: protocol violation", "message", message)
	env := wire.Envelope{Type: wire.FrameConnectionError, Error: message, ErrorCode: ErrCodeProtocolViolation}
	_ = conn.enqueue(env)
}

// handleRequest validates request-frame shape, enforces per-connection
// concurrency limits and request-ID uniqueness, and dispatches the method
// call on its own goroutine. It returns false only for a malformed frame
// (missing id/method), which is a protocol violation the caller must close
// the connection for; everything else (unknown method, invalid payload,
// application error) is reported as a normal "error" response frame.
func (s *Server[S]) handleRequest(conn *Conn, env wire.Envelope) bool {
	if env.ID == "" || env.Method == "" {
		return false
	}

	conn.inflightMu.Lock()
	if _, dup := conn.inflight[env.ID]; dup {
		conn.inflightMu.Unlock()
		s.sendResponseError(conn, env.ID, ErrCodeDuplicateRequestID, "a request with this id is already in flight")
		return true
	}
	reqCtx, cancel := context.WithCancel(conn.ctx)
	conn.inflight[env.ID] = cancel
	conn.inflightMu.Unlock()

	release := func() {
		conn.inflightMu.Lock()
		delete(conn.inflight, env.ID)
		conn.inflightMu.Unlock()
		cancel()
	}

	select {
	case conn.sem <- struct{}{}:
	case <-conn.ctx.Done():
		release()
		return true
	}

	go func() {
		defer func() { <-conn.sem }()
		defer release()
		s.dispatchMethod(reqCtx, conn, env)
	}()
	return true
}

func (s *Server[S]) dispatchMethod(ctx context.Context, conn *Conn, env wire.Envelope) {
	s.mu.Lock()
	m, ok := s.methods[env.Method]
	s.mu.Unlock()
	if !ok {
		s.sendResponseError(conn, env.ID, ErrCodeMethodNotFound, fmt.Sprintf("unknown method %q", env.Method))
		return
	}

	reqPtr := m.adapter.NewRequest()
	if err := wire.Decode(env.Payload, reqPtr); err != nil {
		s.sendResponseError(conn, env.ID, ErrCodeInvalidRequest, "malformed request payload")
		return
	}

	s.connsMu.Lock()
	state, ok := s.states[conn]
	s.connsMu.Unlock()
	if !ok {
		var zero S
		state = zero
	}
	resp, wireErr := s.invokeHandler(ctx, conn, state, m, reqPtr)
	if wireErr != nil {
		s.sendResponseError(conn, env.ID, wireErr.Code, wireErr.Message)
		return
	}

	raw, err := wire.Encode(resp)
	if err != nil {
		s.logf(conn.ctx, slog.LevelError, "latch: failed to marshal response", "method", env.Method, "error", err)
		s.sendResponseError(conn, env.ID, ErrCodeInternal, "internal error")
		return
	}

	_ = conn.enqueue(wire.Envelope{Type: wire.FrameResponse, ID: env.ID, Payload: raw})
}

// invokeHandler calls the method's handler, recovering from panics and
// mapping the result onto a wire error where applicable.
func (s *Server[S]) invokeHandler(ctx context.Context, conn *Conn, state any, m *methodEntry, reqPtr any) (resp any, wireErr *Error) {
	stateVal := reflect.ValueOf(state)
	if !stateVal.IsValid() {
		stateVal = reflect.Zero(reflect.TypeOf((*S)(nil)).Elem())
	}

	defer func() {
		if r := recover(); r != nil {
			s.logf(conn.ctx, slog.LevelError, "latch: handler panicked", "method", m.name, "panic", r)
			wireErr = NewError(ErrCodeInternal, "internal error")
		}
	}()

	result, err := m.adapter.Call(ctx, stateVal, reqPtr)
	if err != nil {
		code, message := errorCodeAndMessage(err, ErrCodeInternal, "internal error", s.opts.Debug)
		if code != ErrCodeInternal {
			s.logf(conn.ctx, slog.LevelDebug, "latch: handler returned application error", "method", m.name, "code", code)
		} else {
			s.logf(conn.ctx, slog.LevelError, "latch: handler returned error", "method", m.name, "error", err)
		}
		return nil, NewError(code, message)
	}
	return result, nil
}

func (s *Server[S]) sendResponseError(conn *Conn, id, code, message string) {
	_ = conn.enqueue(wire.Envelope{Type: wire.FrameError, ID: id, Error: message, ErrorCode: code})
}

func (s *Server[S]) logf(ctx context.Context, level slog.Level, msg string, args ...any) {
	if s.opts.Logger == nil {
		return
	}
	s.opts.Logger.Log(ctx, level, msg, args...)
}

// errorCodeAndMessage extracts a wire code/message pair from err: a *Error
// keeps its own code and message verbatim; anything else becomes
// defaultCode with defaultMessage, unless debug is enabled, in which case
// the underlying error string is included instead.
func errorCodeAndMessage(err error, defaultCode, defaultMessage string, debug bool) (code, message string) {
	if appErr, ok := err.(*Error); ok {
		return appErr.Code, appErr.Message
	}
	if debug {
		return defaultCode, err.Error()
	}
	return defaultCode, defaultMessage
}
