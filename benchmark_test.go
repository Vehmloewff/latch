package latch_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/wire"
)

type benchmarkState struct{}
type benchmarkRequest struct {
	A int `json:"a"`
	B int `json:"b"`
}
type benchmarkResponse struct {
	Result int `json:"result"`
}
type benchmarkEvent struct{}

func BenchmarkRequestRoundTrip(b *testing.B) {
	server := latch.New[benchmarkState](latch.Options{ProtocolVersion: "1"})
	server.OnConnect(func(context.Context, latch.Emitter[benchmarkEvent], *latch.Conn) (benchmarkState, error) {
		return benchmarkState{}, nil
	})
	server.Register("math_add", func(_ context.Context, _ benchmarkState, req benchmarkRequest) (benchmarkResponse, error) {
		return benchmarkResponse{Result: req.A + req.B}, nil
	})

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	ctx := context.Background()
	ws, _, err := websocket.Dial(ctx, url+"?version=1", nil)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	send := func(env wire.Envelope) {
		raw, err := json.Marshal(env)
		if err != nil {
			b.Fatalf("marshal: %v", err)
		}
		if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	recv := func() wire.Envelope {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			b.Fatalf("read: %v", err)
		}
		var env wire.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			b.Fatalf("unmarshal: %v", err)
		}
		return env
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := strconv.Itoa(i)
		raw, _ := json.Marshal(benchmarkRequest{A: i, B: 1})
		send(wire.Envelope{Type: wire.FrameRequest, ID: id, Method: "math_add", Payload: raw})
		resp := recv()
		if resp.Type != wire.FrameResponse || resp.ID != id {
			b.Fatalf("unexpected response: %+v", resp)
		}
	}
}
