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

	"github.com/coder/websocket"

	"github.com/vehmloewff/latchwire/jsonschema"
	"github.com/vehmloewff/latchwire/protocol"
	"github.com/vehmloewff/latchwire/wire"
)

// Manifest is Latchwire's stable, machine-readable description of a
// finalized API: enough for a code generator to build TypeScript, Dart, or
// Go clients (or anything else) without ever inspecting Go reflection or
// source code directly. LatchwireVersion is bumped whenever the manifest
// shape itself changes incompatibly.
type Manifest struct {
	LatchwireVersion int    `json:"latchwireVersion"`
	Version          string `json:"version"`
	// Connect is omitted in the current protocol. It remains as a deprecated
	// field so manifests produced by older callers can still be decoded.
	Connect map[string]any   `json:"connect,omitempty"`
	Methods []ManifestMethod `json:"methods"`
	Event   map[string]any   `json:"event,omitempty"`
	Types   map[string]any   `json:"types"`

	// IR is the complete, lossless normalized intermediate representation
	// the reflection stage produced — exactly what every code generator
	// consumes. The Methods[].Request/Response/Event JSON Schema documents
	// above are a derived, human-and-tool-friendly
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
//
// Deprecated: manifests now contain one Event schema instead of named events.
type ManifestEvent struct {
	Name    string         `json:"name"`
	Payload map[string]any `json:"payload"`
}

// Manifest finalizes the server (if not already finalized) and returns its
// complete protocol manifest. Manifest generation is deterministic: the
// same set of registrations always produces byte-identical JSON.
func (s *Server[S]) Manifest() (*Manifest, error) {
	if err := s.finalize(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m := &Manifest{
		LatchwireVersion: 2,
		Version:          s.opts.ProtocolVersion,
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
	if eventRef, ok := s.ir.EventRef(); ok {
		m.Event = jsonschema.BuildDocument(s.ir, eventRef)
	}
	return m, nil
}

// WriteManifest writes the server's protocol manifest to w as indented
// JSON.
func (s *Server[S]) WriteManifest(w io.Writer) error {
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

	conn := newConn(s, ws, r)
	go conn.writePump()

	s.track(conn)
	if s.onConnect != nil {
		state, err := s.callOnConnect(conn)
		if err != nil {
			code, message := errorCodeAndMessage(err, ErrCodeConnectRejected, "connection rejected", s.opts.Debug)
			_ = conn.enqueue(wire.Envelope{
				Type:  wire.FrameConnectionError,
				Error: &wire.Error{Code: code, Message: message},
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
	s.logf(conn.ctx, slog.LevelInfo, "latchwire: connection accepted")

	s.readLoop(conn)
}

func (s *Server[S]) callOnConnect(conn *Conn) (state any, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logf(conn.ctx, slog.LevelError, "latchwire: OnConnect panicked", "panic", r)
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
		default:
			s.protocolViolation(conn, fmt.Sprintf("unexpected frame type %q from client", env.Type))
			return
		}
	}
}

func (s *Server[S]) protocolViolation(conn *Conn, message string) {
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
func (s *Server[S]) invokeHandler(ctx context.Context, conn *Conn, state any, m *methodEntry, reqPtr any) (resp any, wireErr *Error) {
	stateVal := reflect.ValueOf(state)
	if !stateVal.IsValid() {
		stateVal = reflect.Zero(reflect.TypeOf((*S)(nil)).Elem())
	}

	defer func() {
		if r := recover(); r != nil {
			s.logf(conn.ctx, slog.LevelError, "latchwire: handler panicked", "method", m.name, "panic", r)
			wireErr = NewError(ErrCodeInternal, "internal error")
		}
	}()

	result, err := m.adapter.Call(ctx, stateVal, reqPtr)
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

func (s *Server[S]) sendResponseError(conn *Conn, id, code, message string) {
	_ = conn.enqueue(wire.Envelope{Type: wire.FrameError, ID: id, Error: &wire.Error{Code: code, Message: message}})
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
