package latch

import (
	"reflect"

	"github.com/vehmloewff/latch/protocol"
	"github.com/vehmloewff/latch/reflectapi"
)

// methodEntry is the internal registry record for one registered method.
type methodEntry struct {
	name    string
	adapter *reflectapi.HandlerAdapter

	requestRef  protocol.TypeRef
	responseRef protocol.TypeRef
}

// eventEntry is retained only for source compatibility with old tooling.
type eventEntry struct {
	name        string
	payloadType reflect.Type
	payloadRef  protocol.TypeRef
}

// MethodDescriptor is a read-only view of a registered method, returned by
// Server.Methods for introspection and tooling.
type MethodDescriptor struct {
	Name         string
	RequestType  reflect.Type
	ResponseType reflect.Type
}

// EventDescriptor is retained for source compatibility with old tooling.
type EventDescriptor struct {
	Name        string
	PayloadType reflect.Type
}
