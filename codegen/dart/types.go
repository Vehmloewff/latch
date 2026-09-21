package dart

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// dartType renders the Dart type for ref, given the display name chosen for
// every named type by names.AssignTypeNames. It always yields a
// syntactically complete type, collapsing a hypothetical pointer-to-pointer
// to a single "?" rather than the invalid "??".
func dartType(ref protocol.TypeRef, typeNames map[string]string) string {
	switch ref.Kind {
	case protocol.KindString:
		return "String"
	case protocol.KindTime:
		return "DateTime"
	case protocol.KindBool:
		return "bool"
	case protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32,
		protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64:
		return "int"
	case protocol.KindFloat32, protocol.KindFloat64:
		return "double"
	case protocol.KindPointer:
		inner := dartType(*ref.Elem, typeNames)
		if strings.HasSuffix(inner, "?") {
			return inner
		}
		return inner + "?"
	case protocol.KindSlice, protocol.KindArray:
		return "List<" + dartType(*ref.Elem, typeNames) + ">"
	case protocol.KindMap:
		return "Map<String, " + dartType(*ref.MapValue, typeNames) + ">"
	case protocol.KindStruct, protocol.KindEnum:
		name, ok := typeNames[ref.NamedType]
		if !ok {
			panic(fmt.Sprintf("dart: unknown named type %q", ref.NamedType))
		}
		return name
	default:
		panic(fmt.Sprintf("dart: unhandled kind %q", ref.Kind))
	}
}

// dartFieldName is the idiomatic lowerCamelCase Dart field name for f,
// derived from its wire JSON name (which may already be camelCase, or may
// be snake_case, dotted, etc).
func dartFieldName(f protocol.Field) string {
	return names.CamelCase(f.JSONName)
}

// dartFieldType is the declared Dart field type for f: nullable whenever the
// field is optional (may be absent) or nullable (Go pointer), matching the
// "collapse absent-vs-null to a single nullable type" compromise documented
// in docs/design-notes.md ("Dart optional vs nullable").
func dartFieldType(f protocol.Field, typeNames map[string]string) string {
	base := dartType(f.Type, typeNames)
	if f.Optional && !strings.HasSuffix(base, "?") {
		return base + "?"
	}
	return base
}

// decodeExpr returns a Dart expression that decodes a BinaryCodec value into
// ref's Dart type.
func decodeExpr(expr string, ref protocol.TypeRef, typeNames map[string]string) string {
	switch ref.Kind {
	case protocol.KindString:
		return fmt.Sprintf("%s as String", expr)
	case protocol.KindTime:
		return fmt.Sprintf("%s as DateTime", expr)
	case protocol.KindBool:
		return fmt.Sprintf("%s as bool", expr)
	case protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32:
		return fmt.Sprintf("%s as int", expr)
	case protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64:
		return fmt.Sprintf("(%s as UIntValue).value.toInt()", expr)
	case protocol.KindFloat32:
		return fmt.Sprintf("(%s as Float32Value).value", expr)
	case protocol.KindFloat64:
		return fmt.Sprintf("%s as double", expr)
	case protocol.KindPointer:
		inner := decodeExpr(expr, *ref.Elem, typeNames)
		return fmt.Sprintf("%s == null ? null : %s", expr, inner)
	case protocol.KindSlice, protocol.KindArray:
		if ref.Elem.Kind == protocol.KindUint8 {
			return fmt.Sprintf("%s as Uint8List", expr)
		}
		elem := decodeExpr("e", *ref.Elem, typeNames)
		return fmt.Sprintf("(%s as List).map((e) => %s).toList()", expr, elem)
	case protocol.KindMap:
		val := decodeExpr("v", *ref.MapValue, typeNames)
		return fmt.Sprintf("(%s as Map).map((k, v) => MapEntry(k as String, %s))", expr, val)
	case protocol.KindStruct:
		return fmt.Sprintf("%s.fromBinary(%s)", typeNames[ref.NamedType], expr)
	case protocol.KindEnum:
		return fmt.Sprintf("%s.fromBinary(%s as String)", typeNames[ref.NamedType], expr)
	default:
		panic(fmt.Sprintf("dart: unhandled kind %q", ref.Kind))
	}
}

// fieldDecodeExpr returns the full Dart expression assigned to field f
// inside a generated fromBinary factory.
func dartFieldNumber(t *protocol.NamedType, f protocol.Field) int {
	for i, candidate := range t.Fields {
		if candidate.GoName == f.GoName {
			return i + 1
		}
	}
	return 0
}

func fieldDecodeExpr(f protocol.Field, fieldNumber int, typeNames map[string]string) string {
	accessor := fmt.Sprintf("fields[%d]", fieldNumber)
	inner := decodeExpr(accessor, f.Type, typeNames)
	if f.Optional {
		return fmt.Sprintf("fields.containsKey(%d) ? (%s) : null", fieldNumber, inner)
	}
	return inner
}

