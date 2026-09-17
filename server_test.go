package latchwire_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vehmloewff/latchwire"
	"github.com/vehmloewff/latchwire/testutil"
	"github.com/vehmloewff/latchwire/wire"
)

type ConnectParams struct {
	Token string `json:"token" jsonschema:"minLength=1"`
}

type AddRequest struct {
	A int `json:"a"`
	B int `json:"b"`
}

type AddResponse struct {
	Result int `json:"result"`
}

type Tick struct {
	Value int `json:"value"`
}

func newAddServer(t *testing.T) *latchwire.Server[ConnectParams] {
	t.Helper()
	srv := latchwire.New[ConnectParams](latchwire.Options{
		ProtocolName:    "demo",
		ProtocolVersion: "1",
	})
	err := srv.Register("math.add", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		return AddResponse{Result: req.A + req.B}, nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return srv
}

// wsURL launches srv behind an httptest.Server and returns its "ws://" URL.
func wsURL[C any](t *testing.T, srv *latchwire.Server[C]) string {
	t.Helper()
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return "ws" + strings.TrimPrefix(hs.URL, "http")
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func decodeInto(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
}

func TestHandshakeAndBasicRequest(t *testing.T) {
	srv := newAddServer(t)
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	connected := c.Connect("demo", "1", ConnectParams{Token: "abc"})
	if connected.Type != wire.FrameConnected {
		t.Fatalf("expected connected frame, got %+v", connected)
	}
	if connected.Protocol != "demo" || connected.Version != "1" {
		t.Fatalf("connected frame missing protocol metadata: %+v", connected)
	}

	c.Request("1", "math.add", AddRequest{A: 2, B: 3})
	resp := c.Recv()
	if resp.Type != wire.FrameResponse || resp.ID != "1" {
		t.Fatalf("expected response id=1, got %+v", resp)
	}
	var out AddResponse
	decodeInto(t, resp.Payload, &out)
	if out.Result != 5 {
		t.Fatalf("expected result 5, got %d", out.Result)
	}
}

func TestInvalidConnectPayloadRejected(t *testing.T) {
	srv := newAddServer(t)
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	resp := c.Connect("demo", "1", map[string]any{"token": ""}) // fails minLength=1
	if resp.Type != wire.FrameConnectionError {
		t.Fatalf("expected connection_error, got %+v", resp)
	}
	if resp.Error == nil || resp.Error.Code != latchwire.ErrCodeInvalidConnectPayload {
		t.Fatalf("expected invalid_connect_payload, got %+v", resp.Error)
	}

	if _, err := c.TryRecv(500 * time.Millisecond); err == nil {
		t.Fatalf("expected connection to be closed after invalid connect")
	}
}

func TestFirstMessageMustBeConnect(t *testing.T) {
	srv := newAddServer(t)
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Request("1", "math.add", AddRequest{A: 1, B: 1})

	resp := c.Recv()
	if resp.Type != wire.FrameConnectionError {
		t.Fatalf("expected connection_error, got %+v", resp)
	}
	if resp.Error.Code != latchwire.ErrCodeProtocolViolation {
		t.Fatalf("expected protocol_violation, got %+v", resp.Error)
	}
}

func TestDuplicateConnectRejected(t *testing.T) {
	srv := newAddServer(t)
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	connected := c.Connect("demo", "1", ConnectParams{Token: "abc"})
	if connected.Type != wire.FrameConnected {
		t.Fatalf("expected connected, got %+v", connected)
	}

	c.Send(wire.Envelope{Type: wire.FrameConnect, Protocol: "demo", Version: "1", Payload: mustJSON(t, ConnectParams{Token: "abc"})})
	resp := c.Recv()
	if resp.Type != wire.FrameConnectionError || resp.Error.Code != latchwire.ErrCodeProtocolViolation {
		t.Fatalf("expected protocol_violation connection_error, got %+v", resp)
	}
}

func TestUnknownMethod(t *testing.T) {
	srv := newAddServer(t)
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Connect("demo", "1", ConnectParams{Token: "abc"})

	c.Request("1", "does.not.exist", map[string]any{})
	resp := c.Recv()
	if resp.Type != wire.FrameError || resp.Error.Code != latchwire.ErrCodeMethodNotFound {
		t.Fatalf("expected method_not_found error, got %+v", resp)
	}
}

func TestInvalidRequestPayload(t *testing.T) {
	srv := newAddServer(t)
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Connect("demo", "1", ConnectParams{Token: "abc"})

	c.Request("1", "math.add", map[string]any{"a": "not-a-number", "b": 2})
	resp := c.Recv()
	if resp.Type != wire.FrameError || resp.Error.Code != latchwire.ErrCodeInvalidRequest {
		t.Fatalf("expected invalid_request error, got %+v", resp)
	}

	// The connection must remain usable after a rejected request.
	c.Request("2", "math.add", AddRequest{A: 4, B: 5})
	resp2 := c.Recv()
	if resp2.Type != wire.FrameResponse || resp2.ID != "2" {
		t.Fatalf("expected connection to keep working, got %+v", resp2)
	}
}

func TestApplicationError(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})
	err := srv.Register("user.get", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		return AddResponse{}, latchwire.NewError("not_found", "user not found")
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Connect("demo", "1", ConnectParams{Token: "abc"})
	c.Request("1", "user.get", AddRequest{A: 1, B: 1})

	resp := c.Recv()
	if resp.Type != wire.FrameError {
		t.Fatalf("expected error frame, got %+v", resp)
	}
	if resp.Error.Code != "not_found" || resp.Error.Message != "user not found" {
		t.Fatalf("expected not_found/user not found, got %+v", resp.Error)
	}
}

func TestHandlerPanicRecovered(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})
	err := srv.Register("boom", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		panic("kaboom")
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Connect("demo", "1", ConnectParams{Token: "abc"})
	c.Request("1", "boom", AddRequest{A: 1, B: 1})

	resp := c.Recv()
	if resp.Type != wire.FrameError || resp.Error.Code != latchwire.ErrCodeInternal {
		t.Fatalf("expected internal_error after panic, got %+v", resp)
	}

	// Server must not have crashed: connection remains usable.
	c.Request("2", "boom", AddRequest{A: 1, B: 1})
	resp2 := c.Recv()
	if resp2.Type != wire.FrameError || resp2.Error.Code != latchwire.ErrCodeInternal {
		t.Fatalf("expected server to survive repeated panics, got %+v", resp2)
	}
}

