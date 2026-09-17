// Package latchwire is a Go-first, strongly typed, bidirectional protocol
// framework over WebSockets. A developer registers Go handler functions and
// event definitions on a Server; Latchwire reflects over those registrations
// to validate connections and requests against generated JSON Schema, and to
// generate fully type-safe TypeScript, Dart, and Go clients. See the
// package README for the complete walkthrough.
package latchwire

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"sync"

	"github.com/vehmloewff/latchwire/jsonschema"
	"github.com/vehmloewff/latchwire/names"
	"github.com/vehmloewff/latchwire/protocol"
	"github.com/vehmloewff/latchwire/reflectapi"
)

// Default configuration values, used whenever the corresponding Options
// field is left at its zero value.
const (
	DefaultMaxConcurrentRequests = 32
	DefaultOutboundQueueSize     = 256
	DefaultMaxMessageBytes       = 1 << 20 // 1 MiB
)

// Options configures a Server.
type Options struct {
	// ProtocolName and ProtocolVersion identify this API on the wire. A
	// connecting client's "connect" frame is checked against them when both
	// are non-empty.
	ProtocolName    string
	ProtocolVersion string

	// MaxConcurrentRequests bounds how many method handlers may run
	// concurrently on a single connection. Defaults to
	// DefaultMaxConcurrentRequests.
	MaxConcurrentRequests int

	// OutboundQueueSize bounds the number of frames buffered per connection
	// waiting to be written. Defaults to DefaultOutboundQueueSize. On
	// overflow the connection is closed rather than silently dropping a
	// frame.
	OutboundQueueSize int

	// MaxMessageBytes bounds the size of a single incoming WebSocket
	// message. Defaults to DefaultMaxMessageBytes. Oversized messages close
	// the connection.
	MaxMessageBytes int64

	// ValidateResponses, when true, marshals and validates every outgoing
	// response and event payload against its generated JSON Schema before
	// sending it. Intended for tests and development; adds overhead not
	// needed in production because the Go type system already guarantees
	// response shape.
	ValidateResponses bool

	// Debug, when true, includes the underlying Go error string in
	// ErrCodeInternal wire errors. Never enable this in production: it can
	// leak internal details (stack traces, database errors, file paths).
	Debug bool

	// Logger receives structured lifecycle logs (connection accepted,
	// connect rejected, protocol violations, handler panics, transport
	// errors). Connection params are never logged, since they may contain
	// secrets. A nil Logger disables logging.
	Logger *slog.Logger

	// OriginPatterns lists allowed WebSocket origins, matched the same way
	// as github.com/coder/websocket's AcceptOptions.OriginPatterns. Leave
	// nil (the default) to only allow same-origin connections.
	OriginPatterns []string

	// CheckOrigin, if set, overrides OriginPatterns entirely and receives
	// the raw *http.Request to decide whether to accept the connection.
	CheckOrigin func(*http.Request) bool
}

func (o *Options) withDefaults() Options {
	out := *o
	if out.MaxConcurrentRequests <= 0 {
		out.MaxConcurrentRequests = DefaultMaxConcurrentRequests
	}
	if out.OutboundQueueSize <= 0 {
		out.OutboundQueueSize = DefaultOutboundQueueSize
	}
	if out.MaxMessageBytes <= 0 {
		out.MaxMessageBytes = DefaultMaxMessageBytes
	}
	return out
}

// reservedFrameNames may not be used as a method or event name: they would
// be ambiguous against Latchwire's own wire frame "type" discriminator.
var reservedFrameNames = map[string]bool{
	"connect":          true,
	"connected":        true,
	"request":          true,
	"response":         true,
	"error":            true,
	"event":            true,
	"connection_error": true,
}

// Server is a configured Latchwire API, generic over C, the connect
// parameter type declared by the application. Construct one with New,
// register methods and events, optionally set OnConnect, then either serve
// it (ServeHTTP) or generate clients from it (Generate).
type Server[C any] struct {
	opts Options

	mu          sync.Mutex
	finalized   bool
	finalizeErr error

	registry *reflectapi.Registry

	connectType reflect.Type
	connectRef  protocol.TypeRef
	connectVal  *jsonschema.Validator

	methods     map[string]*methodEntry
	methodOrder []string

	events     map[string]*eventEntry
	eventOrder []string

	onConnect func(context.Context, *Conn[C]) error

	ir *protocol.Protocol

	connsMu sync.Mutex
	conns   map[*Conn[C]]struct{}

	shuttingDown chan struct{}
	shutdownOnce sync.Once
}

