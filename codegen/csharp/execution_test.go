package csharp

import (
	"context"
	_ "embed"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/vehmloewff/latch/protocol"
	"github.com/vehmloewff/latch/wire"
)

//go:embed binary_harness_test.cs
var binaryHarness string

//go:embed socket_harness_test.cs
var socketHarness string

//go:embed connection_error_harness_test.cs
var connectionErrorHarness string

//go:embed early_event_harness_test.cs
var earlyEventHarness string

//go:embed dispose_harness_test.cs
var disposeHarness string

//go:embed reconnect_harness_test.cs
var reconnectHarness string

//go:embed cancellation_harness_test.cs
var cancellationHarness string

// The same generated file is compiled for both suites; no checked-in copy of the runtime is used.
func runCSharp(t *testing.T, harness string, args ...string) {
	t.Helper()
	dotnet, err := exec.LookPath("dotnet")
	if err != nil {
		t.Skip("dotnet SDK not installed")
	}
	dir := t.TempDir()
	p := fixture()
	// Exercise more than the usual packet: scalar narrowing, fixed arrays and time.
	p.Types = append(p.Types, &protocol.NamedType{ID: "metrics", GoName: "Metrics", Kind: protocol.KindStruct, Fields: []protocol.Field{
		{GoName: "Small", Type: protocol.TypeRef{Kind: protocol.KindInt8}},
		{GoName: "Count", Type: protocol.TypeRef{Kind: protocol.KindUint32}, Optional: true},
		{GoName: "Fixed", Type: protocol.TypeRef{Kind: protocol.KindArray, ArrayLen: 3, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}},
		{GoName: "When", Type: protocol.TypeRef{Kind: protocol.KindTime}},
		{GoName: "Samples", Type: protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindPointer, Elem: &protocol.TypeRef{Kind: protocol.KindString}}}},
	}})
	files := map[string]string{
		"Harness.cs":     harness,
		"LatchClient.cs": source(t, p, Options{}),
		"Test.csproj":    `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework><Nullable>enable</Nullable><ImplicitUsings>enable</ImplicitUsings></PropertyGroup></Project>`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, dotnet, append([]string{"run", "--project", dir, "--configuration", "Release", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1", "DOTNET_NOLOGO=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("dotnet timed out: %v\n%s", ctx.Err(), out)
	}
	if err != nil {
		t.Fatalf("dotnet run: %v\n%s", err, out)
	}
	t.Logf("%s", strings.TrimSpace(string(out)))
}

func TestGeneratedCSharpBinaryExecutable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "wire", "testdata", "cross_language_vectors.txt"))
	if err != nil {
		t.Fatal(err)
	}
	vectors := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "#" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		vectors[fields[0]] = strings.Join(fields[1:], "")
	}
	// Go encoder supplies an independent high/low-boundary reference value.
	goValue, err := wire.Encode(struct {
		Min  int64  `latch:"7"`
		Max  uint64 `latch:"19"`
		Blob []byte `latch:"23"`
	}{Min: -1 << 63, Max: ^uint64(0), Blob: []byte{0, 255}})
	if err != nil {
		t.Fatal(err)
	}
	var pairs []string
	for _, name := range []string{"int_minus_one", "int_64", "uint_300", "float32_one", "float64_one", "string_hello", "bytes_binary", "struct_nested", "envelope_response"} {
		if vectors[name] == "" {
			t.Fatalf("missing fixture %s", name)
		}
		pairs = append(pairs, fmt.Sprintf("{ %q, %q }", name, vectors[name]))
	}
	harness := strings.ReplaceAll(binaryHarness, "VECTOR_PAIRS", strings.Join(pairs, ",\n"))
	harness = strings.ReplaceAll(harness, "GO_BOUNDARY", hex.EncodeToString(goValue))
	runCSharp(t, harness)
}

