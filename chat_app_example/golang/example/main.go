package main

import (
	"context"
	"fmt"
	"os"
	"time"

	chat "github.com/vehmloewff/latch/chat_app_example/golang"
)

// Story: Bob has opened a chat app and Alice is already using the same room.
// We create two independent connections so this example is about clients, not
// about pretending that one connection represents the whole application.
func main() {
	url := os.Getenv("SERVER_URL")
	if url == "" {
		url = "ws://127.0.0.1:8080/ws"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Callbacks are installed before either connection starts receiving events.
	bobEvents := make(chan chat.Event, 8)
	aliceEvents := make(chan chat.Event, 8)
	// Bob has opened the chat app and connected to the server.
	bob, err := chat.New(url, func(event chat.Event) { bobEvents <- event }).Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer bob.Close()

	// Alice opens her own connection; a real UI would keep this connection
	// alongside Bob's connection in a separate session or process.
	alice, err := chat.New(url, func(event chat.Event) { aliceEvents <- event }).Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer alice.Close()

	// Bob asks the server which rooms are available before showing the room list.
	rooms, err := bob.ChatListRooms(ctx, chat.ListRoomsRequest{})
	if err != nil {
		panic(err)
	}
	fmt.Println("Bob sees rooms:", rooms.Rooms)

	// Bob joins general. The server emits a presence event to members of that room.
	if _, err := bob.ChatJoinRoom(ctx, chat.JoinRoomRequest{Room: "general", UserID: "bob"}); err != nil {
		panic(err)
	}
	fmt.Println("Bob joined #general:", describeEvent(nextEvent(bobEvents)))

	// Alice joins the same room from her separate connection.
	if _, err := alice.ChatJoinRoom(ctx, chat.JoinRoomRequest{Room: "general", UserID: "alice"}); err != nil {
		panic(err)
	}
	fmt.Println("Bob sees Alice join:", describeEvent(nextEvent(bobEvents)))
	fmt.Println("Alice sees her presence:", describeEvent(nextEvent(aliceEvents)))

	// Alice sends a message. The request resolves for Alice and the server emits
	// the resulting message event to both Alice and Bob.
	sent, err := alice.ChatSendMessage(ctx, chat.SendMessageRequest{
		Room: "general", SenderID: "alice", Text: "Hi Bob, welcome!",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("Alice sent:", sent.Message.Text)
	fmt.Println("Alice receives message event:", describeEvent(nextEvent(aliceEvents)))
	fmt.Println("Bob receives message event:", describeEvent(nextEvent(bobEvents)))

	// Bob can restore the conversation without relying on events that arrived
	// before his UI mounted by asking for the room history.
	history, err := bob.ChatHistory(ctx, chat.HistoryRequest{Room: "general"})
	if err != nil {
		panic(err)
	}
	fmt.Println("Bob loads history:", len(history.Messages), "message(s)")
}

func describeEvent(event chat.Event) string {
	if event.Message != nil {
		return fmt.Sprintf("message from %s: %s", event.Message.Message.SenderID, event.Message.Message.Text)
	}
	if event.Presence != nil {
		return fmt.Sprintf("%s is online in #%s", event.Presence.UserID, event.Presence.Room)
	}
	return event.Kind
}

func nextEvent(events <-chan chat.Event) chat.Event {
	select {
	case event := <-events:
		return event
	case <-time.After(5 * time.Second):
		panic("timed out waiting for chat event")
	}
}
