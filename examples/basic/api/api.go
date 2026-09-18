// Package api is the single handwritten source of truth for the "basic"
// example protocol: Go types, registered methods, registered events, and
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
	"fmt"

	"github.com/vehmloewff/latchwire"
	"github.com/vehmloewff/report"
)

// ConnectParams is the connection setup payload every client must send.
type ConnectParams struct {
	Token string `json:"token" jsonschema:"minLength=1"`
}

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

// MessageReceived is the payload for the "message.received" server event.
type MessageReceived struct {
	Room string `json:"room"`
	Text string `json:"text"`
}

// PresenceChanged is the payload for the "presence.changed" server event.
type PresenceChanged struct {
	UserID string `json:"userId"`
	Online bool   `json:"online"`
}

// MessageReceivedEvent is the "message.received" event declaration, shared
// by OnConnect (which sends a welcome message) and by anything else in the
// application that wants to publish a message.
var MessageReceivedEvent = latchwire.Event[MessageReceived]("message.received")

// PresenceChangedEvent is the "presence.changed" event declaration, sent
// once by OnConnect to demonstrate a second, independently typed event
// stream.
var PresenceChangedEvent = latchwire.Event[PresenceChanged]("presence.changed")

// Build constructs a fresh, fully registered Server. It is the one place
// the "basic" protocol is defined; generated clients and the live server
// are both produced by reflecting over exactly this registration.
func Build() *latchwire.Server[ConnectParams] {
	lw := latchwire.New[ConnectParams](latchwire.Options{
		ProtocolName:    "basic",
		ProtocolVersion: "1",
	})

	if err := lw.RegisterEvent(MessageReceivedEvent); err != nil {
		panic(fmt.Errorf("basic api: %w", err))
	}
	if err := lw.RegisterEvent(PresenceChangedEvent); err != nil {
		panic(fmt.Errorf("basic api: %w", err))
	}

	err := lw.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) report.Err {
		if conn.Params().Token == "" {
			return report.New("a token is required").Hint(report.HintNotPermitted)
		}
		if err := MessageReceivedEvent.Send(conn, MessageReceived{
			Room: "lobby",
			Text: "welcome",
		}); err != nil {
			return report.From(err)
		}
		if err := PresenceChangedEvent.Send(conn, PresenceChanged{
			UserID: "self",
			Online: true,
		}); err != nil {
			return report.From(err)
		}
		return nil
	})
	if err != nil {
		panic(fmt.Errorf("basic api: %w", err))
	}

	err = lw.Register("room.subscribe", func(
		ctx context.Context,
		conn *latchwire.Conn[ConnectParams],
		req SubscribeRequest,
	) (SubscribeResponse, report.Err) {
		return SubscribeResponse{OK: true}, nil
	})
	if err != nil {
		panic(fmt.Errorf("basic api: %w", err))
	}

	err = lw.Register("room.list", func(
		ctx context.Context,
		conn *latchwire.Conn[ConnectParams],
		req ListRoomsRequest,
	) (ListRoomsResponse, report.Err) {
		return ListRoomsResponse{Rooms: []string{"general", "lobby", "random"}}, nil
	})
	if err != nil {
		panic(fmt.Errorf("basic api: %w", err))
	}

	err = lw.Register("profile.get", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req ProfileGetRequest) (ProfileGetResponse, report.Err) {
		if req.UserID == "missing" {
			return ProfileGetResponse{}, report.New("user not found").Hint(report.HintNotFound)
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
	if err != nil {
		panic(fmt.Errorf("basic api: %w", err))
	}

	return lw
}
