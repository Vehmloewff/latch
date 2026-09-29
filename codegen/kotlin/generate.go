// Package kotlin generates a standalone JVM Kotlin client from Latch's protocol IR.
package kotlin

import (
	"fmt"
	"math"

	"sort"
	"strings"

	"github.com/vehmloewff/latch/codegen"
	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// Options configures Kotlin source generation. The output has no package declaration;
// callers may prepend one when placing the file in their own source tree.
type Options struct{ ClientName string }

// Generate emits a standalone Kotlin/JVM source file, including its codec and transport.
func Generate(p *protocol.Protocol, opts Options) (map[string][]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("kotlin: nil protocol")
	}
	for _, t := range p.Types {
		if t == nil {
			return nil, fmt.Errorf("kotlin: nil named type")
		}
	}
	methodNames := make([]string, len(p.Methods))
	for i, m := range p.Methods {
		methodNames[i] = m.Name
	}
	if err := names.ValidateIdentifiers(methodNames); err != nil {
		return nil, fmt.Errorf("kotlin: %w", err)
	}
	typeNames, err := names.AssignTypeNames(p.Types)
	if err != nil {
		return nil, fmt.Errorf("kotlin: %w", err)
	}
	event, ok := p.EventRef()
	if !ok {
		return nil, fmt.Errorf("kotlin: protocol has no event type")
	}
	client := opts.ClientName
	if client == "" {
		client = "LatchClient"
	}
	if !identifier(client) || strings.Contains(client, ".") || isKeyword(client) {
		return nil, fmt.Errorf("kotlin: invalid client name %q", client)
	}
	used := map[string]bool{"LatchBinary": true, "LatchValue": true, "LatchTransport": true, "LatchError": true, "LatchEnvelope": true, "Connected" + client: true, client: true}
	for _, n := range typeNames {
		if !identifier(n) || strings.Contains(n, ".") || isKeyword(n) || used[n] {
			return nil, fmt.Errorf("kotlin: invalid or conflicting type name %q", n)
		}
		used[n] = true
	}
	for _, t := range p.Types {
		if t.Kind != protocol.KindStruct && t.Kind != protocol.KindEnum {
			return nil, fmt.Errorf("kotlin: unsupported named type %q: %s", t.ID, t.Kind)
		}
		members := map[string]bool{}
		if t.Kind == protocol.KindEnum {
			if t.EnumBase != protocol.KindString {
				return nil, fmt.Errorf("kotlin: enum %q must have string base", t.ID)
			}
			for _, v := range t.EnumValues {
				n := names.PascalCase(v)
				if !identifier(n) || members[n] {
					return nil, fmt.Errorf("kotlin: invalid or duplicate enum value %q", v)
				}
				members[n] = true
			}
		} else {
			numbers := map[uint64]bool{}
			for i, f := range t.Fields {
				number := fieldNumber(f, i)
				if number == 0 || number > math.MaxUint32 || numbers[number] {
					return nil, fmt.Errorf("kotlin: invalid or duplicate field number %d in %q", number, t.ID)
				}
				numbers[number] = true
				n := names.CamelCase(f.GoName)
				if !identifier(n) || members[n] || n == "fromLatch" || n == "toLatch" {
					return nil, fmt.Errorf("kotlin: invalid or duplicate field %q", f.GoName)
				}
				members[n] = true
				if err := checkRef(f.Type, typeNames); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := checkRef(event, typeNames); err != nil {
		return nil, err
	}
	methodMembers := map[string]bool{"close": true, "events": true, "onEvent": true, "onEventError": true}
	for _, m := range p.Methods {
		n := names.CamelCase(m.Name)
		if methodMembers[n] {
			return nil, fmt.Errorf("kotlin: duplicate or reserved method %q", n)
		}
		methodMembers[n] = true
		if err := checkRef(m.RequestType, typeNames); err != nil {
			return nil, err
		}
		if err := checkRef(m.ResponseType, typeNames); err != nil {
			return nil, err
		}
	}
	var b strings.Builder
	b.WriteString(codegen.HeaderComment(p.Version))
	b.WriteString(runtimeSource)
	sorted := append([]*protocol.NamedType(nil), p.Types...)
	sort.Slice(sorted, func(i, j int) bool { return typeNames[sorted[i].ID] < typeNames[sorted[j].ID] })
	for _, t := range sorted {
		renderType(&b, t, typeNames)
	}
	fmt.Fprintf(&b, "\nclass %s(private val url: String) {\n  fun connect(): java.util.concurrent.CompletableFuture<Connected%s> =\n    LatchTransport.connect(url, %q).thenApply { Connected%s(it) }\n}\n", client, client, p.Version, client)
	fmt.Fprintf(&b, "\nclass Connected%s internal constructor(private val transport: LatchTransport) : AutoCloseable {\n", client)
	fmt.Fprintf(&b, "  /** Set to receive decoded events. Callback runs on the WebSocket listener thread. */\n  var onEvent: ((%s) -> Unit)? = null\n    set(value) { field = value; transport.onEvent = if (value == null) null else { payload -> value(%s) } }\n", kotlinType(event, typeNames), decodeExpr("LatchBinary.decode(payload)", event, typeNames))
	b.WriteString("  /** Called if the connection fails, including on an event decoding/callback error. */\n  var onEventError: ((Throwable) -> Unit)? = null\n    set(value) { field = value; transport.onFailure = value }\n")
	for _, m := range p.Methods {
		fmt.Fprintf(&b, "\n  fun %s(request: %s): java.util.concurrent.CompletableFuture<%s> =\n", kotlinID(names.CamelCase(m.Name)), kotlinType(m.RequestType, typeNames), kotlinType(m.ResponseType, typeNames))
		fmt.Fprintf(&b, "    transport.call(%q, LatchBinary.encode(%s)).thenApply { %s }\n", m.Name, encodeExpr("request", m.RequestType, typeNames), decodeExpr("LatchBinary.decode(it)", m.ResponseType, typeNames))
	}
	b.WriteString("\n  override fun close() = transport.close()\n}\n")
	return map[string][]byte{"LatchClient.kt": []byte(b.String())}, nil
}

func checkRef(r protocol.TypeRef, typeNames map[string]string) error {
	switch r.Kind {
	case protocol.KindString, protocol.KindBool, protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt64, protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64, protocol.KindFloat32, protocol.KindFloat64, protocol.KindTime:
		return nil
	case protocol.KindStruct, protocol.KindEnum:
		if typeNames[r.NamedType] == "" {
			return fmt.Errorf("kotlin: unknown named type %q", r.NamedType)
		}
		return nil
	case protocol.KindPointer, protocol.KindSlice, protocol.KindArray:
		if r.Elem == nil {
			return fmt.Errorf("kotlin: missing element for %s", r.Kind)
		}
		if r.Kind == protocol.KindArray && r.ArrayLen < 0 {
			return fmt.Errorf("kotlin: negative array length")
		}
		return checkRef(*r.Elem, typeNames)
	case protocol.KindMap:
		if r.MapValue == nil {
			return fmt.Errorf("kotlin: missing map value")
		}
		return checkRef(*r.MapValue, typeNames)
	default:
		return fmt.Errorf("kotlin: unsupported kind %q", r.Kind)
	}
}

func kotlinType(r protocol.TypeRef, ns map[string]string) string {
	switch r.Kind {
	case protocol.KindString:
		return "String"
	case protocol.KindBool:
		return "Boolean"
	case protocol.KindInt, protocol.KindInt64:
		return "Long"
	case protocol.KindInt8:
		return "Byte"
	case protocol.KindInt16:
		return "Short"
	case protocol.KindInt32:
		return "Int"
	case protocol.KindUint, protocol.KindUint64:
		return "ULong"
	case protocol.KindUint8:
		return "UByte"
	case protocol.KindUint16:
		return "UShort"
	case protocol.KindUint32:
		return "UInt"
	case protocol.KindFloat32:
		return "Float"
	case protocol.KindFloat64:
		return "Double"
	case protocol.KindTime:
		return "java.time.Instant"
	case protocol.KindPointer:
		return strings.TrimSuffix(kotlinType(*r.Elem, ns), "?") + "?"
	case protocol.KindSlice, protocol.KindArray:
		if r.Kind == protocol.KindSlice && r.Elem.Kind == protocol.KindUint8 {
			return "ByteArray"
		}
		return "List<" + kotlinType(*r.Elem, ns) + ">"
	case protocol.KindMap:
		return "Map<String, " + kotlinType(*r.MapValue, ns) + ">"
	default:
		return ns[r.NamedType]
	}
}

// fieldNumber uses the wire ID supplied by the IR. Hand-built IR values
// without explicit IDs retain the positional convention.
func fieldNumber(f protocol.Field, index int) uint64 {
	if f.Number != 0 {
		return f.Number
	}
	return uint64(index + 1)
}

func fieldType(f protocol.Field, ns map[string]string) string {
	t := kotlinType(f.Type, ns)
	if (f.Optional || f.Nullable) && !strings.HasSuffix(t, "?") {
		t += "?"
	}
	return t
}

func encodeExpr(v string, r protocol.TypeRef, ns map[string]string) string {
	switch r.Kind {
	case protocol.KindPointer:
		return fmt.Sprintf("%s?.let { %s }", v, encodeExpr("it", *r.Elem, ns))
	case protocol.KindStruct:
		return v + ".toLatch()"
	case protocol.KindEnum:
		return v + ".wireValue"
	case protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt64:
		return "(" + v + ").toLong()"
	case protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64:
		return "LatchValue.Unsigned((" + v + ").toULong())"
	case protocol.KindFloat32:
		return "LatchValue.Single(" + v + ")"
	case protocol.KindTime:
		return "LatchValue.Time(" + v + ")"
	case protocol.KindSlice:
		if r.Elem.Kind == protocol.KindUint8 {
			return v
		}
		return fmt.Sprintf("(%s).map { item -> %s }", v, encodeExpr("item", *r.Elem, ns))
	case protocol.KindArray:
		return fmt.Sprintf("LatchValue.arrayInput(%s, %d).map { item -> %s }", v, r.ArrayLen, encodeExpr("item", *r.Elem, ns))
	case protocol.KindMap:
		return fmt.Sprintf("(%s).mapValues { (_, item) -> %s }", v, encodeExpr("item", *r.MapValue, ns))
	default:
		return v
	}
}

func decodeExpr(v string, r protocol.TypeRef, ns map[string]string) string {
	switch r.Kind {
	case protocol.KindPointer:
		return fmt.Sprintf("%s?.let { item -> %s }", v, decodeExpr("item", *r.Elem, ns))
	case protocol.KindString:
		return "LatchValue.string(" + v + ")"
	case protocol.KindBool:
		return "LatchValue.boolean(" + v + ")"
	case protocol.KindInt, protocol.KindInt64:
		return "LatchValue.signed(" + v + ")"
	case protocol.KindInt8:
		return "LatchValue.int8(" + v + ")"
	case protocol.KindInt16:
		return "LatchValue.int16(" + v + ")"
	case protocol.KindInt32:
		return "LatchValue.int32(" + v + ")"
	case protocol.KindUint, protocol.KindUint64:
		return "LatchValue.unsigned(" + v + ")"
	case protocol.KindUint8:
		return "LatchValue.uint8(" + v + ")"
	case protocol.KindUint16:
		return "LatchValue.uint16(" + v + ")"
	case protocol.KindUint32:
		return "LatchValue.uint32(" + v + ")"
	case protocol.KindFloat32:
		return "LatchValue.single(" + v + ")"
	case protocol.KindFloat64:
		return "LatchValue.double(" + v + ")"
	case protocol.KindTime:
		return "LatchValue.time(" + v + ")"
	case protocol.KindStruct:
		return ns[r.NamedType] + ".fromLatch(" + v + ")"
	case protocol.KindEnum:
		return ns[r.NamedType] + ".fromWire(LatchValue.string(" + v + "))"
	case protocol.KindSlice:
		if r.Elem.Kind == protocol.KindUint8 {
			return "LatchValue.bytes(" + v + ")"
		}
		return fmt.Sprintf("LatchValue.list(%s).map { item -> %s }", v, decodeExpr("item", *r.Elem, ns))
	case protocol.KindArray:
		return fmt.Sprintf("LatchValue.array(%s, %d).map { item -> %s }", v, r.ArrayLen, decodeExpr("item", *r.Elem, ns))
	case protocol.KindMap:
		return fmt.Sprintf("LatchValue.map(%s).mapValues { (_, item) -> %s }", v, decodeExpr("item", *r.MapValue, ns))
	default:
		panic("unvalidated type")
	}
}

func renderType(b *strings.Builder, t *protocol.NamedType, ns map[string]string) {
	name := ns[t.ID]
	if t.Kind == protocol.KindEnum {
		fmt.Fprintf(b, "\nenum class %s(val wireValue: String) {\n", name)
		for i, v := range t.EnumValues {
			sep := ","
			if i == len(t.EnumValues)-1 {
				sep = ";"
			}
			fmt.Fprintf(b, "  %s(%q)%s\n", kotlinID(names.PascalCase(v)), v, sep)
		}
		if len(t.EnumValues) == 0 {
			b.WriteString("  ;\n")
		}
		fmt.Fprintf(b, "  companion object {\n    fun fromWire(value: String): %s = values().firstOrNull { it.wireValue == value }\n      ?: throw LatchError(\"malformed_value\", \"unknown %s value: $value\")\n  }\n}\n", name, name)
		return
	}
	fmt.Fprintf(b, "\nclass %s(\n", name)
	for i, f := range t.Fields {
		comma := ","
		if i == len(t.Fields)-1 {
			comma = ""
		}
		defaultValue := ""
		if f.Optional {
			defaultValue = " = null"
		}
		fmt.Fprintf(b, "  val %s: %s%s%s\n", kotlinID(names.CamelCase(f.GoName)), fieldType(f, ns), defaultValue, comma)
	}
	b.WriteString(") {\n  internal fun toLatch(): Any = LatchValue.Structure(linkedMapOf<Long, Any?>().apply {\n")
	for i, f := range t.Fields {
		v := kotlinID(names.CamelCase(f.GoName))
		encoded := encodeExpr(v, f.Type, ns)
		if f.Optional {
			ref := f.Type
			if ref.Kind == protocol.KindPointer {
				ref = *ref.Elem
			}
			fmt.Fprintf(b, "    %s?.let { item -> put(%dL, %s) }\n", v, fieldNumber(f, i), encodeExpr("item", ref, ns))
		} else if f.Nullable && f.Type.Kind != protocol.KindPointer {
			fmt.Fprintf(b, "    put(%dL, %s?.let { %s })\n", fieldNumber(f, i), v, encodeExpr("it", f.Type, ns))
		} else {
			fmt.Fprintf(b, "    put(%dL, %s)\n", fieldNumber(f, i), encoded)
		}
	}
	b.WriteString("  })\n\n  companion object {\n")
	fmt.Fprintf(b, "    internal fun fromLatch(value: Any?): %s {\n      val fields = LatchValue.structure(value)\n", name)
	if len(t.Fields) == 0 {
		fmt.Fprintf(b, "      return %s()\n", name)
	} else {
		fmt.Fprintf(b, "      return %s(\n", name)
		for i, f := range t.Fields {
			number := fieldNumber(f, i)
			v := fmt.Sprintf("fields[%dL]", number)
			expr := ""
			if f.Optional || f.Nullable || f.Type.Kind == protocol.KindPointer {
				ref := f.Type
				if ref.Kind == protocol.KindPointer {
					ref = *ref.Elem
				}
				expr = fmt.Sprintf("%s?.let { item -> %s }", v, decodeExpr("item", ref, ns))
			} else {
				expr = decodeExpr(fmt.Sprintf("LatchValue.required(fields, %dL)", number), f.Type, ns)
			}
			fmt.Fprintf(b, "        %s = %s,\n", kotlinID(names.CamelCase(f.GoName)), expr)
		}
		b.WriteString("      )\n")
	}
	b.WriteString("    }\n  }\n}\n")
}

var kotlinKeywords = map[string]bool{}

func init() {
	for _, s := range strings.Fields("as break class continue do else false for fun if in interface is null object package return super this throw true try typealias typeof val var when while by catch constructor delegate dynamic field file finally get import init param property receiver set setparam where actual abstract annotation companion const crossinline data enum expect external final infix inline inner internal lateinit noinline open operator out override private protected public reified sealed suspend tailrec value vararg") {
		kotlinKeywords[s] = true
	}
}
func isKeyword(s string) bool { return kotlinKeywords[s] }
func identifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func kotlinID(s string) string {
	if isKeyword(s) {
		return "`" + s + "`"
	}
	return s
}
