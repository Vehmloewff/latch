package chatappclient

import (
	"reflect"
	"testing"
	"time"

	"github.com/vehmloewff/latch/wire"
)

func TestProtocolRoundTripsChatMessage(t *testing.T) {
	original := ChatMessage{
		ID: 7, Room: "general", SenderID: "alice", Text: "hello",
		SentAt: time.Unix(123, 456).UTC(),
	}
	data, err := wire.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ChatMessage
	if err := wire.Decode(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("decoded message differs:\n got: %#v\nwant: %#v", decoded, original)
	}
}

func TestProtocolRoundTripsEventUnion(t *testing.T) {
	original := Event{
		Kind: "message",
		Message: &MessageReceived{Message: ChatMessage{
			ID: 8, Room: "general", SenderID: "bob", Text: "hi Alice",
			SentAt: time.Unix(456, 789).UTC(),
		}},
	}
	data, err := wire.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err := wire.Decode(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("decoded event differs:\n got: %#v\nwant: %#v", decoded, original)
	}
}
