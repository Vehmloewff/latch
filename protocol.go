package latchwire

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"time"

	"github.com/coder/websocket"

	"github.com/vehmloewff/latchwire/jsonschema"
	"github.com/vehmloewff/latchwire/protocol"
	"github.com/vehmloewff/latchwire/wire"
)

// handshakeTimeout bounds how long Latchwire waits for a client's initial
// connect frame before giving up on the connection.
const handshakeTimeout = 10 * time.Second

// Manifest is Latchwire's stable, machine-readable description of a
// finalized API: enough for a code generator to build TypeScript, Dart, or
// Go clients (or anything else) without ever inspecting Go reflection or
// source code directly. LatchwireVersion is bumped whenever the manifest
// shape itself changes incompatibly.
type Manifest struct {
	LatchwireVersion int              `json:"latchwireVersion"`
	Protocol         string           `json:"protocol"`
	Version          string           `json:"version"`
	Connect          map[string]any   `json:"connect"`
	Methods          []ManifestMethod `json:"methods"`
	Events           []ManifestEvent  `json:"events"`
	Types            map[string]any   `json:"types"`

	// IR is the complete, lossless normalized intermediate representation
	// the reflection stage produced — exactly what every code generator
	// consumes. The Connect/Methods[].Request/Response/Events[].Payload
	// JSON Schema documents above are a derived, human-and-tool-friendly
	// view of the same data; IR is what the `latchwire generate` CLI
	// (cmd/latchwire) actually deserializes to regenerate clients without
	// ever re-running reflection.
	IR *protocol.Protocol `json:"ir"`
}

// ManifestMethod describes one registered method, with self-contained JSON
// Schema documents for its request and response.
type ManifestMethod struct {
	Name     string         `json:"name"`
	Request  map[string]any `json:"request"`
	Response map[string]any `json:"response"`
}

// ManifestEvent describes one registered event, with a self-contained JSON
// Schema document for its payload.
type ManifestEvent struct {
	Name    string         `json:"name"`
	Payload map[string]any `json:"payload"`
}

