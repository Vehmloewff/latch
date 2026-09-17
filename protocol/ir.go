// Package protocol defines Latchwire's normalized intermediate representation
// (IR) of a registered API. Exactly one stage (package reflectapi) converts
// Go reflection into this IR. Every downstream consumer — JSON Schema
// generation, runtime validation, the manifest, and the TypeScript/Dart/Go
// code generators — reads this IR and never re-inspects Go reflect.Type
// values directly.
package protocol

// Kind identifies the shape of a TypeRef or NamedType.
type Kind string

const (
	KindString  Kind = "string"
	KindBool    Kind = "bool"
	KindInt     Kind = "int"
	KindInt8    Kind = "int8"
	KindInt16   Kind = "int16"
	KindInt32   Kind = "int32"
	KindInt64   Kind = "int64"
	KindUint    Kind = "uint"
	KindUint8   Kind = "uint8"
	KindUint16  Kind = "uint16"
	KindUint32  Kind = "uint32"
	KindUint64  Kind = "uint64"
	KindFloat32 Kind = "float32"
	KindFloat64 Kind = "float64"
	KindStruct  Kind = "struct"
	KindSlice   Kind = "slice"
	KindArray   Kind = "array"
	KindMap     Kind = "map"
	KindPointer Kind = "pointer"
	KindTime    Kind = "time"
	KindEnum    Kind = "enum"
)

// IsInteger reports whether k is one of the signed/unsigned integer kinds.
func (k Kind) IsInteger() bool {
	switch k {
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		return true
	}
	return false
}

// Is64Bit reports whether k is int64 or uint64, the kinds whose values can
// exceed the JavaScript safe-integer range.
func (k Kind) Is64Bit() bool {
	return k == KindInt64 || k == KindUint64
}

// Constraints holds optional JSON Schema validation keywords parsed from a
// field's `jsonschema:"..."` struct tag.
type Constraints struct {
	MinLength *int
	MaxLength *int
	Minimum   *float64
	Maximum   *float64
	Pattern   string
	Format    string
}

// TypeRef refers to a type used in a field, method request/response, event
// payload, or connect payload. For KindStruct and KindEnum, NamedType is the
// ID of the corresponding entry in Protocol.Types.
type TypeRef struct {
	Kind Kind

	// NamedType is set when Kind is KindStruct or KindEnum.
	NamedType string

	// Elem is set when Kind is KindSlice, KindArray, or KindPointer.
	Elem *TypeRef

	// ArrayLen is set when Kind is KindArray.
	ArrayLen int

	// MapValue is set when Kind is KindMap. Map keys are always strings in v1.
	MapValue *TypeRef
}

// Field is one field of a NamedType of kind KindStruct.
type Field struct {
	GoName      string
	JSONName    string
	Type        TypeRef
	Optional    bool // json tag carries `,omitempty`
	Nullable    bool // Go field type is a pointer
	Constraints Constraints
}

// NamedType is a struct or enum type that generated clients emit as a
// standalone named declaration.
type NamedType struct {
	// ID is a globally unique identifier for this type, stable across a
	// single protocol build. It is derived from the Go package path and type
	// name so that identically named types from different packages remain
	// distinct.
	ID string

	GoPkgPath string
	GoName    string

	Kind Kind // KindStruct or KindEnum

	// Fields is set when Kind is KindStruct.
	Fields []Field

	// EnumBase and EnumValues are set when Kind is KindEnum.
	EnumBase   Kind
	EnumValues []string
}

// Method is one registered RPC method.
type Method struct {
	Name         string
	RequestType  TypeRef
	ResponseType TypeRef
}

// Event is one registered server-to-client event.
type Event struct {
	Name        string
	PayloadType TypeRef
}

// Protocol is the complete normalized representation of a Latchwire API,
// ready to drive JSON Schema generation, runtime validation, the manifest,
// and every language code generator.
type Protocol struct {
	Name        string
	Version     string
	ConnectType TypeRef
	Methods     []Method
	Events      []Event

	// Types holds every named struct/enum type reachable from ConnectType,
	// any Method, or any Event, sorted deterministically by ID.
	Types []*NamedType
}

// TypeByID returns the named type with the given ID, or nil if absent.
func (p *Protocol) TypeByID(id string) *NamedType {
	for _, t := range p.Types {
		if t.ID == id {
			return t
		}
	}
	return nil
}
