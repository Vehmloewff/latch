// Package reflectapi is the single reflection stage of Latch. It walks
// Go reflect.Type values reachable from the event payload and method
// requests/responses, and produces the normalized
// protocol.Protocol IR. No other package in Latch inspects reflect.Type
// directly.
package reflectapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/vehmloewff/latch/protocol"
)

var (
	timeType  = reflect.TypeOf(time.Time{})
	marshaler = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
)

// Registry incrementally resolves reflect.Type values into protocol.TypeRef
// values, deduplicating named struct/enum types by reflect.Type identity so
// that a type used from many methods/events is emitted exactly once.
type Registry struct {
	byType map[reflect.Type]*protocol.NamedType
	order  []*protocol.NamedType

	// enumValues tracks the tag-declared values for each named string enum
	// type, keyed by reflect.Type, so repeated uses of the same Go type are
	// checked for consistency.
	enumValues map[reflect.Type][]string
}

// NewRegistry creates an empty type registry.
func NewRegistry() *Registry {
	return &Registry{
		byType:     make(map[reflect.Type]*protocol.NamedType),
		enumValues: make(map[reflect.Type][]string),
	}
}

// Types returns every named type discovered so far, sorted deterministically
// by ID.
func (r *Registry) Types() []*protocol.NamedType {
	out := make([]*protocol.NamedType, len(r.order))
	copy(out, r.order)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Resolve converts a reflect.Type into a protocol.TypeRef, registering any
// named struct/enum types it discovers along the way.
func (r *Registry) Resolve(t reflect.Type) (protocol.TypeRef, error) {
	return r.resolve(t, map[reflect.Type]bool{})
}

func (r *Registry) resolve(t reflect.Type, visiting map[reflect.Type]bool) (protocol.TypeRef, error) {
	switch {
	case t == timeType:
		return protocol.TypeRef{Kind: protocol.KindTime}, nil

	case t.Implements(marshaler) || reflect.PointerTo(t).Implements(marshaler):
		return protocol.TypeRef{}, fmt.Errorf(
			"type %s implements json.Marshaler; Latch v1 cannot infer its wire shape (unsupported custom marshaler)",
			t.String(),
		)
	}

	switch t.Kind() {
	case reflect.String:
		if t.PkgPath() == "" {
			return protocol.TypeRef{Kind: protocol.KindString}, nil
		}
		return r.resolveNamedString(t)

	case reflect.Bool:
		return protocol.TypeRef{Kind: protocol.KindBool}, nil
	case reflect.Int:
		return protocol.TypeRef{Kind: protocol.KindInt}, nil
	case reflect.Int8:
		return protocol.TypeRef{Kind: protocol.KindInt8}, nil
	case reflect.Int16:
		return protocol.TypeRef{Kind: protocol.KindInt16}, nil
	case reflect.Int32:
		return protocol.TypeRef{Kind: protocol.KindInt32}, nil
	case reflect.Int64:
		return protocol.TypeRef{}, fmt.Errorf(
			"unsupported type %s: int64 fields are rejected in Latch v1 because they cannot be represented "+
				"safely as a JavaScript number and Latch does not yet support a custom wire-encoding policy; "+
				"use int32 (or a string) instead",
			t.String(),
		)
	case reflect.Uint:
		return protocol.TypeRef{Kind: protocol.KindUint}, nil
	case reflect.Uint8:
		return protocol.TypeRef{Kind: protocol.KindUint8}, nil
	case reflect.Uint16:
		return protocol.TypeRef{Kind: protocol.KindUint16}, nil
	case reflect.Uint32:
		return protocol.TypeRef{Kind: protocol.KindUint32}, nil
	case reflect.Uint64:
		return protocol.TypeRef{}, fmt.Errorf(
			"unsupported type %s: uint64 fields are rejected in Latch v1 because they cannot be represented "+
				"safely as a JavaScript number and Latch does not yet support a custom wire-encoding policy; "+
				"use uint32 (or a string) instead",
			t.String(),
		)
	case reflect.Float32:
		return protocol.TypeRef{Kind: protocol.KindFloat32}, nil
	case reflect.Float64:
		return protocol.TypeRef{Kind: protocol.KindFloat64}, nil

	case reflect.Pointer:
		if visiting[t] {
			return protocol.TypeRef{}, fmt.Errorf("unsupported recursive pointer type %s", t.String())
		}
		visiting = cloneVisiting(visiting)
		visiting[t] = true
		elem, err := r.resolve(t.Elem(), visiting)
		if err != nil {
			return protocol.TypeRef{}, err
		}
		return protocol.TypeRef{Kind: protocol.KindPointer, Elem: &elem}, nil

	case reflect.Slice:
		elem, err := r.resolve(t.Elem(), visiting)
		if err != nil {
			return protocol.TypeRef{}, fmt.Errorf("slice element of %s: %w", t.String(), err)
		}
		return protocol.TypeRef{Kind: protocol.KindSlice, Elem: &elem}, nil

	case reflect.Array:
		elem, err := r.resolve(t.Elem(), visiting)
		if err != nil {
			return protocol.TypeRef{}, fmt.Errorf("array element of %s: %w", t.String(), err)
		}
		return protocol.TypeRef{Kind: protocol.KindArray, Elem: &elem, ArrayLen: t.Len()}, nil

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return protocol.TypeRef{}, fmt.Errorf("unsupported map key type %s on %s: only string keys are supported", t.Key().String(), t.String())
		}
		val, err := r.resolve(t.Elem(), visiting)
		if err != nil {
			return protocol.TypeRef{}, fmt.Errorf("map value of %s: %w", t.String(), err)
		}
		return protocol.TypeRef{Kind: protocol.KindMap, MapValue: &val}, nil

	case reflect.Struct:
		return r.resolveStruct(t, visiting)

	default:
		return protocol.TypeRef{}, fmt.Errorf("unsupported Go type %s (kind %s)", t.String(), t.Kind())
	}
}