// Manifest finalizes the server (if not already finalized) and returns its
// complete protocol manifest. Manifest generation is deterministic: the
// same set of registrations always produces byte-identical JSON.
func (s *Server[C]) Manifest() (*Manifest, error) {
	if err := s.finalize(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m := &Manifest{
		LatchwireVersion: 1,
		Protocol:         s.opts.ProtocolName,
		Version:          s.opts.ProtocolVersion,
		Connect:          jsonschema.BuildDocument(s.ir, s.ir.ConnectType),
		Types:            jsonschema.AllDefs(s.ir),
		IR:               s.ir,
	}
	for _, meth := range s.ir.Methods {
		m.Methods = append(m.Methods, ManifestMethod{
			Name:     meth.Name,
			Request:  jsonschema.BuildDocument(s.ir, meth.RequestType),
			Response: jsonschema.BuildDocument(s.ir, meth.ResponseType),
		})
	}
	for _, evt := range s.ir.Events {
		m.Events = append(m.Events, ManifestEvent{
			Name:    evt.Name,
			Payload: jsonschema.BuildDocument(s.ir, evt.PayloadType),
		})
	}
	return m, nil
}

// WriteManifest writes the server's protocol manifest to w as indented
// JSON.
func (s *Server[C]) WriteManifest(w io.Writer) error {
	m, err := s.Manifest()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// Methods returns a read-only snapshot of every registered method, sorted
// by name.
func (s *Server[C]) Methods() []MethodDescriptor {
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

// Events returns a read-only snapshot of every registered event, sorted by
// name.
func (s *Server[C]) Events() []EventDescriptor {
	_ = s.finalize()
	s.mu.Lock()
	defer s.mu.Unlock()

	names := append([]string(nil), s.eventOrder...)
	sort.Strings(names)

	out := make([]EventDescriptor, 0, len(names))
	for _, name := range names {
		e := s.events[name]
		out = append(out, EventDescriptor{Name: name, PayloadType: e.payloadType})
	}
	return out
}

// ConnectSchema returns the JSON Schema document for the server's
// connect-parameter type.
func (s *Server[C]) ConnectSchema() map[string]any {
	if err := s.finalize(); err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return jsonschema.BuildDocument(s.ir, s.connectRef)
}

// ServeHTTP implements http.Handler, accepting a WebSocket connection,
// running the connect handshake, and then dispatching requests until the
// connection closes. The connect-parameter type, every registered method,
// and every registered event are validated once, on the first call to
// ServeHTTP, Manifest, or Generate (whichever runs first).
func (s *Server[C]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := s.finalize(); err != nil {
		s.logf(context.Background(), slog.LevelError, "latchwire: server misconfigured", "error", err)
		http.Error(w, "latchwire: server misconfigured", http.StatusInternalServerError)
		return
	}

	select {
	case <-s.shuttingDown:
		http.Error(w, "latchwire: server is shutting down", http.StatusServiceUnavailable)
		return
	default:
	}

	acceptOpts := &websocket.AcceptOptions{}
	if s.opts.CheckOrigin != nil {
		if !s.opts.CheckOrigin(r) {
			http.Error(w, "latchwire: origin not allowed", http.StatusForbidden)
			return
		}
		acceptOpts.InsecureSkipVerify = true
	} else {
		acceptOpts.OriginPatterns = s.opts.OriginPatterns
	}

	ws, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		s.logf(context.Background(), slog.LevelWarn, "latchwire: websocket accept failed", "error", err)
		return
	}
	ws.SetReadLimit(s.opts.MaxMessageBytes)

	conn := newConn(s, ws, context.Background())
	go conn.writePump()

	if !s.handshake(conn) {
		_ = conn.Close(ClosePolicyViolation, "handshake failed")
		return
	}

	s.track(conn)
	s.logf(conn.ctx, slog.LevelInfo, "latchwire: connection accepted")

	s.readLoop(conn)
}

// handshake performs the connect handshake described in the wire protocol:
// read the connect frame, validate its protocol/version and payload,
// construct Params(), run OnConnect, and send "connected" followed by any
// events buffered during OnConnect. It returns false if the handshake
// failed, in which case a connection_error frame has already been written
// (best-effort) and the caller must close the connection.
func (s *Server[C]) handshake(conn *Conn[C]) bool {
	ctx, cancel := context.WithTimeout(conn.ctx, handshakeTimeout)
	defer cancel()

	_, raw, err := conn.ws.Read(ctx)
	if err != nil {
		s.logf(conn.ctx, slog.LevelWarn, "latchwire: handshake read failed", "error", err)
		return false
	}

	var env wire.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		s.sendConnectionErrorDirect(conn, ErrCodeProtocolViolation, "malformed connect envelope")
		return false
	}
	if env.Type != wire.FrameConnect {
		s.sendConnectionErrorDirect(conn, ErrCodeProtocolViolation, "first message on a connection must be a connect frame")
		return false
	}
	if s.opts.ProtocolName != "" && env.Protocol != "" && env.Protocol != s.opts.ProtocolName {
		s.sendConnectionErrorDirect(conn, ErrCodeProtocolViolation, "protocol name mismatch")
		return false
	}
	if s.opts.ProtocolVersion != "" && env.Version != "" && env.Version != s.opts.ProtocolVersion {
		s.sendConnectionErrorDirect(conn, ErrCodeProtocolViolation, "protocol version mismatch")
		return false
	}

	payload := env.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	if err := s.connectVal.ValidateJSON(payload); err != nil {
		s.sendConnectionErrorDirect(conn, ErrCodeInvalidConnectPayload, "connect payload failed schema validation")
		return false
	}

	paramsPtr := reflect.New(s.connectType)
	if err := json.Unmarshal(payload, paramsPtr.Interface()); err != nil {
		s.sendConnectionErrorDirect(conn, ErrCodeInvalidConnectPayload, "malformed connect payload")
		return false
	}
	conn.params = paramsPtr.Elem().Interface().(C)

	if s.onConnect != nil {
		if err := s.callOnConnect(conn); err != nil {
			code, message := errorCodeAndMessage(err, ErrCodeConnectRejected, "connection rejected", s.opts.Debug)
			s.sendConnectionErrorDirect(conn, code, message)
			return false
		}
	}

	connectedEnv := wire.Envelope{
		Type:     wire.FrameConnected,
		Protocol: s.opts.ProtocolName,
		Version:  s.opts.ProtocolVersion,
	}
	if err := conn.flushAfterConnect(connectedEnv); err != nil {
		s.logf(conn.ctx, slog.LevelWarn, "latchwire: failed to flush connected frame", "error", err)
		return false
	}
	return true
}

func (s *Server[C]) callOnConnect(conn *Conn[C]) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logf(conn.ctx, slog.LevelError, "latchwire: OnConnect panicked", "panic", r)
			err = NewError(ErrCodeConnectRejected, "connection rejected")
		}
	}()
	return s.onConnect(conn.ctx, conn)
}

// sendConnectionErrorDirect writes a connection_error frame synchronously,
// bypassing the outbound queue. This is only safe during the handshake:
// before "connected" is sent, nothing else can possibly be writing to the
// socket (event sends are buffered, not queued, until flushAfterConnect
// runs), so there is no concurrent-write hazard.
func (s *Server[C]) sendConnectionErrorDirect(conn *Conn[C], code, message string) {
	env := wire.Envelope{Type: wire.FrameConnectionError, Error: &wire.Error{Code: code, Message: message}}
	if err := conn.writeEnvelope(env); err != nil {
		s.logf(conn.ctx, slog.LevelWarn, "latchwire: failed to write connection_error frame", "error", err)
	}
}

