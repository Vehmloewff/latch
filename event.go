package latchwire

import (
	"fmt"
	"reflect"
)

// ConnSender is the minimal capability an EventDef needs to deliver a typed
// event. It is implemented by *Conn[C] for every connect-parameter type C.
// Go does not allow a method (EventDef[T].Send) to introduce a second type
// parameter, so the connection's own type parameter C is erased behind this
// interface instead.
type ConnSender interface {
	sendEventFrame(name string, payload any) error
}

// EventRegistration is implemented by *EventDef[T] for every payload type T.
// It lets Server[C].RegisterEvent accept any EventDef without RegisterEvent
// itself needing a new type parameter.
type EventRegistration interface {
	eventName() string
	payloadGoType() reflect.Type
}

// EventDef is a compile-time-typed declaration of a server-to-client event.
// Construct one with Event[T], register it with Server.RegisterEvent, and
// send through it with Send.
type EventDef[T any] struct {
	name string
}

// Event declares a new server-to-client event carrying payloads of type T,
// identified on the wire by name (e.g. "user.updated"). It must still be
// passed to Server.RegisterEvent before it can be sent or generated.
func Event[T any](name string) *EventDef[T] {
	return &EventDef[T]{name: name}
}

// Name returns the event's registered wire name.
func (e *EventDef[T]) Name() string { return e.name }

func (e *EventDef[T]) eventName() string { return e.name }

func (e *EventDef[T]) payloadGoType() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

// Send delivers payload as a typed event to conn. It is safe to call from
// any goroutine, including concurrently with other sends on the same or
// different connections. If conn's OnConnect handler is still running, the
// event is buffered and delivered immediately after the "connected" frame.
func (e *EventDef[T]) Send(conn ConnSender, payload T) error {
	if e.name == "" {
		return fmt.Errorf("latchwire: cannot send an event with an empty name")
	}
	return conn.sendEventFrame(e.name, payload)
}