// New creates a Server whose connect handshake payload is decoded into and
// validated against C. C must be a named, exported struct type (the same
// constraint Latchwire places on every request, response, and event payload
// type); this is checked at finalization time, the first call to ServeHTTP,
// Manifest, or Generate.
func New[C any](opts Options) *Server[C] {
	var zero C
	return &Server[C]{
		opts:         opts.withDefaults(),
		registry:     reflectapi.NewRegistry(),
		connectType:  reflect.TypeOf(zero),
		methods:      map[string]*methodEntry{},
		events:       map[string]*eventEntry{},
		conns:        map[*Conn[C]]struct{}{},
		shuttingDown: make(chan struct{}),
	}
}

// Register declares an RPC method. handler must have exactly the shape:
//
//	func(context.Context, *latchwire.Conn[C], Request) (Response, error)
//
// where Request and Response are named, exported struct types and C matches
// this Server's connect-parameter type. The signature is validated
// immediately; Register never defers validation to the first request.
func (s *Server[C]) Register(name string, handler any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalized {
		return fmt.Errorf("latchwire: cannot register method %q: server is already finalized", name)
	}
	if name == "" {
		return fmt.Errorf("latchwire: method name must not be empty")
	}
	if reservedFrameNames[name] {
		return fmt.Errorf("latchwire: method name %q is reserved", name)
	}
	if _, exists := s.methods[name]; exists {
		return fmt.Errorf("latchwire: method %q is already registered", name)
	}

	wantConnType := reflect.TypeOf((*Conn[C])(nil))
	adapter, err := reflectapi.ValidateHandler(handler, wantConnType)
	if err != nil {
		return fmt.Errorf("latchwire: register method %q: %w", name, err)
	}

	s.methods[name] = &methodEntry{name: name, adapter: adapter}
	s.methodOrder = append(s.methodOrder, name)
	return nil
}

// RegisterEvent declares a server-to-client event, previously created with
// Event[T]. Its payload type T must be a named, exported struct type.
func (s *Server[C]) RegisterEvent(e EventRegistration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalized {
		return fmt.Errorf("latchwire: cannot register event: server is already finalized")
	}

	name := e.eventName()
	if name == "" {
		return fmt.Errorf("latchwire: event name must not be empty")
	}
	if reservedFrameNames[name] {
		return fmt.Errorf("latchwire: event name %q is reserved", name)
	}
	if _, exists := s.events[name]; exists {
		return fmt.Errorf("latchwire: event %q is already registered", name)
	}

	t := e.payloadGoType()
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return fmt.Errorf("latchwire: event %q payload must be a named struct type, got %s", name, t)
	}

	s.events[name] = &eventEntry{name: name, payloadType: t}
	s.eventOrder = append(s.eventOrder, name)
	return nil
}

// OnConnect registers the connection-setup callback, invoked exactly once
// per connection after its connect payload has passed schema validation and
// decoding but before the "connected" frame is sent. Registering a second
// OnConnect handler returns an error. OnConnect itself is optional: a
// server with no OnConnect accepts every schema-valid connection.
func (s *Server[C]) OnConnect(fn func(context.Context, *Conn[C]) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalized {
		return fmt.Errorf("latchwire: cannot register OnConnect: server is already finalized")
	}
	if s.onConnect != nil {
		return fmt.Errorf("latchwire: OnConnect has already been registered")
	}
	s.onConnect = fn
	return nil
}

// finalize resolves every registered type via reflection into the protocol
// IR, checks for naming collisions, and compiles every JSON Schema
// validator exactly once. It runs at most once; subsequent calls return the
// same result. Registration methods reject calls made after finalization.
func (s *Server[C]) finalize() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalized {
		return s.finalizeErr
	}
	s.finalized = true
	s.finalizeErr = s.finalizeLocked()
	return s.finalizeErr
}