func socketPacket(t *testing.T, text string) []byte {
	t.Helper()
	b, err := wire.Encode(struct {
		Text     string            `latch:"13"`
		State    string            `latch:"2"`
		Nullable *string           `latch:"3"`
		Blob     []byte            `latch:"5"`
		History  []any             `latch:"6"`
		Tags     map[string]string `latch:"7"`
	}{Text: text, State: "open", Blob: []byte{1, 2}, History: []any{}, Tags: map[string]string{"from": "go"}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGeneratedCSharpReconnect(t *testing.T) {
	var attempts atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		_, raw, err := ws.Read(ctx)
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		var req wire.Envelope
		if err := req.UnmarshalBinary(raw); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		if n == 2 {
			ws.Close(websocket.StatusNormalClosure, "drop")
			return
		}
		if n != 3 {
			t.Errorf("unexpected attempt %d", n)
			return
		}
		resp, _ := (wire.Envelope{Type: wire.FrameResponse, ID: req.ID, Payload: req.Payload}).MarshalBinary()
		if err := ws.Write(ctx, websocket.MessageBinary, resp); err != nil {
			t.Errorf("response: %v", err)
			return
		}
		event, _ := (wire.Envelope{Type: wire.FrameEvent, Payload: socketPacket(t, "event")}).MarshalBinary()
		if err := ws.Write(ctx, websocket.MessageBinary, event); err != nil {
			t.Errorf("event: %v", err)
			return
		}
		<-ctx.Done()
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	runCSharp(t, reconnectHarness, "ws"+strings.TrimPrefix(server.URL, "http"))
	if attempts.Load() != 3 {
		t.Fatalf("attempts: %d", attempts.Load())
	}
}

func TestGeneratedCSharpInitialCancellation(t *testing.T) {
	var retries, handshakes atomic.Int32
	handshakeCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/retry":
			retries.Add(1)
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/handshake":
			handshakes.Add(1)
			select {
			case <-r.Context().Done():
				close(handshakeCanceled)
			case <-time.After(7 * time.Second):
				t.Error("active handshake was not canceled")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	runCSharp(t, cancellationHarness, url+"/retry", url+"/handshake")
	if got := retries.Load(); got != 1 {
		t.Errorf("attempts after cancellation during retry delay = %d, want 1", got)
	}
	if got := handshakes.Load(); got != 1 {
		t.Errorf("active handshake attempts = %d, want 1", got)
	}
	select {
	case <-handshakeCanceled:
	case <-time.After(2 * time.Second):
		t.Error("active handshake remained open after cancellation")
	}
}

func TestGeneratedCSharpWebSocketLifecycle(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not installed")
	}
	event := socketPacket(t, "event")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("version") != "v1" || r.URL.Query().Get("foo") != "bar" || len(r.URL.Query()["version"]) != 1 {
			t.Errorf("handshake query: %s", r.URL.RawQuery)
			http.Error(w, "bad version", 400)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "done")
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		send := func(e wire.Envelope) bool {
			b, err := e.MarshalBinary()
			if err == nil {
				err = ws.Write(ctx, websocket.MessageBinary, b)
			}
			if err != nil {
				t.Errorf("send %s: %v", e.Type, err)
				return false
			}
			return true
		}
		recv := func() (wire.Envelope, bool) {
			typ, b, err := ws.Read(ctx)
			if err != nil {
				t.Errorf("read: %v", err)
				return wire.Envelope{}, false
			}
			if typ != websocket.MessageBinary {
				t.Errorf("request frame type %v", typ)
				return wire.Envelope{}, false
			}
			var e wire.Envelope
			if err = e.UnmarshalBinary(b); err != nil {
				t.Errorf("decode request: %v", err)
				return e, false
			}
			if e.Type != wire.FrameRequest || e.Method != "chat_send_message" {
				t.Errorf("request: %+v", e)
				return e, false
			}
			return e, true
		}
		first, ok := recv()
		if !ok {
			return
		}
		if !send(wire.Envelope{Type: wire.FrameEvent, Event: "packet", Payload: event}) {
			return
		}
		second, ok := recv()
		if !ok {
			return
		}
		if first.ID == second.ID {
			t.Error("duplicate request IDs")
			return
		}
		for _, req := range []wire.Envelope{second, first} {
			if !send(wire.Envelope{Type: wire.FrameResponse, ID: req.ID, Payload: req.Payload}) {
				return
			}
		}
		third, ok := recv()
		if !ok {
			return
		}
		if !send(wire.Envelope{Type: wire.FrameError, ID: third.ID, ErrorCode: "denied", Error: "not allowed"}) {
			return
		}
		_, ok = recv()
		if !ok {
			return
		} // Close while an RPC is pending.
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "?foo=bar&version=old"
	runCSharp(t, socketHarness, url)
}

func TestGeneratedCSharpConnectionError(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not installed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "done")
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		var request wire.Envelope
		if err := request.UnmarshalBinary(data); err != nil || request.Type != wire.FrameRequest {
			t.Errorf("request: %+v, %v", request, err)
			return
		}
		frame, err := (wire.Envelope{Type: wire.FrameConnectionError, ErrorCode: "maintenance", Error: "try later"}).MarshalBinary()
		if err != nil {
			t.Errorf("marshal connection error: %v", err)
			return
		}
		if err := ws.Write(ctx, websocket.MessageBinary, frame); err != nil {
			t.Errorf("send connection error: %v", err)
		}
	}))
	defer server.Close()
	runCSharp(t, connectionErrorHarness, "ws"+strings.TrimPrefix(server.URL, "http"))
}