// encodeExpr returns a Dart expression that converts a typed Dart value to
// the corresponding BinaryCodec value.
func encodeExpr(expr string, ref protocol.TypeRef, typeNames map[string]string) string {
	switch ref.Kind {
	case protocol.KindString, protocol.KindBool,
		protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32,
		protocol.KindFloat64, protocol.KindTime:
		return expr
	case protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64:
		return fmt.Sprintf("UIntValue(%s)", expr)
	case protocol.KindFloat32:
		return fmt.Sprintf("Float32Value(%s)", expr)
	case protocol.KindPointer:
		inner := encodeExpr(expr+"!", *ref.Elem, typeNames)
		return fmt.Sprintf("%s == null ? null : %s", expr, inner)
	case protocol.KindSlice, protocol.KindArray:
		if ref.Elem.Kind == protocol.KindUint8 {
			return fmt.Sprintf("Uint8List.fromList(%s)", expr)
		}
		elem := encodeExpr("e", *ref.Elem, typeNames)
		return fmt.Sprintf("%s.map((e) => %s).toList()", expr, elem)
	case protocol.KindMap:
		val := encodeExpr("v", *ref.MapValue, typeNames)
		return fmt.Sprintf("%s.map((k, v) => MapEntry(k, %s))", expr, val)
	case protocol.KindStruct, protocol.KindEnum:
		return fmt.Sprintf("%s.toBinary()", expr)
	default:
		panic(fmt.Sprintf("dart: unhandled kind %q", ref.Kind))
	}
}

// fieldEncodeStatement returns the Dart statement(s) that add field f to a
// `map` variable inside a generated toJson method.
func fieldEncodeStatement(t *protocol.NamedType, f protocol.Field, typeNames map[string]string) string {
	id := dartFieldNumber(t, f)
	if f.Optional {
		base := f.Type
		if base.Kind == protocol.KindPointer {
			base = *base.Elem
		}
		return fmt.Sprintf(
			"    if (%s != null) {\n      fields[%d] = %s;\n    }\n",
			dartFieldName(f), id, encodeExpr(dartFieldName(f)+"!", base, typeNames),
		)
	}
	return fmt.Sprintf("    fields[%d] = %s;\n", id, encodeExpr(dartFieldName(f), f.Type, typeNames))
}

// sortedNamedTypes returns types ordered by their generated display name,
// for deterministic output.
func sortedNamedTypes(types []*protocol.NamedType, typeNames map[string]string) []*protocol.NamedType {
	out := append([]*protocol.NamedType(nil), types...)
	sort.Slice(out, func(i, j int) bool { return typeNames[out[i].ID] < typeNames[out[j].ID] })
	return out
}

// generateModelsFile renders models.dart: one Dart class (with fromJson/
// toJson) per struct named type, one enhanced enum per enum named type.
func generateModelsFile(p *protocol.Protocol, typeNames map[string]string) string {
	var b strings.Builder

	hasEnum := false
	for _, t := range p.Types {
		if t.Kind == protocol.KindEnum {
			hasEnum = true
			break
		}
	}
	if hasEnum {
		b.WriteString("// Binary runtime is embedded in client.dart.\n\n")
	}

	for _, t := range sortedNamedTypes(p.Types, typeNames) {
		name := typeNames[t.ID]
		switch t.Kind {
		case protocol.KindEnum:
			b.WriteString(fmt.Sprintf("enum %s {\n", name))
			for i, v := range t.EnumValues {
				sep := ","
				if i == len(t.EnumValues)-1 {
					sep = ";"
				}
				b.WriteString(fmt.Sprintf("  %s(%q)%s\n", names.CamelCase(v), v, sep))
			}
			b.WriteString("\n  final String wireValue;\n")
			fmt.Fprintf(&b, "  const %s(this.wireValue);\n\n", name)
			fmt.Fprintf(&b, "  static %s fromBinary(String value) {\n", name)
			fmt.Fprintf(&b, "    for (final v in %s.values) {\n", name)
			b.WriteString("      if (v.wireValue == value) return v;\n")
			b.WriteString("    }\n")
			fmt.Fprintf(&b, "    throw LatchDecodeException('unknown %s value: ' + value);\n", name)
			b.WriteString("  }\n\n")
			b.WriteString("  String toBinary() => wireValue;\n")
			b.WriteString("}\n\n")

		case protocol.KindStruct:
			b.WriteString(fmt.Sprintf("class %s {\n", name))
			for _, f := range t.Fields {
				fmt.Fprintf(&b, "  final %s %s;\n", dartFieldType(f, typeNames), dartFieldName(f))
			}
			b.WriteString("\n")

			// Dart does not allow an empty "({})" named-parameter group —
			// a zero-field struct (e.g. a request type with no payload)
			// needs a plain no-argument constructor instead.
			if len(t.Fields) == 0 {
				fmt.Fprintf(&b, "  %s();\n\n", name)
				fmt.Fprintf(&b, "  factory %s.fromBinary(Object? value) {\n", name)
				fmt.Fprintf(&b, "    return %s();\n  }\n\n", name)
			} else {
				fmt.Fprintf(&b, "  %s({\n", name)
				for _, f := range t.Fields {
					req := "required "
					if f.Optional {
						req = ""
					}
					fmt.Fprintf(&b, "    %sthis.%s,\n", req, dartFieldName(f))
				}
				b.WriteString("  });\n\n")

				fmt.Fprintf(&b, "  factory %s.fromBinary(Object? value) {\n", name)
				b.WriteString("    final fields = (value as StructValue).fields;\n")
				fmt.Fprintf(&b, "    return %s(\n", name)
				for _, f := range t.Fields {
					fmt.Fprintf(&b, "      %s: %s,\n", dartFieldName(f), fieldDecodeExpr(f, dartFieldNumber(t, f), typeNames))
				}
				b.WriteString("    );\n  }\n\n")
			}

			b.WriteString("  StructValue toBinary() {\n")
			b.WriteString("    final fields = <int, Object?>{};\n")
			for _, f := range t.Fields {
				b.WriteString(fieldEncodeStatement(t, f, typeNames))
			}
			b.WriteString("    return StructValue(fields);\n")
			b.WriteString("  }\n")
			b.WriteString("}\n\n")

		default:
			panic(fmt.Sprintf("dart: named type %q has unsupported kind %q", t.ID, t.Kind))
		}
	}

	return b.String()
}
