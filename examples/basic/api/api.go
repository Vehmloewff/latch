// Package api is the single handwritten source of truth for the "basic"
// example protocol: Go types, registered methods, one event type, and
// an OnConnect handler. Both examples/basic/server (which serves it) and
// examples/basic/gen (which generates TypeScript/Dart/Go clients from it)
// call Build, so the served protocol and the generated clients can never
// drift apart. It deliberately covers every shape spec section 49 asks the
// cross-language integration suite to exercise: nested types, nullable and
// optional fields, two events, an application error, and (by virtue of
// Latchwire's own concurrency support) concurrent calls.
package api

import (
	"context"

	latchwire "github.com/vehmloewff/latch"
)

// SubscribeRequest is the payload for the "room.subscribe" method.
type SubscribeRequest struct {
	Room string `json:"room" jsonschema:"minLength=1"`
}

// SubscribeResponse is the response for the "room.subscribe" method.
type SubscribeResponse struct {
	OK bool `json:"ok"`
}

// ListRoomsRequest is the payload for the "room.list" method. It carries no
// meaningful fields but must still be a named struct type.
type ListRoomsRequest struct{}

// ListRoomsResponse is the response for the "room.list" method,
// demonstrating a top-level slice-of-primitive field.
type ListRoomsResponse struct {
	Rooms []string `json:"rooms"`
}

// Address is a nested type used by ProfileGetResponse, with one optional
// field (Zip).
type Address struct {
	City string  `json:"city"`
	Zip  *string `json:"zip,omitempty"`
}

// Profile is a nested type with a required-nullable field (Nickname), a
// nested struct field (Address), and a slice field (Tags).
type Profile struct {
	Name     string   `json:"name"`
	Nickname *string  `json:"nickname"`
	Address  Address  `json:"address"`
	Tags     []string `json:"tags"`
}

// ProfileGetRequest is the payload for the "profile.get" method.
type ProfileGetRequest struct {
	UserID string `json:"userId" jsonschema:"minLength=1"`
}

// ProfileGetResponse is the response for the "profile.get" method.
type ProfileGetResponse struct {
	Profile Profile `json:"profile"`
}

// MessageReceived is one variant of the server event.
type MessageReceived struct {
	Room string `json:"room"`
	Text string `json:"text"`
}

// PresenceChanged is one variant of the server event.
type PresenceChanged struct {
	UserID string `json:"userId"`
	Online bool   `json:"online"`
}

// Event is the one server-to-client event. Kind selects the variant and only
// the corresponding pointer is populated.
type Event struct {
	Kind     string           `json:"kind"`
	Message  *MessageReceived `json:"message,omitempty"`
	Presence *PresenceChanged `json:"presence,omitempty"`
}

// State is the per-connection application state created by OnConnect and
// supplied to every method and OnDisconnect callback.
type State struct {
	ConnectedPath string
}

// Build constructs a fresh, fully registered Server. It is the one place
// the "basic" protocol is defined; generated clients and the live server
// are both produced by reflecting over exactly this registration.
func Build() *latchwire.Server[State] {
	lw := latchwire.New[State](latchwire.Options{
		ProtocolVersion: "1",
	})

	lw.OnConnect(func(ctx context.Context, emitter latchwire.Emitter[Event], conn *latchwire.Conn) (State, error) {
		if err := emitter.Send(Event{
			Kind: "message",
			Message: &MessageReceived{
				Room: "lobby",
				Text: "welcome",
			},
		}); err != nil {
			return State{}, err
		}
		if err := emitter.Send(Event{
			Kind: "presence",
			Presence: &PresenceChanged{
				UserID: "self",
				Online: true,
			},
		}); err != nil {
			return State{}, err
		}
		return State{ConnectedPath: conn.Request().URL.Path}, nil
	})

	lw.Register("room_subscribe", func(
		ctx context.Context,
		state State,
		req SubscribeRequest,
	) (SubscribeResponse, error) {
		return SubscribeResponse{OK: true}, nil
	})

	lw.Register("room_list", func(
		ctx context.Context,
		state State,
		req ListRoomsRequest,
	) (ListRoomsResponse, error) {
		return ListRoomsResponse{Rooms: []string{"general", "lobby", "random"}}, nil
	})

	lw.Register("profile_get", func(ctx context.Context, state State, req ProfileGetRequest) (ProfileGetResponse, error) {
		if req.UserID == "missing" {
			return ProfileGetResponse{}, latchwire.NewError("not_found", "user not found")
		}

		nickname := "the " + req.UserID
		var zip *string
		if req.UserID != "no-zip" {
			z := "00000"
			zip = &z
		}

		return ProfileGetResponse{
			Profile: Profile{
				Name:     "User " + req.UserID,
				Nickname: &nickname,
				Address: Address{
					City: "Springfield",
					Zip:  zip,
				},
				Tags: []string{"tag-a", "tag-b"},
			},
		}, nil
	})

	lw.OnDisconnect(func(ctx context.Context, state State) {
		_ = state
	})

	return lw
}