func (r *Registry) resolveNamedString(t reflect.Type) (protocol.TypeRef, error) {
	// A named string type with no declared enum values is treated as a
	// plain string on the wire — Go itself places no value restriction on
	// it, so this matches Go's own semantics. See docs/design-notes.md
	// ("Enum declaration convention").
	values, hasEnum := r.enumValues[t]
	if !hasEnum {
		return protocol.TypeRef{Kind: protocol.KindString}, nil
	}

	id := typeID(t)
	if existing, ok := r.byType[t]; ok {
		_ = existing
		return protocol.TypeRef{Kind: protocol.KindEnum, NamedType: id}, nil
	}

	nt := &protocol.NamedType{
		ID:         id,
		GoPkgPath:  t.PkgPath(),
		GoName:     t.Name(),
		Kind:       protocol.KindEnum,
		EnumBase:   protocol.KindString,
		EnumValues: values,
	}
	r.byType[t] = nt
	r.order = append(r.order, nt)
	return protocol.TypeRef{Kind: protocol.KindEnum, NamedType: id}, nil
}

// DeclareEnum registers the allowed values for a named string type. It must
// be called (via the public latch.Enum tag mechanism, see field.go)
// before that type is resolved for the first time.
func (r *Registry) declareEnum(t reflect.Type, values []string) error {
	if existing, ok := r.enumValues[t]; ok {
		if !equalStrings(existing, values) {
			return fmt.Errorf(
				"type %s declares conflicting enum values in different jsonschema_enum tags (%v vs %v)",
				t.String(), existing, values,
			)
		}
		return nil
	}
	r.enumValues[t] = values
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *Registry) resolveStruct(t reflect.Type, visiting map[reflect.Type]bool) (protocol.TypeRef, error) {
	if t.Name() == "" {
		return protocol.TypeRef{}, fmt.Errorf(
			"anonymous struct types are not supported; define a named type instead",
		)
	}
	if t.PkgPath() == "" {
		return protocol.TypeRef{}, fmt.Errorf("struct type %s has no package path", t.Name())
	}

	if nt, ok := r.byType[t]; ok {
		return protocol.TypeRef{Kind: protocol.KindStruct, NamedType: nt.ID}, nil
	}

	id := typeID(t)
	nt := &protocol.NamedType{
		ID:        id,
		GoPkgPath: t.PkgPath(),
		GoName:    t.Name(),
		Kind:      protocol.KindStruct,
	}
	// Register before walking fields so self-referential types (e.g. a tree
	// node with []*Node children) resolve back to this same NamedType
	// instead of recursing forever.
	r.byType[t] = nt
	r.order = append(r.order, nt)

	visiting = cloneVisiting(visiting)
	visiting[t] = true

	var fields []protocol.Field

	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		if sf.Anonymous {
			return protocol.TypeRef{}, fmt.Errorf(
				"struct %s: embedded field %s is not supported in Latch v1; use a named field instead",
				t.String(), sf.Name,
			)
		}
		if !sf.IsExported() {
			continue
		}

		optional := parseLatchOptional(sf)

		if err := declareFieldEnum(r, sf); err != nil {
			return protocol.TypeRef{}, fmt.Errorf("struct %s field %s: %w", t.String(), sf.Name, err)
		}

		fieldType := sf.Type
		nullable := false
		if fieldType.Kind() == reflect.Pointer {
			nullable = true
		}

		ref, err := r.resolve(fieldType, visiting)
		if err != nil {
			return protocol.TypeRef{}, fmt.Errorf("struct %s field %s: %w", t.String(), sf.Name, err)
		}

		fields = append(fields, protocol.Field{
			GoName:   sf.Name,
			Type:     ref,
			Optional: optional,
			Nullable: nullable,
		})
	}

	nt.Fields = fields
	return protocol.TypeRef{Kind: protocol.KindStruct, NamedType: id}, nil
}

func cloneVisiting(v map[reflect.Type]bool) map[reflect.Type]bool {
	out := make(map[reflect.Type]bool, len(v)+1)
	for k, val := range v {
		out[k] = val
	}
	return out
}

func typeID(t reflect.Type) string {
	return t.PkgPath() + "." + t.Name()
}

func parseLatchOptional(sf reflect.StructField) bool {
	tag, ok := sf.Tag.Lookup("latch")
	if !ok || tag == "" {
		return false
	}
	for _, option := range strings.Split(tag, ",")[1:] {
		if option == "omitempty" {
			return true
		}
	}
	return false
}

func declareFieldEnum(r *Registry, sf reflect.StructField) error {
	tag, ok := sf.Tag.Lookup("jsonschema_enum")
	if !ok || tag == "" {
		return nil
	}
	t := sf.Type
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.String || t.PkgPath() == "" {
		return fmt.Errorf("jsonschema_enum tag only applies to named string types, got %s", sf.Type.String())
	}
	values := strings.Split(tag, ",")
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return r.declareEnum(t, values)
}
