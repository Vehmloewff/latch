// Package api defines the complete protocol used by the chat_app_example.
package api

import (
	"context"

	"sync"
	"time"

	"github.com/vehmloewff/latch"
)

// JoinRoomRequest identifies the user and room for a connection.
type JoinRoomRequest struct {
	Room   string `latch:"1"`
	UserID string `latch:"2"`
}

type JoinRoomResponse struct {
	Room      string   `latch:"1"`
	MemberIDs []string `latch:"2"`
}

type ListRoomsRequest struct{}

type ListRoomsResponse struct {
	Rooms []string `latch:"1"`
}

type HistoryRequest struct {
	Room string `latch:"1"`
}

type ChatMessage struct {
	ID       int32     `latch:"1"`
	Room     string    `latch:"2"`
	SenderID string    `latch:"3"`
	Text     string    `latch:"4"`
	SentAt   time.Time `latch:"5"`
}

type HistoryResponse struct {
	Messages []ChatMessage `latch:"1"`
}

type SendMessageRequest struct {
	Room     string `latch:"1"`
	SenderID string `latch:"2"`
	Text     string `latch:"3"`
}

type SendMessageResponse struct {
	Message ChatMessage `latch:"1"`
}

type PresenceChanged struct {
	Room   string `latch:"1"`
	UserID string `latch:"2"`
	Online bool   `latch:"3"`
}

type MessageReceived struct {
	Message ChatMessage `latch:"1"`
}

// Event is the server-to-client event union. Kind selects the populated field.
type Event struct {
	Kind     string           `latch:"1"`
	Message  *MessageReceived `latch:"2,omitempty"`
	Presence *PresenceChanged `latch:"3,omitempty"`
}

type State struct {
	connection *connection
}

type connection struct {
	emitter latch.Emitter[Event]
	userID  string
	rooms   map[string]bool
}

// Build creates an in-memory chat service. It intentionally keeps state in the
// example so the two-client walkthrough can demonstrate real fan-out.
func Build() *latch.Server[State] {
	server := latch.New[State](latch.Options{ProtocolVersion: "1"})
	var mu sync.Mutex
	connections := map[*latch.Conn]*connection{}
	messages := map[string][]ChatMessage{
		"general": {},
		"random":  {},
	}
	var nextMessageID int32

	broadcast := func(event Event, room string) {
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range connections {
			if conn.rooms[room] {
				_ = conn.emitter.Send(event)
			}
		}
	}

	server.OnConnect(func(_ context.Context, emitter latch.Emitter[Event], conn *latch.Conn) (State, error) {
		mu.Lock()
		state := &connection{emitter: emitter, rooms: map[string]bool{}}
		connections[conn] = state
		mu.Unlock()
		return State{connection: state}, nil
	})

	server.OnDisconnect(func(_ context.Context, state State) {
		mu.Lock()
		defer mu.Unlock()
		for conn, value := range connections {
			if value == state.connection {
				delete(connections, conn)
				break
			}
		}
	})

	server.Register("chat_list_rooms", func(_ context.Context, _ State, _ ListRoomsRequest) (ListRoomsResponse, error) {
		return ListRoomsResponse{Rooms: []string{"general", "random"}}, nil
	})

	server.Register("chat_join_room", func(_ context.Context, state State, req JoinRoomRequest) (JoinRoomResponse, error) {
		if req.Room == "" || req.UserID == "" {
			return JoinRoomResponse{}, latch.NewError("invalid_request", "room and userId are required")
		}
		mu.Lock()
		state.connection.userID = req.UserID
		state.connection.rooms[req.Room] = true
		members := make([]string, 0, len(connections))
		for _, value := range connections {
			if value.rooms[req.Room] && value.userID != "" {
				members = append(members, value.userID)
			}
		}
		mu.Unlock()
		broadcast(Event{Kind: "presence", Presence: &PresenceChanged{Room: req.Room, UserID: req.UserID, Online: true}}, req.Room)
		return JoinRoomResponse{Room: req.Room, MemberIDs: members}, nil
	})

	server.Register("chat_history", func(_ context.Context, _ State, req HistoryRequest) (HistoryResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		return HistoryResponse{Messages: append([]ChatMessage(nil), messages[req.Room]...)}, nil
	})

	server.Register("chat_send_message", func(_ context.Context, _ State, req SendMessageRequest) (SendMessageResponse, error) {
		if req.Room == "" || req.SenderID == "" || req.Text == "" {
			return SendMessageResponse{}, latch.NewError("invalid_request", "room, senderId, and text are required")
		}
		mu.Lock()
		nextMessageID++
		message := ChatMessage{ID: nextMessageID, Room: req.Room, SenderID: req.SenderID, Text: req.Text, SentAt: time.Now().UTC()}
		messages[req.Room] = append(messages[req.Room], message)
		mu.Unlock()
		broadcast(Event{Kind: "message", Message: &MessageReceived{Message: message}}, req.Room)
		return SendMessageResponse{Message: message}, nil
	})

	return server
}
