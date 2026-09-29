package chatappclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/vehmloewff/latch/wire"
)

func TestInitialConnectRetriesTransientFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		_, _, _ = ws.Read(r.Context())
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	states := make(chan ConnectionState, 8)
	conn, err := New("ws"+strings.TrimPrefix(server.URL, "http"), nil, func(s ConnectionState) { states <- s }).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if attempts.Load() != 2 {
		t.Fatalf("dial attempts = %d", attempts.Load())
	}
	for _, want := range []ConnectionState{ConnectionStateConnecting, ConnectionStateOffline, ConnectionStateConnecting, ConnectionStateConnected} {
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("state = %s, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestRequestHookRunsBeforeEveryDial(t *testing.T) {
	var constructed atomic.Int32
	auth := make(chan string, 4)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		auth <- r.Header.Get("Authorization")
		if n == 1 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if n == 2 {
			_ = ws.Close(websocket.StatusGoingAway, "reconnect")
			return
		}
		defer ws.CloseNow()
		_, _, _ = ws.Read(r.Context())
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	states := make(chan ConnectionState, 16)
	client := NewWithOptions("ws"+strings.TrimPrefix(server.URL, "http"), nil, Options{
		OnConnectionStateChange: func(state ConnectionState) { states <- state },
		OnRequestConstructed: func(headers http.Header) {
			headers.Set("Authorization", fmt.Sprintf("Bearer token-%d", constructed.Add(1)))
		},
	})
	conn, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, want := range []ConnectionState{
		ConnectionStateConnecting, ConnectionStateOffline, ConnectionStateConnecting,
		ConnectionStateConnected, ConnectionStateOffline, ConnectionStateConnecting, ConnectionStateConnected,
	} {
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("state = %s, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for n := 1; n <= 3; n++ {
		select {
		case got := <-auth:
			if want := fmt.Sprintf("Bearer token-%d", n); got != want {
				t.Fatalf("attempt %d Authorization = %q, want %q", n, got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if got := constructed.Load(); got != 3 {
		t.Fatalf("hook called %d times, want 3", got)
	}
}

func TestOfflineCallsRespectContextAndClose(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) != 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.Close(websocket.StatusGoingAway, "offline")
	}))
	defer server.Close()
	states := make(chan ConnectionState, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := New("ws"+strings.TrimPrefix(server.URL, "http"), nil, func(s ConnectionState) { states <- s }).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, want := range []ConnectionState{ConnectionStateConnecting, ConnectionStateConnected, ConnectionStateOffline} {
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("state = %s, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	if _, err := conn.ChatListRooms(short, ListRoomsRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("offline call error = %v, want deadline", err)
	}
	result := make(chan error, 1)
	go func() { _, err := conn.ChatListRooms(ctx, ListRoomsRequest{}); result <- err }()
	select {
	case err := <-result:
		t.Fatalf("offline call completed before close: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "connection_closed") {
			t.Fatalf("queued call error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if attempts.Load() != 1 {
		t.Fatalf("retried after close: %d attempts", attempts.Load())
	}
}

func TestReconnectQueuesNewCallsButDoesNotReplayInFlight(t *testing.T) {
	var connections atomic.Int32
	requests := make(chan int32, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		n := connections.Add(1)
		if n == 1 {
			_, raw, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			var env wire.Envelope
			if err := env.UnmarshalBinary(raw); err != nil {
				return
			}
			requests <- n
			return // fail the in-flight request without responding
		}
		_, raw, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		var env wire.Envelope
		if err := env.UnmarshalBinary(raw); err != nil {
			return
		}
		requests <- n
		payload, _ := wire.Encode(ListRoomsResponse{Rooms: []string{"restored"}})
		response, _ := (wire.Envelope{Type: wire.FrameResponse, ID: env.ID, Payload: payload}).MarshalBinary()
		if err := ws.Write(r.Context(), websocket.MessageBinary, response); err != nil {
			return
		}
		eventPayload, _ := wire.Encode(Event{Kind: "restored"})
		event, _ := (wire.Envelope{Type: wire.FrameEvent, Payload: eventPayload}).MarshalBinary()
		if err := ws.Write(r.Context(), websocket.MessageBinary, event); err != nil {
			return
		}
		_, _, _ = ws.Read(r.Context())
	}))
	defer server.Close()
	states := make(chan ConnectionState, 16)
	events := make(chan Event, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := New("ws"+strings.TrimPrefix(server.URL, "http"), func(e Event) { events <- e }, func(s ConnectionState) { states <- s }).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ChatListRooms(ctx, ListRoomsRequest{}); err == nil {
		t.Fatal("in-flight request succeeded after socket closed")
	}
	select {
	case n := <-requests:
		if n != 1 {
			t.Fatalf("first request on connection %d", n)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	result := make(chan error, 1)
	go func() {
		response, err := conn.ChatListRooms(ctx, ListRoomsRequest{})
		if err == nil && (len(response.Rooms) != 1 || response.Rooms[0] != "restored") {
			err = errors.New("unexpected response")
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case n := <-requests:
		if n != 2 {
			t.Fatalf("queued request on connection %d", n)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case e := <-events:
		if e.Kind != "restored" {
			t.Fatalf("event: %+v", e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, want := range []ConnectionState{ConnectionStateConnecting, ConnectionStateConnected, ConnectionStateOffline, ConnectionStateConnecting, ConnectionStateConnected} {
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("state = %s, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if connections.Load() != 2 {
		t.Fatalf("replayed first request: %d connections", connections.Load())
	}
	_ = conn.Close()
	if _, err := conn.ChatListRooms(ctx, ListRoomsRequest{}); err == nil {
		t.Fatal("call after close succeeded")
	}
	select {
	case <-conn.Closed():
	default:
		t.Fatal("close did not signal shutdown")
	}
}
