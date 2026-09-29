package api_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vehmloewff/latch/chat_app_example/api"
	chatappclient "github.com/vehmloewff/latch/chat_app_example/golang"
	"github.com/vehmloewff/latch/client"
)

func connectChatClient(t *testing.T, url string) (*chatappclient.ConnectedLatchClient, <-chan chatappclient.Event) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan chatappclient.Event, 16)
	connected, err := chatappclient.New(url, func(event chatappclient.Event) { events <- event }).Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = connected.Close() })
	return connected, events
}

func nextChatEvent(t *testing.T, events <-chan chatappclient.Event) chatappclient.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for chat event")
		return chatappclient.Event{}
	}
}

func requireChatError(t *testing.T, err error, code string) {
	t.Helper()
	var chatErr *client.Error
	if !errors.As(err, &chatErr) || chatErr.Code != code {
		t.Fatalf("error = %v, want client error code %q", err, code)
	}
}

func TestBuildServesValidationFanoutAndHistory(t *testing.T) {
	httpServer := httptest.NewServer(api.Build())
	t.Cleanup(httpServer.Close)
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	alice, aliceEvents := connectChatClient(t, url)
	bob, bobEvents := connectChatClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rooms, err := alice.ChatListRooms(ctx, chatappclient.ListRoomsRequest{})
	if err != nil {
		t.Fatalf("ChatListRooms: %v", err)
	}
	if strings.Join(rooms.Rooms, ",") != "general,random" {
		t.Fatalf("rooms = %#v, want general,random", rooms.Rooms)
	}

	if _, err := alice.ChatJoinRoom(ctx, chatappclient.JoinRoomRequest{}); err == nil {
		t.Fatal("empty join request unexpectedly succeeded")
	} else {
		requireChatError(t, err, "invalid_request")
	}
	if _, err := alice.ChatSendMessage(ctx, chatappclient.SendMessageRequest{}); err == nil {
		t.Fatal("empty send request unexpectedly succeeded")
	} else {
		requireChatError(t, err, "invalid_request")
	}

	if _, err := alice.ChatJoinRoom(ctx, chatappclient.JoinRoomRequest{Room: "general", UserID: "alice"}); err != nil {
		t.Fatalf("alice join: %v", err)
	}
	alicePresence := nextChatEvent(t, aliceEvents)
	if alicePresence.Kind != "presence" || alicePresence.Presence == nil || alicePresence.Presence.UserID != "alice" {
		t.Fatalf("alice join event = %#v", alicePresence)
	}

	joined, err := bob.ChatJoinRoom(ctx, chatappclient.JoinRoomRequest{Room: "general", UserID: "bob"})
	if err != nil {
		t.Fatalf("bob join: %v", err)
	}
	if len(joined.MemberIDs) != 2 || !contains(joined.MemberIDs, "alice") || !contains(joined.MemberIDs, "bob") {
		t.Fatalf("bob members = %#v, want alice and bob", joined.MemberIDs)
	}
	if event := nextChatEvent(t, aliceEvents); event.Kind != "presence" || event.Presence == nil || event.Presence.UserID != "bob" {
		t.Fatalf("alice bob-presence event = %#v", event)
	}
	if event := nextChatEvent(t, bobEvents); event.Kind != "presence" || event.Presence == nil || event.Presence.UserID != "bob" {
		t.Fatalf("bob presence event = %#v", event)
	}

	sent, err := alice.ChatSendMessage(ctx, chatappclient.SendMessageRequest{Room: "general", SenderID: "alice", Text: "hello"})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if sent.Message.ID == 0 || sent.Message.Text != "hello" || sent.Message.SenderID != "alice" {
		t.Fatalf("sent message = %#v", sent.Message)
	}

	for name, events := range map[string]<-chan chatappclient.Event{"alice": aliceEvents, "bob": bobEvents} {
		event := nextChatEvent(t, events)
		if event.Kind != "message" || event.Message == nil || event.Message.Message.Text != "hello" {
			t.Fatalf("%s message event = %#v", name, event)
		}
	}

	history, err := bob.ChatHistory(ctx, chatappclient.HistoryRequest{Room: "general"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history.Messages) != 1 || history.Messages[0].Text != "hello" {
		t.Fatalf("history = %#v, want one hello message", history.Messages)
	}
	unknownHistory, err := bob.ChatHistory(ctx, chatappclient.HistoryRequest{Room: "unknown"})
	if err != nil {
		t.Fatalf("unknown-room history: %v", err)
	}
	if len(unknownHistory.Messages) != 0 {
		t.Fatalf("unknown-room history = %#v, want empty", unknownHistory.Messages)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
