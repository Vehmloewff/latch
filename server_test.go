package latch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/testutil"
)

type serverState struct{}
type serverRequest struct {
	A int `json:"a" latch:"1"`
	B int `json:"b" latch:"2"`
}
type serverResponse struct {
	Result int `json:"result" latch:"1"`
}
type serverEvent struct {
	Kind string `json:"kind" latch:"1"`
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

func TestServerRejectsMissingProtocolVersion(t *testing.T) {
	server := newServer(t)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	for _, rawURL := range []string{httpServer.URL, httpServer.URL + "?version="} {
		response, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET %s: %v", rawURL, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want %d", rawURL, response.StatusCode, http.StatusBadRequest)
		}
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
