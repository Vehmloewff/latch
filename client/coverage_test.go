package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/vehmloewff/latch/client"
	"github.com/vehmloewff/latch/wire"
)

type coverageRequest struct {
	Value int `latch:"1"`
}

type coverageResponse struct {
	Value int `latch:"1"`
}

type coverageEvent struct {
	Value int `latch:"1"`
}

func newCoverageWSServer(t *testing.T, handler func(*websocket.Conn)) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")
		handler(ws)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func writeCoverageRaw(ws *websocket.Conn, raw []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageBinary, raw)
}

func writeCoverageEnvelope(ws *websocket.Conn, env wire.Envelope) error {
	raw, err := env.MarshalBinary()
	if err != nil {
		return err
	}
	return writeCoverageRaw(ws, raw)
}

func readCoverageRequest(ws *websocket.Conn) (wire.Envelope, error) {
	_, raw, err := ws.Read(context.Background())
	if err != nil {
		return wire.Envelope{}, err
	}
	var env wire.Envelope
	if err := env.UnmarshalBinary(raw); err != nil {
		return wire.Envelope{}, err
	}
	return env, nil
}

func connectCoverageClient(t *testing.T, handler func(*websocket.Conn)) *client.Conn {
	t.Helper()
	url := newCoverageWSServer(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := client.Connect(ctx, url, "1")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestClientConnectInvalidURLAndDialFailure(t *testing.T) {
	if _, err := client.Connect(context.Background(), "ws://[::1", "1"); err == nil || !strings.Contains(err.Error(), "latch: build connection URL") {
		t.Fatalf("invalid URL error = %v, want URL construction error", err)
	}

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Deliberately do not upgrade this request.
		http.Error(w, "not a websocket", http.StatusBadRequest)
	}))
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := client.Connect(ctx, wsURL, "1"); err == nil || !strings.Contains(err.Error(), "latch: dial") {
		t.Fatalf("dial error = %v, want dial failure", err)
	}
}

func TestClientClosedClosesChannelAndRejectsCalls(t *testing.T) {
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		_, _, _ = ws.Read(context.Background())
	})

	select {
	case <-conn.Closed():
		t.Fatal("Closed channel was closed before Close")
	default:
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-conn.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("Closed channel was not closed")
	}

	_, err := client.Call[coverageResponse](context.Background(), conn, "after_close", coverageRequest{})
	var clientErr *client.Error
	if !errors.As(err, &clientErr) || clientErr.Code != "connection_closed" {
		t.Fatalf("call after close error = %v, want connection_closed", err)
	}
}

func TestClientIgnoresMalformedFrame(t *testing.T) {
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		request, err := readCoverageRequest(ws)
		if err != nil {
			return
		}
		if err := writeCoverageRaw(ws, []byte{0xff, 0x00}); err != nil {
			return
		}
		payload, err := wire.Encode(coverageResponse{Value: 42})
		if err != nil {
			return
		}
		_ = writeCoverageEnvelope(ws, wire.Envelope{
			Type:    wire.FrameResponse,
			ID:      request.ID,
			Payload: payload,
		})
	})
	conn.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := client.Call[coverageResponse](ctx, conn, "malformed_then_response", coverageRequest{Value: 1})
	if err != nil {
		t.Fatalf("Call after malformed frame: %v", err)
	}
	if response.Value != 42 {
		t.Fatalf("response value = %d, want 42", response.Value)
	}
}

func TestClientConnectionErrorFailsPendingCallAndCloses(t *testing.T) {
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		request, err := readCoverageRequest(ws)
		if err != nil {
			return
		}
		_ = writeCoverageEnvelope(ws, wire.Envelope{
			Type:      wire.FrameConnectionError,
			ErrorCode: "server_down",
		})
		_ = request
	})
	conn.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Call[coverageResponse](ctx, conn, "wait_for_disconnect", coverageRequest{})
	var clientErr *client.Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("Call error type = %T, want *client.Error", err)
	}
	if clientErr.Code != "server_down" || clientErr.Message != "connection error" {
		t.Fatalf("connection error = %#v, want server_down: connection error", clientErr)
	}
	select {
	case <-conn.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("connection did not close after connection_error frame")
	}
}