// readLoop reads frames until the connection closes or a protocol violation
// occurs, dispatching each "request" frame to its handler.
func (s *Server[C]) readLoop(conn *Conn[C]) {
	defer conn.Close(CloseNormal, "")

	for {
		_, raw, err := conn.ws.Read(conn.ctx)
		if err != nil {
			return
		}

		var env wire.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			s.protocolViolation(conn, "malformed JSON envelope")
			return
		}

		switch env.Type {
		case wire.FrameRequest:
			if !s.handleRequest(conn, env) {
				s.protocolViolation(conn, "malformed request frame")
				return
			}
		case wire.FrameConnect:
			s.protocolViolation(conn, "a connect frame may only be sent once per connection")
			return
		default:
			s.protocolViolation(conn, fmt.Sprintf("unexpected frame type %q from client", env.Type))
			return
		}
	}
}

func (s *Server[C]) protocolViolation(conn *Conn[C], message string) {
	s.logf(conn.ctx, slog.LevelWarn, "latchwire: protocol violation", "message", message)
	env := wire.Envelope{Type: wire.FrameConnectionError, Error: &wire.Error{Code: ErrCodeProtocolViolation, Message: message}}
	_ = conn.enqueue(env)
}

// handleRequest validates request-frame shape, enforces per-connection
// concurrency limits and request-ID uniqueness, and dispatches the method
// call on its own goroutine. It returns false only for a malformed frame
// (missing id/method), which is a protocol violation the caller must close
// the connection for; everything else (unknown method, invalid payload,
// application error) is reported as a normal "error" response frame.
func (s *Server[C]) handleRequest(conn *Conn[C], env wire.Envelope) bool {
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

func (s *Server[C]) dispatchMethod(ctx context.Context, conn *Conn[C], env wire.Envelope) {
	s.mu.Lock()
	m, ok := s.methods[env.Method]
	s.mu.Unlock()
	if !ok {
		s.sendResponseError(conn, env.ID, ErrCodeMethodNotFound, fmt.Sprintf("unknown method %q", env.Method))
		return
	}

	payload := env.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	if err := m.requestValidator.ValidateJSON(payload); err != nil {
		s.sendResponseError(conn, env.ID, ErrCodeInvalidRequest, "request payload failed schema validation")
		return
	}

	reqPtr := m.adapter.NewRequest()
	if err := json.Unmarshal(payload, reqPtr); err != nil {
		s.sendResponseError(conn, env.ID, ErrCodeInvalidRequest, "malformed request payload")
		return
	}

	resp, wireErr := s.invokeHandler(ctx, conn, m, reqPtr)
	if wireErr != nil {
		s.sendResponseError(conn, env.ID, wireErr.Code, wireErr.Message)
		return
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		s.logf(conn.ctx, slog.LevelError, "latchwire: failed to marshal response", "method", env.Method, "error", err)
		s.sendResponseError(conn, env.ID, ErrCodeInternal, "internal error")
		return
	}

	if m.responseValidator != nil {
		if err := m.responseValidator.ValidateJSON(raw); err != nil {
			s.logf(conn.ctx, slog.LevelError, "latchwire: response failed schema validation (this indicates a Latchwire bug)", "method", env.Method, "error", err)
			s.sendResponseError(conn, env.ID, ErrCodeInternal, "internal error")
			return
		}
	}

	_ = conn.enqueue(wire.Envelope{Type: wire.FrameResponse, ID: env.ID, Payload: raw})
}

// invokeHandler calls the method's handler, recovering from panics and
// mapping the result onto a wire error where applicable.
func (s *Server[C]) invokeHandler(ctx context.Context, conn *Conn[C], m *methodEntry, reqPtr any) (resp any, wireErr *Error) {
	connVal := reflect.ValueOf(conn)

	defer func() {
		if r := recover(); r != nil {
			s.logf(conn.ctx, slog.LevelError, "latchwire: handler panicked", "method", m.name, "panic", r)
			wireErr = NewError(ErrCodeInternal, "internal error")
		}
	}()

	result, err := m.adapter.Call(ctx, connVal, reqPtr)
	if err != nil {
		code, message := errorCodeAndMessage(err, ErrCodeInternal, "internal error", s.opts.Debug)
		if code != ErrCodeInternal {
			s.logf(conn.ctx, slog.LevelDebug, "latchwire: handler returned application error", "method", m.name, "code", code)
		} else {
			s.logf(conn.ctx, slog.LevelError, "latchwire: handler returned error", "method", m.name, "error", err)
		}
		return nil, NewError(code, message)
	}
	return result, nil
}

func (s *Server[C]) sendResponseError(conn *Conn[C], id, code, message string) {
	_ = conn.enqueue(wire.Envelope{Type: wire.FrameError, ID: id, Error: &wire.Error{Code: code, Message: message}})
}

func (s *Server[C]) logf(ctx context.Context, level slog.Level, msg string, args ...any) {
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