func TestConcurrentRequestsOutOfOrder(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{
		ProtocolName:          "demo",
		ProtocolVersion:       "1",
		MaxConcurrentRequests: 8,
	})
	err := srv.Register("delay.echo", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		// A carries the requested delay in milliseconds, inverted so that
		// higher IDs finish first, proving responses need not be in order.
		time.Sleep(time.Duration(req.A) * time.Millisecond)
		return AddResponse{Result: req.B}, nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Connect("demo", "1", ConnectParams{Token: "abc"})

	const n = 5
	for i := 0; i < n; i++ {
		delay := (n - i) * 20 // request 0 sleeps longest, request n-1 shortest
		c.Request(fmt.Sprintf("%d", i), "delay.echo", AddRequest{A: delay, B: i})
	}

	seen := map[string]int{}
	var order []string
	for i := 0; i < n; i++ {
		resp := c.Recv()
		if resp.Type != wire.FrameResponse {
			t.Fatalf("expected response, got %+v", resp)
		}
		var out AddResponse
		decodeInto(t, resp.Payload, &out)
		seen[resp.ID] = out.Result
		order = append(order, resp.ID)
	}

	if len(seen) != n {
		t.Fatalf("expected %d distinct responses, got %d (%v)", n, len(seen), seen)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%d", i)
		if seen[id] != i {
			t.Fatalf("response %s carried wrong payload: %d", id, seen[id])
		}
	}
	if order[0] == "0" {
		t.Fatalf("expected out-of-order completion (request 0 sleeps longest), got order %v", order)
	}
}