func TestClientResponseSurvivesImmediateServerClose(t *testing.T) {
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		request, err := readCoverageRequest(ws)
		if err != nil {
			return
		}
		payload, err := wire.Encode(coverageResponse{Value: 42})
		if err != nil {
			return
		}
		_ = writeCoverageEnvelope(ws, wire.Envelope{
			Type: wire.FrameResponse, ID: request.ID, Payload: payload,
		})
	})
	conn.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := client.Call[coverageResponse](ctx, conn, "response_then_close", coverageRequest{})
	if err != nil {
		t.Fatalf("Call lost response to server close: %v", err)
	}
	if response.Value != 42 {
		t.Fatalf("response value = %d, want 42", response.Value)
	}
	select {
	case <-conn.Closed():
	case <-ctx.Done():
		t.Fatal("server close did not reach client")
	}
}

func TestClientRequestEncodingFailure(t *testing.T) {
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		_, _, _ = ws.Read(context.Background())
	})
	conn.Start()

	_, err := client.Call[coverageResponse](context.Background(), conn, "bad_request", func() {})
	if err == nil || !strings.Contains(err.Error(), "latch: encode request") {
		t.Fatalf("request encoding error = %v, want encode context", err)
	}
}

func TestClientRequestCancellation(t *testing.T) {
	requestReceived := make(chan struct{})
	release := make(chan struct{})
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		if _, err := readCoverageRequest(ws); err != nil {
			return
		}
		close(requestReceived)
		<-release
	})
	conn.Start()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Call[coverageResponse](ctx, conn, "cancel_me", coverageRequest{})
		result <- err
	}()

	select {
	case <-requestReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive request")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled call error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled call did not return")
	}
	close(release)
}

func TestClientResponseDecodeFailure(t *testing.T) {
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		request, err := readCoverageRequest(ws)
		if err != nil {
			return
		}
		payload, err := wire.Encode("not a response struct")
		if err != nil {
			return
		}
		_ = writeCoverageEnvelope(ws, wire.Envelope{
			Type:    wire.FrameResponse,
			ID:      request.ID,
			Payload: payload,
		})
	})
	conn.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Call[coverageResponse](ctx, conn, "bad_response", coverageRequest{})
	if err == nil || !strings.Contains(err.Error(), `latch: decode response for "bad_response"`) {
		t.Fatalf("decode failure = %v, want response decode context", err)
	}
}

func TestClientFrameErrorFormattingAndDefaults(t *testing.T) {
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		request, err := readCoverageRequest(ws)
		if err != nil {
			return
		}
		_ = writeCoverageEnvelope(ws, wire.Envelope{
			Type:      wire.FrameError,
			ID:        request.ID,
			ErrorCode: "remote_failure",
		})
	})
	conn.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Call[coverageResponse](ctx, conn, "remote_error", coverageRequest{})
	var clientErr *client.Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("frame error type = %T, want *client.Error", err)
	}
	if got, want := clientErr.Error(), "remote_failure: internal error"; got != want {
		t.Fatalf("formatted frame error = %q, want %q", got, want)
	}

	if got, want := (&client.Error{Code: "code", Message: "message"}).Error(), "code: message"; got != want {
		t.Fatalf("formatted Error = %q, want %q", got, want)
	}
}

func TestClientEventDecodeFailureAndDrop(t *testing.T) {
	const eventCount = 65
	sent := make(chan struct{})
	conn := connectCoverageClient(t, func(ws *websocket.Conn) {
		// This payload has a valid envelope but an invalid event value.
		if err := writeCoverageEnvelope(ws, wire.Envelope{
			Type:    wire.FrameEvent,
			Payload: []byte{wire.KindString, 1, 0xff},
		}); err != nil {
			return
		}
		for i := 0; i < eventCount; i++ {
			payload, err := wire.Encode(coverageEvent{Value: i})
			if err != nil {
				return
			}
			if err := writeCoverageEnvelope(ws, wire.Envelope{
				Type:    wire.FrameEvent,
				Payload: payload,
			}); err != nil {
				return
			}
		}
		close(sent)
	})
	events := client.RegisterEvent[coverageEvent](conn)
	conn.Start()

	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish sending events")
	}

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for cap(events) > len(events) {
		select {
		case <-deadline.C:
			t.Fatalf("received %d events, want full buffer of %d", len(events), cap(events))
		case <-time.After(time.Millisecond):
		}
	}
	for want := 0; want < cap(events); want++ {
		select {
		case got, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before buffered events were read")
			}
			if got.Value != want {
				t.Fatalf("event %d value = %d, want %d", want, got.Value, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out reading event %d", want)
		}
	}
}
