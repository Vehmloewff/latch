package typescript

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vehmloewff/latchwire/protocol"
)

// tsType renders the TypeScript type for ref, given the display name chosen
// for every named type by names.AssignTypeNames.
func tsType(ref protocol.TypeRef, typeNames map[string]string) string {
	switch ref.Kind {
	case protocol.KindString:
		return "string"
	case protocol.KindBool:
		return "boolean"
	case protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32,
		protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32,
		protocol.KindFloat32, protocol.KindFloat64:
		return "number"
	case protocol.KindTime:
		// RFC3339 string on the wire; see docs/design-notes.md.
		return "string"
	case protocol.KindPointer:
		return tsType(*ref.Elem, typeNames) + " | null"
	case protocol.KindSlice, protocol.KindArray:
		return arrayElemType(*ref.Elem, typeNames) + "[]"
	case protocol.KindMap:
		return "Record<string, " + tsType(*ref.MapValue, typeNames) + ">"
	case protocol.KindStruct, protocol.KindEnum:
		name, ok := typeNames[ref.NamedType]
		if !ok {
			panic(fmt.Sprintf("typescript: unknown named type %q", ref.NamedType))
		}
		return name
	default:
		panic(fmt.Sprintf("typescript: unhandled kind %q", ref.Kind))
	}
}

// arrayElemType parenthesizes union-shaped element types (nullable elements,
// e.g. (string | null)[]) so the generated array type parses as intended.
func arrayElemType(ref protocol.TypeRef, typeNames map[string]string) string {
	t := tsType(ref, typeNames)
	if ref.Kind == protocol.KindPointer {
		return "(" + t + ")"
	}
	return t
}

// sortedNamedTypes returns p.Types ordered by their generated display name,
// for deterministic output.
func sortedNamedTypes(types []*protocol.NamedType, typeNames map[string]string) []*protocol.NamedType {
	out := append([]*protocol.NamedType(nil), types...)
	sort.Slice(out, func(i, j int) bool {
		return typeNames[out[i].ID] < typeNames[out[j].ID]
	})
	return out
}

// generateTypesFile renders types.ts: one TypeScript interface per struct
// named type, one string-literal union per enum named type.
func generateTypesFile(p *protocol.Protocol, typeNames map[string]string) string {
	var b strings.Builder

	for _, t := range sortedNamedTypes(p.Types, typeNames) {
		name := typeNames[t.ID]
		switch t.Kind {
		case protocol.KindEnum:
			b.WriteString(fmt.Sprintf("export type %s =\n", name))
			for i, v := range t.EnumValues {
				sep := " "
				if i == 0 {
					sep = "  "
				} else {
					sep = "  | "
				}
				b.WriteString(fmt.Sprintf("%s%q", sep, v))
				if i < len(t.EnumValues)-1 {
					b.WriteString("\n")
				}
			}
			b.WriteString(";\n\n")

		case protocol.KindStruct:
			b.WriteString(fmt.Sprintf("export interface %s {\n", name))
			for _, f := range t.Fields {
				opt := ""
				if f.Optional {
					opt = "?"
				}
				b.WriteString(fmt.Sprintf("  %s%s: %s;\n", f.JSONName, opt, tsType(f.Type, typeNames)))
			}
			b.WriteString("}\n\n")

		default:
			panic(fmt.Sprintf("typescript: named type %q has unsupported kind %q", t.ID, t.Kind))
		}
	}

	return b.String()
}
