package latchwire

import (
	"reflect"

	"github.com/vehmloewff/latchwire/jsonschema"
	"github.com/vehmloewff/latchwire/protocol"
	"github.com/vehmloewff/latchwire/reflectapi"
)

// methodEntry is the internal registry record for one registered method.
type methodEntry struct {
	name    string
	adapter *reflectapi.HandlerAdapter

	requestRef  protocol.TypeRef
	responseRef protocol.TypeRef

	requestValidator  *jsonschema.Validator
	responseValidator *jsonschema.Validator // only set when Options.ValidateResponses
}

// eventEntry is the internal registry record for one registered event.
type eventEntry struct {
	name        string
	payloadType reflect.Type
	payloadRef  protocol.TypeRef
	validator   *jsonschema.Validator // only set when Options.ValidateResponses
}

// MethodDescriptor is a read-only view of a registered method, returned by
// Server.Methods for introspection and tooling.
type MethodDescriptor struct {
	Name         string
	RequestType  reflect.Type
	ResponseType reflect.Type
}

// EventDescriptor is a read-only view of a registered event, returned by
// Server.Events for introspection and tooling.
type EventDescriptor struct {
	Name        string
	PayloadType reflect.Type
}
