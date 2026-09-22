package latch_test

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/testutil"
	"github.com/vehmloewff/latch/wire"
)

type coreState struct {
	Token string
}

type coreEvent struct {
	Kind string `latch:"1"`
}
type coreRequest struct {
	Value int `latch:"1"`
}
type coreResponse struct {
	Value int `latch:"1"`
}

func newCoreServer(t *testing.T, opts latch.Options) *latch.Server[coreState] {
	t.Helper()
	server := latch.New[coreState](opts)
	server.OnConnect(func(context.Context, latch.Emitter[coreEvent], *latch.Conn) (coreState, error) {
		return coreState{Token: "connected"}, nil
	})
	return server
}

func serveCore(t *testing.T, server *latch.Server[coreState], query string) (*httptest.Server, *testutil.Client) {
	t.Helper()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + query
	return httpServer, testutil.Dial(t, url)
}

func sendCoreRequest(t *testing.T, client *testutil.Client, id, method string, value int) {
	t.Helper()
	client.Request(id, method, coreRequest{Value: value})
}

// rawEnvelope deliberately skips wire.Envelope.MarshalBinary validation so the
// server can be tested with envelope shapes that a well-formed client cannot
// produce, such as a request without an ID or method.
func rawEnvelope(frameCode byte, version, id, method, event string, payload []byte, message, code string) []byte {
	raw := []byte{1, frameCode}
	appendBlob := func(value []byte) {
		var encoded [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(encoded[:], uint64(len(value)))
		raw = append(raw, encoded[:n]...)
		raw = append(raw, value...)
	}
	appendBlob([]byte(version))
	appendBlob([]byte(id))
	appendBlob([]byte(method))
	appendBlob([]byte(event))
	appendBlob(payload)
	appendBlob([]byte(message))
	appendBlob([]byte(code))
	return raw
}

func requireWireError(t *testing.T, client *testutil.Client, id, code string) wire.Envelope {
	t.Helper()
	env := client.Recv()
	if env.Type != wire.FrameError {
		t.Fatalf("frame type = %q, want error", env.Type)
	}
	if env.ID != id || env.ErrorCode != code {
		t.Fatalf("error frame = %+v, want id %q and code %q", env, id, code)
	}
	return env
}

func readCoreResponse(t *testing.T, client *testutil.Client, wantID string) coreResponse {
	t.Helper()
	env := client.Recv()
	if env.Type != wire.FrameResponse || env.ID != wantID {
		t.Fatalf("response frame = %+v, want response id %q", env, wantID)
	}
	var response coreResponse
	if err := wire.Decode(env.Payload, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func TestServerDispatchesUnknownMethodAndInvalidPayload(t *testing.T) {
	t.Run("unknown method", func(t *testing.T) {
		server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
		server.Register("known_method", func(_ context.Context, _ coreState, req coreRequest) (coreResponse, error) {
			return coreResponse{Value: req.Value}, nil
		})
		_, client := serveCore(t, server, "?version=1")

		sendCoreRequest(t, client, "unknown-1", "missing_method", 1)
		env := requireWireError(t, client, "unknown-1", latch.ErrCodeMethodNotFound)
		if !strings.Contains(env.Error, "missing_method") {
			t.Fatalf("unknown-method message = %q, want method name", env.Error)
		}
	})

	t.Run("invalid payload", func(t *testing.T) {
		server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
		server.Register("known_method", func(_ context.Context, _ coreState, req coreRequest) (coreResponse, error) {
			return coreResponse{Value: req.Value}, nil
		})
		_, client := serveCore(t, server, "?version=1")

		client.Send(wire.Envelope{
			Type:    wire.FrameRequest,
			ID:      "invalid-1",
			Method:  "known_method",
			Payload: []byte{wire.KindString, 0}, // not a struct-shaped coreRequest
		})
		requireWireError(t, client, "invalid-1", latch.ErrCodeInvalidRequest)
	})
}

func TestServerRejectsMalformedFramesAndRequestShapes(t *testing.T) {
	tests := []struct {
		name string
		send func(*testutil.Client)
	}{
		{
			name: "malformed binary envelope",
			send: func(client *testutil.Client) { client.SendRaw([]byte{0xff, 0x00}) },
		},
		{
			name: "request missing id",
			send: func(client *testutil.Client) {
				client.SendRaw(rawEnvelope(3, "", "", "known_method", "", []byte{wire.KindStruct, 0}, "", ""))
			},
		},
		{
			name: "request missing method",
			send: func(client *testutil.Client) {
				client.SendRaw(rawEnvelope(3, "", "missing-method", "", "", []byte{wire.KindStruct, 0}, "", ""))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
			server.Register("known_method", func(_ context.Context, _ coreState, req coreRequest) (coreResponse, error) {
				return coreResponse{Value: req.Value}, nil
			})
			_, client := serveCore(t, server, "?version=1")
			tt.send(client)

			env := client.Recv()
			if env.Type != wire.FrameConnectionError || env.ErrorCode != latch.ErrCodeProtocolViolation {
				t.Fatalf("frame = %+v, want protocol violation connection_error", env)
			}
			if _, err := client.TryRecv(500 * time.Millisecond); err == nil {
				t.Fatal("connection remained open after malformed frame")
			}
		})
	}
}

func TestServerDispatchesApplicationErrorsAndRecoversPanics(t *testing.T) {
	server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
	server.Register("application_error", func(_ context.Context, _ coreState, _ coreRequest) (coreResponse, error) {
		return coreResponse{}, latch.NewError("invalid_input", "value cannot be used")
	})
	server.Register("panic_method", func(_ context.Context, _ coreState, _ coreRequest) (coreResponse, error) {
		panic("handler exploded")
	})
	_, client := serveCore(t, server, "?version=1")

	sendCoreRequest(t, client, "app-error", "application_error", 1)
	env := requireWireError(t, client, "app-error", "invalid_input")
	if env.Error != "value cannot be used" {
		t.Fatalf("application error message = %q", env.Error)
	}

	sendCoreRequest(t, client, "panic", "panic_method", 1)
	env = requireWireError(t, client, "panic", latch.ErrCodeInternal)
	if env.Error != "internal error" {
		t.Fatalf("panic error message = %q, want generic internal error", env.Error)
	}

	sendCoreRequest(t, client, "after-panic", "application_error", 1)
	requireWireError(t, client, "after-panic", "invalid_input")
}

func TestServerRejectsDuplicateRequestIDsWhileFirstIsInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
	server.Register("blocking_method", func(_ context.Context, _ coreState, req coreRequest) (coreResponse, error) {
		close(started)
		<-release
		return coreResponse{Value: req.Value}, nil
	})
	_, client := serveCore(t, server, "?version=1")

	sendCoreRequest(t, client, "same-id", "blocking_method", 7)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking handler did not start")
	}
	sendCoreRequest(t, client, "same-id", "blocking_method", 8)
	requireWireError(t, client, "same-id", latch.ErrCodeDuplicateRequestID)

	close(release)
	response := readCoreResponse(t, client, "same-id")
	if response.Value != 7 {
		t.Fatalf("first response = %+v, want value 7", response)
	}
}

func TestServerLimitsConcurrencyPerConnection(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var callsMu sync.Mutex
	calls := 0

	server := newCoreServer(t, latch.Options{ProtocolVersion: "1", MaxConcurrentRequests: 1})
	server.Register("ordered_method", func(_ context.Context, _ coreState, req coreRequest) (coreResponse, error) {
		callsMu.Lock()
		calls++
		callNumber := calls
		callsMu.Unlock()
		if callNumber == 1 {
			close(firstStarted)
			<-releaseFirst
		} else {
			close(secondStarted)
		}
		return coreResponse{Value: req.Value}, nil
	})
	_, client := serveCore(t, server, "?version=1")

	sendCoreRequest(t, client, "first", "ordered_method", 1)
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first handler did not start")
	}
	sendCoreRequest(t, client, "second", "ordered_method", 2)

	select {
	case <-secondStarted:
		t.Fatal("second handler started before the first handler released the concurrency slot")
	case <-time.After(150 * time.Millisecond):
	}

	close(releaseFirst)
	responses := map[string]coreResponse{}
	for len(responses) < 2 {
		env := client.Recv()
		if env.Type != wire.FrameResponse {
			t.Fatalf("unexpected frame while reading responses: %+v", env)
		}
		var response coreResponse
		if err := wire.Decode(env.Payload, &response); err != nil {
			t.Fatalf("decode response %q: %v", env.ID, err)
		}
		responses[env.ID] = response
	}
	if responses["first"].Value != 1 || responses["second"].Value != 2 {
		t.Fatalf("responses = %+v, want first=1 and second=2", responses)
	}
}