func (s *Server[C]) finalizeLocked() error {
	if s.connectType == nil {
		return fmt.Errorf("latchwire: connect parameter type must be a concrete struct type")
	}

	connectRef, err := s.registry.Resolve(s.connectType)
	if err != nil {
		return fmt.Errorf("latchwire: connect params: %w", err)
	}
	if connectRef.Kind != protocol.KindStruct {
		return fmt.Errorf("latchwire: connect params type %s must be a struct", s.connectType)
	}
	s.connectRef = connectRef

	sortedMethods := append([]string(nil), s.methodOrder...)
	sort.Strings(sortedMethods)

	var methods []protocol.Method
	for _, name := range sortedMethods {
		m := s.methods[name]

		reqRef, err := s.registry.Resolve(m.adapter.RequestType)
		if err != nil {
			return fmt.Errorf("latchwire: method %q request type: %w", name, err)
		}
		respRef, err := s.registry.Resolve(m.adapter.ResponseType)
		if err != nil {
			return fmt.Errorf("latchwire: method %q response type: %w", name, err)
		}
		m.requestRef = reqRef
		m.responseRef = respRef
		methods = append(methods, protocol.Method{Name: name, RequestType: reqRef, ResponseType: respRef})
	}

	sortedEvents := append([]string(nil), s.eventOrder...)
	sort.Strings(sortedEvents)

	var events []protocol.Event
	for _, name := range sortedEvents {
		e := s.events[name]
		payloadRef, err := s.registry.Resolve(e.payloadType)
		if err != nil {
			return fmt.Errorf("latchwire: event %q payload type: %w", name, err)
		}
		e.payloadRef = payloadRef
		events = append(events, protocol.Event{Name: name, PayloadType: payloadRef})
	}

	s.ir = &protocol.Protocol{
		Name:        s.opts.ProtocolName,
		Version:     s.opts.ProtocolVersion,
		ConnectType: connectRef,
		Methods:     methods,
		Events:      events,
		Types:       s.registry.Types(),
	}

	if _, err := names.AssignTypeNames(s.ir.Types); err != nil {
		return err
	}
	if _, err := names.BuildMethodTree(sortedMethods); err != nil {
		return err
	}

	connectDoc := jsonschema.BuildDocument(s.ir, connectRef)
	connectVal, err := jsonschema.Compile("latchwire://connect", connectDoc)
	if err != nil {
		return fmt.Errorf("latchwire: compile connect schema: %w", err)
	}
	s.connectVal = connectVal

	for _, name := range sortedMethods {
		m := s.methods[name]

		reqDoc := jsonschema.BuildDocument(s.ir, m.requestRef)
		reqVal, err := jsonschema.Compile("latchwire://method/"+name+"/request", reqDoc)
		if err != nil {
			return fmt.Errorf("latchwire: compile method %q request schema: %w", name, err)
		}
		m.requestValidator = reqVal

		if s.opts.ValidateResponses {
			respDoc := jsonschema.BuildDocument(s.ir, m.responseRef)
			respVal, err := jsonschema.Compile("latchwire://method/"+name+"/response", respDoc)
			if err != nil {
				return fmt.Errorf("latchwire: compile method %q response schema: %w", name, err)
			}
			m.responseValidator = respVal
		}
	}

	if s.opts.ValidateResponses {
		for _, name := range sortedEvents {
			e := s.events[name]
			doc := jsonschema.BuildDocument(s.ir, e.payloadRef)
			v, err := jsonschema.Compile("latchwire://event/"+name, doc)
			if err != nil {
				return fmt.Errorf("latchwire: compile event %q schema: %w", name, err)
			}
			e.validator = v
		}
	}

	return nil
}

// eventValidator returns the compiled validator for a registered event, or
// nil when ValidateResponses is disabled or the event is unknown.
func (s *Server[C]) eventValidator(name string) *jsonschema.Validator {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.events[name]
	if !ok {
		return nil
	}
	return e.validator
}

func (s *Server[C]) track(c *Conn[C]) {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	s.conns[c] = struct{}{}
}

func (s *Server[C]) untrack(c *Conn[C]) {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	delete(s.conns, c)
}

// Close performs a graceful shutdown: it stops accepting new connections,
// closes every active connection, runs their OnClose callbacks, and waits
// for all of that to finish or for ctx to be done, whichever comes first.
func (s *Server[C]) Close(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		close(s.shuttingDown)
	})

	s.connsMu.Lock()
	conns := make([]*Conn[C], 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.connsMu.Unlock()

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, c := range conns {
			wg.Add(1)
			go func(c *Conn[C]) {
				defer wg.Done()
				_ = c.Close(CloseGoingAway, "server shutting down")
			}(c)
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
