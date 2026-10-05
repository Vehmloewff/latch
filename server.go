// Package latch is a Go-first, strongly typed, bidirectional protocol
// framework over WebSockets. A developer registers Go handler functions and
// event definitions on a Server; Latch reflects over those registrations to
// build the binary protocol IR and generate fully type-safe TypeScript, Dart,
// and Go clients. See the package README for the complete walkthrough.
package latch

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"sync"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
	"github.com/vehmloewff/latch/reflectapi"
	"github.com/vehmloewff/latch/wire"
)

// Default configuration values, used whenever the corresponding Options
// field is left at its zero value.
const (
	DefaultMaxConcurrentRequests = 32
	DefaultOutboundQueueSize     = 256
	DefaultMaxMessageBytes       = 1 << 20 // 1 MiB
	DefaultMaxDecodeBytes        = wire.DefaultMaxDecodeBytes
)

// Options configures a Server.
type Options struct {
	// ProtocolVersion is included in generated client URLs as the "version"
	// query parameter and recorded in manifests.
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

	// MaxDecodeBytes bounds aggregate conservative allocation accounting when
	// decoding each request payload, independently of MaxMessageBytes. This
	// is not an exact heap or process-memory cap. Non-positive values default
	// to DefaultMaxDecodeBytes (64 MiB). Payloads exceeding the budget receive
	// ErrCodeInvalidRequest without invoking the handler.
	MaxDecodeBytes int64

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
	if out.MaxDecodeBytes <= 0 {
		out.MaxDecodeBytes = DefaultMaxDecodeBytes
	}
	return out
}

// Server is a configured Latch API. Its one server-to-client event type
// is inferred from the generic Emitter supplied to OnConnect.
// Construct one with New, register methods, optionally set OnConnect, then
// either serve it (ServeHTTP) or generate clients from it with one of the
// target-specific GenerateTypeScript, GenerateDart, or GenerateGo methods.
type Server[S any] struct {
	opts Options

	mu          sync.Mutex
	finalized   bool
	finalizeErr error

	registry *reflectapi.Registry

	eventType reflect.Type
	eventRef  protocol.TypeRef

	methods     map[string]*methodEntry
	methodOrder []string

	onConnect        func(context.Context, any, *Conn) (any, error)
	onConnectEmitter emitterRegistration
	onDisconnect     func(context.Context, any)

	ir *protocol.Protocol

	connsMu sync.Mutex
	conns   map[*Conn]struct{}
	states  map[*Conn]any

	shuttingDown chan struct{}
	shutdownOnce sync.Once
}

// New creates a Server generic over S, the per-connection application state.
// The event payload type is inferred from the concrete Emitter used by
// OnConnect.
func New[S any](opts Options) *Server[S] {
	return &Server[S]{
		opts:         opts.withDefaults(),
		registry:     reflectapi.NewRegistry(),
		methods:      map[string]*methodEntry{},
		conns:        map[*Conn]struct{}{},
		states:       map[*Conn]any{},
		shuttingDown: make(chan struct{}),
	}
}

// Register declares an RPC method. handler must have exactly the shape:
//
//	func(context.Context, S, Request) (Response, error)
//
// where Request and Response are named, exported struct types. The signature
// is validated immediately and panics on invalid configuration; Register
// never defers validation to the first request.
func (s *Server[S]) Register(name string, handler any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalized {
		panic(fmt.Errorf("latch: cannot register method %q: server is already finalized", name))
	}
	if !names.IsSnakeCase(name) {
		panic(fmt.Errorf("latch: method name %q must be snake_case", name))
	}
	if _, exists := s.methods[name]; exists {
		panic(fmt.Errorf("latch: method %q is already registered", name))
	}

	wantStateType := reflect.TypeOf((*S)(nil)).Elem()
	adapter, err := reflectapi.ValidateStateHandler(handler, wantStateType)
	if err != nil {
		panic(fmt.Errorf("latch: register method %q: %w", name, err))
	}

	s.methods[name] = &methodEntry{name: name, adapter: adapter}
	s.methodOrder = append(s.methodOrder, name)
}

