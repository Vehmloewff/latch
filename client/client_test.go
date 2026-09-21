package client_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/client"
)

type testState struct{}
type testRequest struct {
	A int `json:"a" latch:"1"`
	B int `json:"b" latch:"2"`
}
type testResponse struct {
	Result int `json:"result" latch:"1"`
}
type testEvent struct {
	Value int `json:"value" latch:"1"`
}

func newTestServer(t *testing.T) string {
	t.Helper()
	server := latch.New[testState](latch.Options{ProtocolVersion: "1"})
	server.OnConnect(func(_ context.Context, emitter latch.Emitter[testEvent], _ *latch.Conn) (testState, error) {
		if err := emitter.Send(testEvent{Value: 1}); err != nil {
			return testState{}, err
		}
		return testState{}, nil
	})
	server.Register("math_add", func(_ context.Context, _ testState, req testRequest) (testResponse, error) {
		if req.A == -1 {
			return testResponse{}, latch.NewError("invalid_request", "a must not be -1")
		}
		return testResponse{Result: req.A + req.B}, nil
	})

	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

func connectClient(t *testing.T) (*client.Conn, <-chan testEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := client.Connect(ctx, newTestServer(t), "1")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	events := client.RegisterEvent[testEvent](conn)
	conn.Start()
	t.Cleanup(func() { _ = conn.Close() })
	return conn, events
}

func TestClientCallAndEvent(t *testing.T) {
	conn, events := connectClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	select {
	case event := <-events:
		if event.Value != 1 {
			t.Fatalf("event value = %d, want 1", event.Value)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for event")
	}

	response, err := client.Call[testResponse](ctx, conn, "math_add", testRequest{A: 2, B: 3})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if response.Result != 5 {
		t.Fatalf("result = %d, want 5", response.Result)
	}
}

func TestClientApplicationError(t *testing.T) {
	conn, _ := connectClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Call[testResponse](ctx, conn, "math_add", testRequest{A: -1, B: 1})
	wireErr, ok := err.(*client.Error)
	if !ok {
		t.Fatalf("error type = %T, want *client.Error", err)
	}
	if wireErr.Code != "invalid_request" || wireErr.Message != "a must not be -1" {
		t.Fatalf("error = %#v, want invalid_request with the handler message", wireErr)
	}
}

func TestClientCloseClosesEvents(t *testing.T) {
	conn, events := connectClient(t)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-events:
		if ok {
			for range events {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event channel was not closed")
	}
}
