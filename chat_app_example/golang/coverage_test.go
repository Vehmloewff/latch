package chatappclient

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vehmloewff/latch/chat_app_example/api"
)

func TestGeneratedClientWrappersRunAgainstChatExampleServer(t *testing.T) {
	httpServer := httptest.NewServer(api.Build())
	t.Cleanup(httpServer.Close)
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan Event, 4)
	states := make(chan ConnectionState, 3)
	connected, err := New(url, func(event Event) { events <- event }, func(state ConnectionState) { states <- state }).Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer connected.Close()

	for _, want := range []ConnectionState{ConnectionStateConnecting, ConnectionStateConnected} {
		if got := nextGeneratedState(t, states); got != want {
			t.Fatalf("state = %q, want %q", got, want)
		}
	}

	rooms, err := connected.ChatListRooms(ctx, ListRoomsRequest{})
	if err != nil {
		t.Fatalf("ChatListRooms: %v", err)
	}
	if len(rooms.Rooms) != 2 || rooms.Rooms[0] != "general" || rooms.Rooms[1] != "random" {
		t.Fatalf("rooms = %#v, want general and random", rooms.Rooms)
	}

	joined, err := connected.ChatJoinRoom(ctx, JoinRoomRequest{Room: "general", UserID: "alice"})
	if err != nil {
		t.Fatalf("ChatJoinRoom: %v", err)
	}
	if joined.Room != "general" || len(joined.MemberIDs) != 1 || joined.MemberIDs[0] != "alice" {
		t.Fatalf("join response = %#v, want Alice in general", joined)
	}
	if event := nextGeneratedEvent(t, events); event.Kind != "presence" || event.Presence == nil || event.Presence.UserID != "alice" {
		t.Fatalf("join event = %#v, want Alice presence", event)
	}

	history, err := connected.ChatHistory(ctx, HistoryRequest{Room: "general"})
	if err != nil {
		t.Fatalf("ChatHistory before send: %v", err)
	}
	if len(history.Messages) != 0 {
		t.Fatalf("initial history = %#v, want empty", history.Messages)
	}

	sent, err := connected.ChatSendMessage(ctx, SendMessageRequest{Room: "general", SenderID: "alice", Text: "hello"})
	if err != nil {
		t.Fatalf("ChatSendMessage: %v", err)
	}
	if sent.Message.Room != "general" || sent.Message.SenderID != "alice" || sent.Message.Text != "hello" || sent.Message.ID == 0 {
		t.Fatalf("send response = %#v, want populated message", sent)
	}
	if event := nextGeneratedEvent(t, events); event.Kind != "message" || event.Message == nil || event.Message.Message.Text != "hello" {
		t.Fatalf("message event = %#v, want hello message", event)
	}

	history, err = connected.ChatHistory(ctx, HistoryRequest{Room: "general"})
	if err != nil {
		t.Fatalf("ChatHistory after send: %v", err)
	}
	if len(history.Messages) != 1 || history.Messages[0].Text != "hello" {
		t.Fatalf("final history = %#v, want one hello message", history.Messages)
	}

	if err := connected.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-connected.Closed():
	case <-time.After(time.Second):
		t.Fatal("Closed channel was not closed after Close")
	}
	if got := nextGeneratedState(t, states); got != ConnectionStateOffline {
		t.Fatalf("state after close = %q, want offline", got)
	}
}

func TestConnectionFailureReportsOffline(t *testing.T) {
	states := make(chan ConnectionState, 2)
	_, err := New("://invalid", nil, func(state ConnectionState) { states <- state }).Connect(context.Background())
	if err == nil {
		t.Fatal("expected invalid URL to fail")
	}
	for _, want := range []ConnectionState{ConnectionStateConnecting, ConnectionStateOffline} {
		if got := nextGeneratedState(t, states); got != want {
			t.Fatalf("state = %q, want %q", got, want)
		}
	}
}

func nextGeneratedState(t *testing.T, states <-chan ConnectionState) ConnectionState {
	t.Helper()
	select {
	case state := <-states:
		return state
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connection state")
		return ""
	}
}

func nextGeneratedEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for generated client event")
		return Event{}
	}
}