func TestDuplicateRequestIDRejected(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})
	started := make(chan struct{})
	release := make(chan struct{})
	err := srv.Register("slow", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		close(started)
		<-release
		return AddResponse{Result: 1}, nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Connect("demo", "1", ConnectParams{Token: "abc"})

	c.Request("dup", "slow", AddRequest{})
	<-started

	c.Request("dup", "slow", AddRequest{})
	resp := c.Recv()
	if resp.Type != wire.FrameError || resp.Error.Code != latchwire.ErrCodeDuplicateRequestID {
		t.Fatalf("expected duplicate_request_id, got %+v", resp)
	}

	close(release)
	final := c.Recv()
	if final.Type != wire.FrameResponse || final.ID != "dup" {
		t.Fatalf("expected the original in-flight request to still complete, got %+v", final)
	}
}

func TestEventBufferedUntilAfterConnected(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})
	tick := latchwire.Event[Tick]("tick")
	if err := srv.RegisterEvent(tick); err != nil {
		t.Fatalf("RegisterEvent: %v", err)
	}
	err := srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) error {
		// Sent synchronously, before OnConnect returns and before
		// "connected" is written: must still arrive after "connected".
		if err := tick.Send(conn, Tick{Value: 1}); err != nil {
			t.Errorf("Send during OnConnect: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	connected := c.Connect("demo", "1", ConnectParams{Token: "abc"})
	if connected.Type != wire.FrameConnected {
		t.Fatalf("expected connected first, got %+v", connected)
	}

	evt := c.Recv()
	if evt.Type != wire.FrameEvent || evt.Event != "tick" {
		t.Fatalf("expected buffered tick event after connected, got %+v", evt)
	}
	var payload Tick
	decodeInto(t, evt.Payload, &payload)
	if payload.Value != 1 {
		t.Fatalf("expected tick value 1, got %d", payload.Value)
	}
}

func TestOnConnectRejectionClosesConnection(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})
	err := srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) error {
		return latchwire.NewError("forbidden", "nope")
	})
	if err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	resp := c.Connect("demo", "1", ConnectParams{Token: "abc"})
	if resp.Type != wire.FrameConnectionError {
		t.Fatalf("expected connection_error, got %+v", resp)
	}
	if resp.Error.Code != "forbidden" {
		t.Fatalf("expected forbidden code, got %+v", resp.Error)
	}
	if _, err := c.TryRecv(500 * time.Millisecond); err == nil {
		t.Fatalf("expected connection closed after OnConnect rejection")
	}
}

func TestOnCloseCallbacksRunLIFOOnDisconnect(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})

	var mu sync.Mutex
	var order []int
	done := make(chan struct{})

	err := srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) error {
		conn.OnClose(func() {
			mu.Lock()
			order = append(order, 1)
			mu.Unlock()
		})
		conn.OnClose(func() {
			mu.Lock()
			order = append(order, 2)
			mu.Unlock()
		})
		conn.OnClose(func() {
			mu.Lock()
			order = append(order, 3)
			mu.Unlock()
			close(done)
		})
		return nil
	})
	if err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	connected := c.Connect("demo", "1", ConnectParams{Token: "abc"})
	if connected.Type != wire.FrameConnected {
		t.Fatalf("expected connected, got %+v", connected)
	}
	c.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("OnClose callbacks did not run after disconnect")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []int{3, 2, 1}
	if len(order) != len(want) {
		t.Fatalf("expected %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("expected LIFO order %v, got %v", want, order)
		}
	}
}

func TestDisconnectCancelsRequestContext(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})
	canceled := make(chan struct{})
	err := srv.Register("wait", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		<-ctx.Done()
		close(canceled)
		return AddResponse{}, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Connect("demo", "1", ConnectParams{Token: "abc"})
	c.Request("1", "wait", AddRequest{})
	c.Close()

	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected request context to be canceled on disconnect")
	}
}

