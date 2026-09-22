package chatappclient

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vehmloewff/latch/examples/chat_app/api"
)

func TestGeneratedClientWrappersRunAgainstChatExampleServer(t *testing.T) {
	httpServer := httptest.NewServer(api.Build())
	t.Cleanup(httpServer.Close)
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connected, err := New(url).Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer connected.Close()

	if connected.Events() == nil {
		t.Fatal("Events() returned nil channel")
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
	if event := nextGeneratedEvent(t, connected.Events()); event.Kind != "presence" || event.Presence == nil || event.Presence.UserID != "alice" {
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
	if event := nextGeneratedEvent(t, connected.Events()); event.Kind != "message" || event.Message == nil || event.Message.Message.Text != "hello" {
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
