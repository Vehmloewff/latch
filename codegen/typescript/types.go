package typescript

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// tsType renders the TypeScript type for ref, given the display name chosen
// for every named type by names.AssignTypeNames.
func tsType(ref protocol.TypeRef, typeNames map[string]string) string {
	switch ref.Kind {
	case protocol.KindString:
		return "string"
	case protocol.KindBool:
		return "boolean"
	case protocol.KindInt64, protocol.KindUint64:
		return "bigint"
	case protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32,
		protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32,
		protocol.KindFloat32, protocol.KindFloat64:
		return "number"
	case protocol.KindTime:
		// Go encodes time.Time as signed Unix nanoseconds. Keep it as bigint
		// so current timestamps remain exact in JavaScript.
		return "bigint"
	case protocol.KindPointer:
		return tsType(*ref.Elem, typeNames) + " | null"
	case protocol.KindSlice:
		if ref.Elem.Kind == protocol.KindUint8 {
			return "Uint8Array"
		}
		return arrayElemType(*ref.Elem, typeNames) + "[]"
	case protocol.KindArray:
		if ref.Elem.Kind == protocol.KindUint8 {
			return "Uint8Array"
		}
		if ref.ArrayLen <= 0 {
			return "[]"
		}
		elem := arrayElemType(*ref.Elem, typeNames)
		return "[" + strings.TrimSuffix(strings.Repeat(elem+", ", ref.ArrayLen), ", ") + "]"
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
				b.WriteString(fmt.Sprintf("  %s%s: %s;\n", names.CamelCase(f.GoName), opt, tsType(f.Type, typeNames)))
			}
			b.WriteString("}\n\n")

		default:
			panic(fmt.Sprintf("typescript: named type %q has unsupported kind %q", t.ID, t.Kind))
		}
	}

	b.WriteString("export const __latchWireTypes: WireTypeRegistry = {\n")
	for _, t := range sortedNamedTypes(p.Types, typeNames) {
		name := typeNames[t.ID]
		b.WriteString(fmt.Sprintf("  %q: ", name))
		if t.Kind == protocol.KindEnum {
			b.WriteString("{ kind: \"enum\" },\n")
			continue
		}
		b.WriteString("{ kind: \"struct\", fields: {")
		for i, f := range t.Fields {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(fmt.Sprintf("%d: { name: %q, type: %s", i+1, names.CamelCase(f.GoName), wireTypeExpr(f.Type, typeNames)))
			if f.Optional {
				b.WriteString(", optional: true")
			}
			b.WriteString(" }")
		}
		b.WriteString(" }},\n")
	}
	b.WriteString("};\n\n")

	return b.String()
}

func integerWireType(kind protocol.Kind) string {
	if kind == protocol.KindInt {
		return `{ kind: "int" }`
	}
	if kind == protocol.KindUint {
		return `{ kind: "uint" }`
	}
	signed := kind == protocol.KindInt8 || kind == protocol.KindInt16 || kind == protocol.KindInt32 || kind == protocol.KindInt64
	bits := 64
	switch kind {
	case protocol.KindInt8, protocol.KindUint8:
		bits = 8
	case protocol.KindInt16, protocol.KindUint16:
		bits = 16
	case protocol.KindInt32, protocol.KindUint32:
		bits = 32
	}

	var min, max string
	if signed {
		min = fmt.Sprintf("-%d", uint64(1)<<(bits-1))
		max = fmt.Sprintf("%d", (uint64(1)<<(bits-1))-1)
	} else {
		min = "0"
		if bits == 64 {
			max = fmt.Sprintf("%d", ^uint64(0))
		} else {
			max = fmt.Sprintf("%d", (uint64(1)<<bits)-1)
		}
	}
	if bits == 64 {
		min += "n"
		max += "n"
	}
	if signed {
		return fmt.Sprintf(`{ kind: "int", bits: %d, min: %s, max: %s }`, bits, min, max)
	}
	return fmt.Sprintf(`{ kind: "uint", bits: %d, min: %s, max: %s }`, bits, min, max)
}

func wireTypeExpr(ref protocol.TypeRef, typeNames map[string]string) string {
	switch ref.Kind {
	case protocol.KindString:
		return `{ kind: "string" }`
	case protocol.KindBool:
		return `{ kind: "bool" }`
	case protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt64,
		protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64:
		return integerWireType(ref.Kind)
	case protocol.KindFloat32:
		return `{ kind: "float32" }`
	case protocol.KindFloat64:
		return `{ kind: "float64" }`
	case protocol.KindTime:
		return `{ kind: "time", unit: "nanoseconds" }`
	case protocol.KindEnum, protocol.KindStruct:
		return fmt.Sprintf(`{ kind: "named", name: %q }`, typeNames[ref.NamedType])
	case protocol.KindPointer:
		return fmt.Sprintf(`{ kind: "nullable", elem: %s }`, wireTypeExpr(*ref.Elem, typeNames))
	case protocol.KindSlice:
		if ref.Elem.Kind == protocol.KindUint8 {
			return `{ kind: "bytes" }`
		}
		return fmt.Sprintf(`{ kind: "list", elem: %s }`, wireTypeExpr(*ref.Elem, typeNames))
	case protocol.KindArray:
		if ref.Elem.Kind == protocol.KindUint8 {
			return fmt.Sprintf(`{ kind: "bytes", length: %d }`, ref.ArrayLen)
		}
		return fmt.Sprintf(`{ kind: "array", length: %d, elem: %s }`, ref.ArrayLen, wireTypeExpr(*ref.Elem, typeNames))
	case protocol.KindMap:
		return fmt.Sprintf(`{ kind: "map", value: %s }`, wireTypeExpr(*ref.MapValue, typeNames))
	default:
		panic(fmt.Sprintf("typescript: cannot build wire type for %q", ref.Kind))
	}
}