// OnConnect registers the connection callback, invoked exactly once per
// connection after the HTTP upgrade succeeds. The callback may use the
// connection's copied HTTP request through Conn.Request(). Its event
// parameter is a concrete Emitter[E]:
//
//	func(context.Context, Emitter[E], *Conn) S
//	func(context.Context, Emitter[E], *Conn) (S, error)
//
// Invalid callbacks and duplicate registrations panic immediately. OnConnect
// itself is optional.
func (s *Server[S]) OnConnect(fn any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalized {
		panic(fmt.Errorf("latch: cannot register OnConnect: server is already finalized"))
	}
	if s.onConnect != nil {
		panic(fmt.Errorf("latch: OnConnect has already been registered"))
	}
	adapted, eventType, emitter, err := adaptOnConnect[S](fn)
	if err != nil {
		panic(err)
	}
	s.onConnect = adapted
	s.eventType = eventType
	s.onConnectEmitter = emitter
}

// OnDisconnect registers a callback invoked once after a connection closes.
// Invalid callbacks and duplicate registrations panic immediately.
// The callback receives the State returned by OnConnect:
//
//	func(context.Context, S)
//	func(context.Context, S) error
func (s *Server[S]) OnDisconnect(fn any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalized {
		panic(fmt.Errorf("latch: cannot register OnDisconnect: server is already finalized"))
	}
	if s.onDisconnect != nil {
		panic(fmt.Errorf("latch: OnDisconnect has already been registered"))
	}
	adapted, err := adaptOnDisconnect[S](fn)
	if err != nil {
		panic(err)
	}
	s.onDisconnect = adapted
}

func adaptOnDisconnect[S any](fn any) (func(context.Context, any), error) {
	if fn == nil {
		return nil, fmt.Errorf("latch: OnDisconnect handler must not be nil")
	}
	v := reflect.ValueOf(fn)
	t := v.Type()
	stateType := reflect.TypeOf((*S)(nil)).Elem()
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if t.Kind() != reflect.Func || t.IsVariadic() ||
		(t.NumIn() != 1 && t.NumIn() != 2) ||
		(t.NumIn() == 2 && t.In(0) != ctxType) ||
		t.In(t.NumIn()-1) != stateType ||
		(t.NumOut() != 0 && (t.NumOut() != 1 || t.Out(0) != errorType)) {
		return nil, fmt.Errorf("latch: OnDisconnect handler must accept State, optionally preceded by context.Context")
	}
	return func(ctx context.Context, state any) {
		stateVal := reflect.ValueOf(state)
		if !stateVal.IsValid() {
			stateVal = reflect.Zero(stateType)
		}
		args := []reflect.Value{stateVal}
		if t.NumIn() == 2 {
			args = []reflect.Value{reflect.ValueOf(ctx), stateVal}
		}
		outs := v.Call(args)
		if len(outs) == 1 && !outs[0].IsNil() {
			// Disconnect callbacks cannot report an error to the peer; the
			// callback is still isolated from connection cleanup.
		}
	}, nil
}

func adaptOnConnect[S any](fn any) (func(context.Context, any, *Conn) (any, error), reflect.Type, emitterRegistration, error) {
	if fn == nil {
		return nil, nil, nil, fmt.Errorf("latch: OnConnect handler must not be nil")
	}
	v := reflect.ValueOf(fn)
	t := v.Type()
	stateType := reflect.TypeOf((*S)(nil)).Elem()
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if t.Kind() != reflect.Func || t.IsVariadic() ||
		(t.NumOut() != 1 && t.NumOut() != 2) ||
		t.Out(0) != stateType ||
		(t.NumOut() == 2 && t.Out(1) != errorType) {
		return nil, nil, nil, fmt.Errorf("latch: OnConnect handler must return S or (S, error)")
	}
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	connType := reflect.TypeOf((*Conn)(nil))
	if t.NumIn() != 3 {
		return nil, nil, nil, fmt.Errorf("latch: OnConnect handler must accept context.Context, Emitter[E], and *Conn")
	}
	if t.In(0) != ctxType {
		return nil, nil, nil, fmt.Errorf("latch: OnConnect handler's first argument must be context.Context")
	}
	if t.In(2) != connType {
		return nil, nil, nil, fmt.Errorf("latch: OnConnect handler's third argument must be *Conn")
	}
	prototype, ok := reflect.New(t.In(1)).Elem().Interface().(emitterRegistration)
	if !ok {
		return nil, nil, nil, fmt.Errorf("latch: OnConnect handler's second argument must be Emitter[E]")
	}
	return func(ctx context.Context, emitter any, conn *Conn) (any, error) {
		outs := v.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(emitter), reflect.ValueOf(conn)})
		var err error
		if len(outs) == 2 && !outs[1].IsNil() {
			err = outs[1].Interface().(error)
		}
		return outs[0].Interface(), err
	}, prototype.emitterType(), prototype, nil
}

