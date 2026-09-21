package latch_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/testutil"
	"github.com/vehmloewff/latch/wire"
)

type serverState struct{}
type serverRequest struct {
	A int `json:"a"`
	B int `json:"b"`
}
type serverResponse struct {
	Result int `json:"result"`
}
type serverEvent struct {
	Kind string `json:"kind"`
}

func newServer(t *testing.T) *latch.Server[serverState] {
	t.Helper()
	server := latch.New[serverState](latch.Options{ProtocolVersion: "1"})
	server.OnConnect(func(_ context.Context, emitter latch.Emitter[serverEvent], _ *latch.Conn) (serverState, error) {
		if err := emitter.Send(serverEvent{Kind: "connected"}); err != nil {
			return serverState{}, err
		}
		return serverState{}, nil
	})
	server.Register("math_add", func(_ context.Context, _ serverState, req serverRequest) (serverResponse, error) {
		return serverResponse{Result: req.A + req.B}, nil
	})
	return server
}

func rawClient(t *testing.T, server *latch.Server[serverState]) *testutil.Client {
	t.Helper()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return testutil.Dial(t, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"?version=1")
}

func TestServerRequestAndEvent(t *testing.T) {
	client := rawClient(t, newServer(t))
	first := client.Recv()
	if first.Type != wire.FrameEvent {
		t.Fatalf("first frame type = %q, want event", first.Type)
	}
	var event serverEvent
	if err := json.Unmarshal(first.Payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != "connected" {
		t.Fatalf("event kind = %q, want connected", event.Kind)
	}

	payload, _ := json.Marshal(serverRequest{A: 2, B: 3})
	client.Send(wire.Envelope{Type: wire.FrameRequest, ID: "1", Method: "math_add", Payload: payload})
	response := client.Recv()
	if response.Type != wire.FrameResponse || response.ID != "1" {
		t.Fatalf("response = %+v", response)
	}
	var result serverResponse
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Result != 5 {
		t.Fatalf("result = %d, want 5", result.Result)
	}
}

func TestServerRejectsInvalidRequestAndUnknownMethod(t *testing.T) {
	client := rawClient(t, newServer(t))
	if event := client.Recv(); event.Type != wire.FrameEvent {
		t.Fatalf("expected initial event, got %+v", event)
	}

	client.Send(wire.Envelope{Type: wire.FrameRequest, ID: "1", Method: "math_add", Payload: []byte(`{"a":"bad","b":2}`)})
	invalid := client.Recv()
	if invalid.Type != wire.FrameError || invalid.Error != "request payload failed schema validation" {
		t.Fatalf("invalid request response = %+v", invalid)
	}

	client.Send(wire.Envelope{Type: wire.FrameRequest, ID: "2", Method: "missing_method", Payload: []byte(`{}`)})
	unknown := client.Recv()
	if unknown.Type != wire.FrameError {
		t.Fatalf("unknown method response = %+v", unknown)
	}
}

func TestServerFinalizationRejectsMissingEventType(t *testing.T) {
	server := latch.New[serverState](latch.Options{})
	if _, err := server.Schema(); err == nil {
		t.Fatal("Schema succeeded without an event type")
	}
}

func TestServerClosesConnection(t *testing.T) {
	client := rawClient(t, newServer(t))
	_ = client.Recv()
	client.Close()
	if _, err := client.TryRecv(100 * time.Millisecond); err == nil {
		t.Fatal("expected closed connection")
	}
}