func TestServerCancelsInFlightHandlerWhenClientDisconnects(t *testing.T) {
	handlerStarted := make(chan struct{})
	handlerCanceled := make(chan struct{})
	server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
	server.Register("wait_method", func(ctx context.Context, _ coreState, _ coreRequest) (coreResponse, error) {
		close(handlerStarted)
		<-ctx.Done()
		close(handlerCanceled)
		return coreResponse{}, ctx.Err()
	})
	_, client := serveCore(t, server, "?version=1")

	sendCoreRequest(t, client, "cancel-me", "wait_method", 1)
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("cancellable handler did not start")
	}
	client.Close()

	select {
	case <-handlerCanceled:
	case <-time.After(time.Second):
		t.Fatal("handler context was not canceled after client disconnect")
	}
}

func TestConnLifecycleCallbacksContextAndRequest(t *testing.T) {
	connectDone := make(chan struct{})
	connDone := make(chan struct{})
	disconnectDone := make(chan coreState, 1)
	closeEvents := make(chan string, 3)
	var observedRequest *http.Request
	var observedConn *latch.Conn
	var observedConnectContext context.Context
	var observedConnContext context.Context
	var observedMu sync.Mutex

	server := latch.New[coreState](latch.Options{ProtocolVersion: "1"})
	server.OnConnect(func(ctx context.Context, _ latch.Emitter[coreEvent], conn *latch.Conn) (coreState, error) {
		observedMu.Lock()
		observedRequest = conn.Request()
		observedConn = conn
		observedConnectContext = ctx
		observedConnContext = conn.Context()
		observedMu.Unlock()

		conn.OnClose(func() { closeEvents <- "first" })
		conn.OnClose(func() { panic("callback panic must be isolated") })
		conn.OnClose(func() { closeEvents <- "last" })
		go func() {
			<-conn.Done()
			close(connDone)
		}()
		close(connectDone)
		return coreState{Token: "state-from-connect"}, nil
	})
	server.OnDisconnect(func(ctx context.Context, state coreState) {
		if ctx.Err() == nil {
			disconnectDone <- coreState{Token: "wrong-context"}
			return
		}
		disconnectDone <- state
	})

	_, client := serveCore(t, server, "?version=1&client=lifecycle")
	select {
	case <-connectDone:
	case <-time.After(time.Second):
		t.Fatal("OnConnect did not run")
	}
	client.Close()

	select {
	case <-connDone:
	case <-time.After(time.Second):
		t.Fatal("Conn context was not canceled")
	}
	select {
	case state := <-disconnectDone:
		if state.Token != "state-from-connect" {
			t.Fatalf("OnDisconnect state = %+v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("OnDisconnect did not run")
	}

	observedMu.Lock()
	request := observedRequest
	conn := observedConn
	connectContext := observedConnectContext
	connContext := observedConnContext
	observedMu.Unlock()
	if request == nil || request.URL.Query().Get("client") != "lifecycle" {
		t.Fatalf("Conn.Request() = %#v, want copied upgrade request", request)
	}
	if request.Context() != connectContext || connContext != connectContext {
		t.Fatal("OnConnect context and Conn context/request context were not preserved")
	}
	if err := connContext.Err(); err == nil {
		t.Fatal("Conn context remained active after disconnect")
	}

	select {
	case event := <-closeEvents:
		if event != "last" {
			t.Fatalf("first OnClose event = %q, want LIFO callback order", event)
		}
	case <-time.After(time.Second):
		t.Fatal("last OnClose callback did not run")
	}
	select {
	case event := <-closeEvents:
		if event != "first" {
			t.Fatalf("second OnClose event = %q, want first callback after last", event)
		}
	case <-time.After(time.Second):
		t.Fatal("OnClose callback after panic did not run")
	}

	conn.OnClose(func() { closeEvents <- "late" })
	select {
	case event := <-closeEvents:
		if event != "late" {
			t.Fatalf("late OnClose event = %q, want immediate callback", event)
		}
	case <-time.After(time.Second):
		t.Fatal("OnClose callback registered after closure did not run")
	}
}

func TestServerAdmissionOriginAndShutdown(t *testing.T) {
	t.Run("missing version", func(t *testing.T) {
		server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
		httpServer := httptest.NewServer(server)
		t.Cleanup(httpServer.Close)
		response, err := http.Get(httpServer.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("origin check", func(t *testing.T) {
		var originMu sync.Mutex
		var origins []string
		server := newCoreServer(t, latch.Options{
			ProtocolVersion: "1",
			CheckOrigin: func(request *http.Request) bool {
				originMu.Lock()
				origins = append(origins, request.Header.Get("Origin"))
				originMu.Unlock()
				return request.Header.Get("Origin") == "https://allowed.example"
			},
		})
		httpServer := httptest.NewServer(server)
		t.Cleanup(httpServer.Close)
		url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "?version=1"

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": []string{"https://blocked.example"}},
		})
		cancel()
		if err == nil {
			t.Fatal("blocked origin unexpectedly connected")
		}
		if response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("blocked origin response = %#v, err = %v", response, err)
		}

		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": []string{"https://allowed.example"}},
		})
		cancel()
		if err != nil {
			t.Fatalf("allowed origin dial: %v", err)
		}
		_ = ws.Close(websocket.StatusNormalClosure, "")

		originMu.Lock()
		gotOrigins := append([]string(nil), origins...)
		originMu.Unlock()
		if !reflect.DeepEqual(gotOrigins, []string{"https://blocked.example", "https://allowed.example"}) {
			t.Fatalf("origins seen by CheckOrigin = %v", gotOrigins)
		}
	})

	t.Run("close stops new connections and closes active ones", func(t *testing.T) {
		disconnected := make(chan struct{})
		server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
		server.OnDisconnect(func(coreState) { close(disconnected) })
		httpServer, client := serveCore(t, server, "?version=1")

		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := server.Close(closeCtx); err != nil {
			cancel()
			t.Fatalf("Server.Close: %v", err)
		}
		cancel()
		select {
		case <-disconnected:
		case <-time.After(time.Second):
			t.Fatal("OnDisconnect did not run during Server.Close")
		}
		if _, err := client.TryRecv(500 * time.Millisecond); err == nil {
			t.Fatal("active client remained connected after Server.Close")
		}

		response, err := http.Get(httpServer.URL + "?version=1")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("post-shutdown status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
		}
	})
}

