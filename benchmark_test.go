package latchwire_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/vehmloewff/latchwire"
	"github.com/vehmloewff/latchwire/wire"
)

// BenchmarkRequestRoundTrip measures single-connection request/response
// throughput: schema validation, decode, handler dispatch, encode, and the
// write-pump round trip, end to end over a real (loopback) WebSocket. Per
// spec section 53, Latchwire is not trying to beat a custom binary
// protocol — this exists to catch obvious regressions (e.g. accidentally
// recompiling a schema per request), not to chase a specific number.
func BenchmarkRequestRoundTrip(b *testing.B) {
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})
	err := srv.Register("math.add", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		return AddResponse{Result: req.A + req.B}, nil
	})
	if err != nil {
		b.Fatalf("Register: %v", err)
	}

	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http")

	ctx := context.Background()
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	mustMarshal := func(v any) json.RawMessage {
		raw, err := json.Marshal(v)
		if err != nil {
			b.Fatalf("marshal: %v", err)
		}
		return raw
	}
	send := func(env wire.Envelope) {
		raw, err := json.Marshal(env)
		if err != nil {
			b.Fatalf("marshal envelope: %v", err)
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
			b.Fatalf("unmarshal envelope: %v", err)
		}
		return env
	}

	send(wire.Envelope{Type: wire.FrameConnect, Protocol: "demo", Version: "1", Payload: mustMarshal(ConnectParams{Token: "abc"})})
	if connected := recv(); connected.Type != wire.FrameConnected {
		b.Fatalf("expected connected, got %+v", connected)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		id := strconv.Itoa(i)
		send(wire.Envelope{Type: wire.FrameRequest, ID: id, Method: "math.add", Payload: mustMarshal(AddRequest{A: i, B: 1})})
		resp := recv()
		if resp.Type != wire.FrameResponse || resp.ID != id {
			b.Fatalf("unexpected response: %+v", resp)
		}
	}
}