func TestGeneratedCSharpEarlyEvent(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not installed")
	}
	event := socketPacket(t, "early")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("version") != "v1" {
			t.Errorf("handshake version: %s", r.URL.RawQuery)
			http.Error(w, "bad version", 400)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "done")
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		frame, err := (wire.Envelope{Type: wire.FrameEvent, Event: "packet", Payload: event}).MarshalBinary()
		if err != nil {
			t.Errorf("marshal event: %v", err)
			return
		}
		// Send as soon as the WebSocket is accepted, before the client issues a request.
		if err := ws.Write(ctx, websocket.MessageBinary, frame); err != nil {
			t.Errorf("send early event: %v", err)
			return
		}
		request, ok := readCSharpRequest(t, ctx, ws)
		if !ok {
			return
		}
		frame, err = (wire.Envelope{Type: wire.FrameResponse, ID: request.ID, Payload: request.Payload}).MarshalBinary()
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		if err := ws.Write(ctx, websocket.MessageBinary, frame); err != nil {
			t.Errorf("send response: %v", err)
			return
		}
		// Keep the connection open until the harness has registered and observed the event.
		_, _, _ = ws.Read(ctx)
	}))
	defer server.Close()
	runCSharp(t, earlyEventHarness, "ws"+strings.TrimPrefix(server.URL, "http"))
}

func TestGeneratedCSharpDisposePending(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK not installed")
	}
	event := socketPacket(t, "received")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "done")
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if _, ok := readCSharpRequest(t, ctx, ws); !ok {
			return
		}
		frame, err := (wire.Envelope{Type: wire.FrameEvent, Event: "packet", Payload: event}).MarshalBinary()
		if err != nil {
			t.Errorf("marshal event: %v", err)
			return
		}
		if err := ws.Write(ctx, websocket.MessageBinary, frame); err != nil {
			t.Errorf("send event: %v", err)
			return
		}
		// Never respond: only the client's explicit DisposeAsync can fail the call.
		_, _, _ = ws.Read(ctx)
	}))
	defer server.Close()
	runCSharp(t, disposeHarness, "ws"+strings.TrimPrefix(server.URL, "http"))
}

func readCSharpRequest(t *testing.T, ctx context.Context, ws *websocket.Conn) (wire.Envelope, bool) {
	t.Helper()
	typ, data, err := ws.Read(ctx)
	if err != nil {
		t.Errorf("read request: %v", err)
		return wire.Envelope{}, false
	}
	var request wire.Envelope
	if typ != websocket.MessageBinary {
		t.Errorf("request is not binary: %v", typ)
		return request, false
	}
	if err := request.UnmarshalBinary(data); err != nil {
		t.Errorf("decode request: %v", err)
		return request, false
	}
	if request.Type != wire.FrameRequest || request.Method != "chat_send_message" {
		t.Errorf("unexpected request: %+v", request)
		return request, false
	}
	return request, true
}