func TestOutboundQueueOverflowClosesConnection(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{
		ProtocolName:      "demo",
		ProtocolVersion:   "1",
		OutboundQueueSize: 2,
	})
	tick := latchwire.Event[Tick]("tick")
	if err := srv.RegisterEvent(tick); err != nil {
		t.Fatalf("RegisterEvent: %v", err)
	}
	err := srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) error {
		go func() {
			for i := 0; i < 100; i++ {
				if tick.Send(conn, Tick{Value: i}) != nil {
					return
				}
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Send(wire.Envelope{Type: wire.FrameConnect, Protocol: "demo", Version: "1", Payload: mustJSON(t, ConnectParams{Token: "abc"})})

	// Deliberately never drain beyond what's needed to notice the
	// connection ending, forcing the outbound queue to overflow. The
	// OnConnect goroutine above races against the server's own
	// connected-frame flush (both send events; only one can be first) —
	// depending on which wins, the overflow can be detected either before
	// or after "connected" is ever written, so this test only asserts the
	// one guarantee that actually holds regardless of that race: the
	// connection terminates rather than hanging or dropping frames
	// silently forever. See docs/design-notes.md.
	deadline := time.Now().Add(3 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		env, err := c.TryRecv(500 * time.Millisecond)
		if err != nil {
			closed = true
			break
		}
		if env.Type == wire.FrameConnectionError {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatalf("expected the connection to close due to outbound queue overflow, but it neither closed nor sent a connection_error")
	}
}

func TestOversizedMessageClosesConnection(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{
		ProtocolName:    "demo",
		ProtocolVersion: "1",
		MaxMessageBytes: 128,
	})
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	big := strings.Repeat("x", 1024)
	c.SendRaw([]byte(`{"type":"connect","protocol":"demo","version":"1","payload":{"token":"` + big + `"}}`))

	if _, err := c.TryRecv(2 * time.Second); err == nil {
		t.Fatalf("expected oversized message to close the connection without a normal response")
	}
}

func TestServerCloseShutsDownActiveConnections(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})
	canceled := make(chan struct{})
	err := srv.Register("wait", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		<-ctx.Done()
		close(canceled)
		return AddResponse{}, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	closedCallback := make(chan struct{})
	err = srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) error {
		conn.OnClose(func() { close(closedCallback) })
		return nil
	})
	if err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	connected := c.Connect("demo", "1", ConnectParams{Token: "abc"})
	if connected.Type != wire.FrameConnected {
		t.Fatalf("expected connected, got %+v", connected)
	}
	c.Request("1", "wait", AddRequest{})

	// Give the request time to actually start running before shutting down.
	time.Sleep(50 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Close(shutdownCtx); err != nil {
		t.Fatalf("Server.Close: %v", err)
	}

	select {
	case <-canceled:
	default:
		t.Fatalf("expected the in-flight request's context to be canceled by server shutdown")
	}
	select {
	case <-closedCallback:
	default:
		t.Fatalf("expected OnClose callbacks to run during server shutdown")
	}

	if _, err := c.TryRecv(500 * time.Millisecond); err == nil {
		t.Fatalf("expected the connection to be closed after server shutdown")
	}
}

func TestMaxConcurrentRequestsBoundsConcurrency(t *testing.T) {
	const limit = 3
	srv := latchwire.New[ConnectParams](latchwire.Options{
		ProtocolName:          "demo",
		ProtocolVersion:       "1",
		MaxConcurrentRequests: limit,
	})

	var mu sync.Mutex
	running := 0
	maxObserved := 0
	release := make(chan struct{})

	err := srv.Register("slow", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		mu.Lock()
		running++
		if running > maxObserved {
			maxObserved = running
		}
		mu.Unlock()

		<-release

		mu.Lock()
		running--
		mu.Unlock()
		return AddResponse{}, nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	url := wsURL(t, srv)

	c := testutil.Dial(t, url)
	c.Connect("demo", "1", ConnectParams{Token: "abc"})

	const total = limit * 3
	for i := 0; i < total; i++ {
		c.Request(fmt.Sprintf("%d", i), "slow", AddRequest{})
	}

	// Let every request that's going to start actually start.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	observed := maxObserved
	mu.Unlock()
	if observed > limit {
		t.Fatalf("expected at most %d concurrent handlers, observed %d", limit, observed)
	}
	if observed != limit {
		t.Fatalf("expected concurrency to actually reach the configured limit %d, observed %d", limit, observed)
	}

	close(release)
	for i := 0; i < total; i++ {
		resp := c.Recv()
		if resp.Type != wire.FrameResponse {
			t.Fatalf("expected response, got %+v", resp)
		}
	}
}