// finalize resolves every registered type via reflection into the protocol
// IR and checks for naming collisions. It runs at most once; subsequent calls
// return the same result. Registration methods reject calls made after
// finalization.
func (s *Server[S]) finalize() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finalized {
		return s.finalizeErr
	}
	s.finalized = true
	s.finalizeErr = s.finalizeLocked()
	return s.finalizeErr
}

func (s *Server[S]) finalizeLocked() error {
	if s.eventType == nil {
		return fmt.Errorf("latch: event type must be a concrete struct type")
	}

	eventRef, err := s.registry.Resolve(s.eventType)
	if err != nil {
		return fmt.Errorf("latch: event type: %w", err)
	}
	if eventRef.Kind != protocol.KindStruct {
		return fmt.Errorf("latch: event type %s must be a struct", s.eventType)
	}
	s.eventRef = eventRef

	sortedMethods := append([]string(nil), s.methodOrder...)
	sort.Strings(sortedMethods)

	var methods []protocol.Method
	for _, name := range sortedMethods {
		m := s.methods[name]

		reqRef, err := s.registry.Resolve(m.adapter.RequestType)
		if err != nil {
			return fmt.Errorf("latch: method %q request type: %w", name, err)
		}
		respRef, err := s.registry.Resolve(m.adapter.ResponseType)
		if err != nil {
			return fmt.Errorf("latch: method %q response type: %w", name, err)
		}
		m.requestRef = reqRef
		m.responseRef = respRef
		methods = append(methods, protocol.Method{Name: name, RequestType: reqRef, ResponseType: respRef})
	}

	s.ir = &protocol.Protocol{
		Version:   s.opts.ProtocolVersion,
		Methods:   methods,
		EventType: eventRef,
		Types:     s.registry.Types(),
	}

	if _, err := names.AssignTypeNames(s.ir.Types); err != nil {
		return err
	}

	return nil
}

// track admits a connection only while shutdown has not begun. A WebSocket
// upgrade can complete before its ServeHTTP goroutine gets to this point.
func (s *Server[S]) track(c *Conn) bool {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	select {
	case <-s.shuttingDown:
		return false
	case <-c.closed:
		return false
	default:
	}
	s.conns[c] = struct{}{}
	return true
}

// setState pairs a successful OnConnect result with its connection. If Close
// raced OnConnect, untrack has already run and the state still needs cleanup.
func (s *Server[S]) setState(c *Conn, state any) {
	s.connsMu.Lock()
	_, tracked := s.conns[c]
	if tracked {
		s.states[c] = state
	}
	s.connsMu.Unlock()
	if !tracked && s.onDisconnect != nil {
		s.callOnDisconnect(c.Context(), state)
	}
}

func (s *Server[S]) untrack(c *Conn) {
	s.connsMu.Lock()
	delete(s.conns, c)
	state, hasState := s.states[c]
	delete(s.states, c)
	s.connsMu.Unlock()

	if hasState && s.onDisconnect != nil {
		s.callOnDisconnect(c.Context(), state)
	}
}

func (s *Server[S]) callOnDisconnect(ctx context.Context, state any) {
	defer func() {
		if r := recover(); r != nil {
			s.logf(ctx, slog.LevelError, "latch: OnDisconnect panicked", "panic", r)
		}
	}()
	s.onDisconnect(ctx, state)
}

// Close performs a graceful shutdown: it stops accepting new connections,
// closes every active connection, runs their OnClose callbacks, and waits
// for all of that to finish or for ctx to be done, whichever comes first.
func (s *Server[S]) Close(ctx context.Context) error {
	s.connsMu.Lock()
	s.shutdownOnce.Do(func() {
		close(s.shuttingDown)
	})
	conns := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.connsMu.Unlock()

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, c := range conns {
			wg.Add(1)
			go func(c *Conn) {
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
