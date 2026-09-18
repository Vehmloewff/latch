package latchwire

import (
	"fmt"
	"reflect"
)

// Emitter is the typed capability supplied to OnConnect. E is the
// one server-to-client event payload type for the connection. Applications
// that need several event variants should represent them as one tagged E.
type Emitter[E any] struct {
	conn *Conn
}

// Send delivers one typed server-to-client event.
func (e Emitter[E]) Send(payload E) error {
	if e.conn == nil {
		return fmt.Errorf("latchwire: emitter is not attached to a connection")
	}
	return e.conn.sendEventFrame(payload)
}

// emitterRegistration lets the non-generic Server discover the payload type
// from a concrete Emitter[E] used in an OnConnect function. Go does
// not allow a generic method on Server, so this is the type-safe boundary
// between the generic emitter and the non-generic server.
type emitterRegistration interface {
	emitterType() reflect.Type
	bindEmitter(*Conn) any
}

func (e Emitter[E]) emitterType() reflect.Type {
	return reflect.TypeOf((*E)(nil)).Elem()
}

func (e Emitter[E]) bindEmitter(conn *Conn) any {
	return Emitter[E]{conn: conn}
}
