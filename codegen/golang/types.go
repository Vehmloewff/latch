package golang

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// goType renders the Go type for ref, given the display name chosen for
// every named type by names.AssignTypeNames. Unlike TypeScript and
// Dart, Go needs no hand-written (de)serialization: encoding/json already
// implements exactly the semantics Latch's IR was designed around
// (struct tags, omitempty, pointers for nullability), so the generated
// client is just plain Go structs.
func goType(ref protocol.TypeRef, typeNames map[string]string) string {
	switch ref.Kind {
	case protocol.KindString:
		return "string"
	case protocol.KindBool:
		return "bool"
	case protocol.KindInt:
		return "int"
	case protocol.KindInt8:
		return "int8"
	case protocol.KindInt16:
		return "int16"
	case protocol.KindInt32:
		return "int32"
	case protocol.KindUint:
		return "uint"
	case protocol.KindUint8:
		return "uint8"
	case protocol.KindUint16:
		return "uint16"
	case protocol.KindUint32:
		return "uint32"
	case protocol.KindFloat32:
		return "float32"
	case protocol.KindFloat64:
		return "float64"
	case protocol.KindTime:
		return "time.Time"
	case protocol.KindPointer:
		return "*" + goType(*ref.Elem, typeNames)
	case protocol.KindSlice, protocol.KindArray:
		// Latch always generates a slice, even for a fixed-size Go
		// array on the server: JSON itself has no fixed-length array type,
		// so the client-side representation gains nothing from Go's [N]T
		// and loses easy zero-value handling.
		return "[]" + goType(*ref.Elem, typeNames)
	case protocol.KindMap:
		return "map[string]" + goType(*ref.MapValue, typeNames)
	case protocol.KindStruct, protocol.KindEnum:
		name, ok := typeNames[ref.NamedType]
		if !ok {
			panic(fmt.Sprintf("golang: unknown named type %q", ref.NamedType))
		}
		return name
	default:
		panic(fmt.Sprintf("golang: unhandled kind %q", ref.Kind))
	}
}

// usesTime reports whether ref (transitively) contains a KindTime, so the
// generator knows whether types.go needs to import "time".
func usesTime(ref protocol.TypeRef) bool {
	switch ref.Kind {
	case protocol.KindTime:
		return true
	case protocol.KindPointer, protocol.KindSlice, protocol.KindArray:
		return usesTime(*ref.Elem)
	case protocol.KindMap:
		return usesTime(*ref.MapValue)
	default:
		return false
	}
}

func sortedNamedTypes(types []*protocol.NamedType, typeNames map[string]string) []*protocol.NamedType {
	out := append([]*protocol.NamedType(nil), types...)
	sort.Slice(out, func(i, j int) bool { return typeNames[out[i].ID] < typeNames[out[j].ID] })
	return out
}

// generateTypesFile renders types.go: one Go struct (with encoding/json
// struct tags matching the wire exactly) per struct named type, one named
// string type plus a const block per enum named type.
func fieldNumber(t *protocol.NamedType, f protocol.Field) int {
	for i, candidate := range t.Fields {
		if candidate.GoName == f.GoName {
			return i + 1
		}
	}
	return 0
}

func generateTypesFile(pkg string, p *protocol.Protocol, typeNames map[string]string) string {
	sorted := sortedNamedTypes(p.Types, typeNames)

	needsTime := false
	for _, t := range sorted {
		for _, f := range t.Fields {
			if usesTime(f.Type) {
				needsTime = true
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	if needsTime {
		b.WriteString("import \"time\"\n\n")
	}

	for _, t := range sorted {
		name := typeNames[t.ID]
		switch t.Kind {
		case protocol.KindEnum:
			fmt.Fprintf(&b, "type %s string\n\n", name)
			b.WriteString("const (\n")
			for _, v := range t.EnumValues {
				fmt.Fprintf(&b, "\t%s%s %s = %q\n", name, names.PascalCase(v), name, v)
			}
			b.WriteString(")\n\n")

		case protocol.KindStruct:
			fmt.Fprintf(&b, "type %s struct {\n", name)
			for _, f := range t.Fields {
				tag := f.JSONName
				if f.Optional {
					tag += ",omitempty"
				}
				fmt.Fprintf(&b, "\t%s %s `json:%q latch:%q`\n", f.GoName, goType(f.Type, typeNames), tag, fmt.Sprintf("%d%s", fieldNumber(t, f), func() string {
					if f.Optional {
						return ",omitempty"
					}
					return ""
				}()))
			}
			b.WriteString("}\n\n")

		default:
			panic(fmt.Sprintf("golang: named type %q has unsupported kind %q", t.ID, t.Kind))
		}
	}

	return b.String()
}