func TestServeHTTPRejectsMisconfiguredServer(t *testing.T) {
	server := latch.New[coreState](latch.Options{ProtocolVersion: "1"})
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	response, err := http.Get(httpServer.URL + "?version=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
}

func TestOnConnectRejectionAndPanicCloseTheConnection(t *testing.T) {
	tests := []struct {
		name   string
		handle func(context.Context, latch.Emitter[coreEvent], *latch.Conn) (coreState, error)
	}{
		{
			name: "returned error",
			handle: func(context.Context, latch.Emitter[coreEvent], *latch.Conn) (coreState, error) {
				return coreState{}, latch.NewError("not_ready", "not ready")
			},
		},
		{
			name: "panic",
			handle: func(context.Context, latch.Emitter[coreEvent], *latch.Conn) (coreState, error) {
				panic("connect panic")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := latch.New[coreState](latch.Options{ProtocolVersion: "1"})
			server.OnConnect(tt.handle)
			httpServer := httptest.NewServer(server)
			t.Cleanup(httpServer.Close)
			client := testutil.Dial(t, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"?version=1")

			env := client.Recv()
			if env.Type != wire.FrameConnectionError || env.ErrorCode != latch.ErrCodeConnectRejected {
				t.Fatalf("rejection frame = %+v, want connect_rejected", env)
			}
			if _, err := client.TryRecv(500 * time.Millisecond); err == nil {
				t.Fatal("rejected connection remained open")
			}
		})
	}
}

func TestServerDebugFlagKeepsInternalErrorsGenericByDefault(t *testing.T) {
	server := newCoreServer(t, latch.Options{ProtocolVersion: "1", Debug: false})
	server.Register("ordinary_error", func(_ context.Context, _ coreState, _ coreRequest) (coreResponse, error) {
		return coreResponse{}, errors.New("database password must not cross the wire")
	})
	_, client := serveCore(t, server, "?version=1")
	sendCoreRequest(t, client, "generic", "ordinary_error", 1)
	env := requireWireError(t, client, "generic", latch.ErrCodeInternal)
	if strings.Contains(env.Error, "database password") {
		t.Fatalf("internal error leaked underlying error: %q", env.Error)
	}
}
