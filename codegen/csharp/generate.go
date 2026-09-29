// Package csharp generates standalone C# clients from Latch protocol IR.
package csharp

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/vehmloewff/latch/codegen"
	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// Options configures C# source generation. The generated file has no namespace.
type Options struct{ ClientName string }

// Generate emits a single C# file containing DTOs, a client, and the embedded runtime.
func Generate(p *protocol.Protocol, opts Options) (map[string][]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("csharp: nil protocol")
	}
	for _, t := range p.Types {
		if t == nil {
			return nil, fmt.Errorf("csharp: nil named type")
		}
	}
	methods := make([]string, len(p.Methods))
	for i, m := range p.Methods {
		methods[i] = m.Name
	}
	if err := names.ValidateIdentifiers(methods); err != nil {
		return nil, fmt.Errorf("csharp: %w", err)
	}
	ns, err := names.AssignTypeNames(p.Types)
	if err != nil {
		return nil, fmt.Errorf("csharp: %w", err)
	}
	event, ok := p.EventRef()
	if !ok {
		return nil, fmt.Errorf("csharp: protocol has no event type")
	}
	client := opts.ClientName
	if client == "" {
		client = "LatchClient"
	}
	if !identifier(client) || keyword(client) {
		return nil, fmt.Errorf("csharp: invalid client name %q", client)
	}
	used := map[string]bool{"LatchBinary": true, "LatchTransport": true, "LatchValue": true, "LatchError": true, "LatchEnvelope": true, "ConnectionState": true, "Connected" + client: true, client: true}
	for _, s := range strings.Fields("Action ArgumentOutOfRangeException DateTimeOffset Dictionary Exception FormatException IAsyncDisposable List Task ValueTask") {
		used[s] = true
	}
	for _, t := range p.Types {
		if t.Kind == protocol.KindEnum {
			used[ns[t.ID]+"Wire"] = true
		}
	}
	for _, n := range ns {
		if !identifier(n) || keyword(n) || used[n] {
			return nil, fmt.Errorf("csharp: invalid or conflicting type name %q", n)
		}
		used[n] = true
	}
	for _, t := range p.Types {
		if t.Kind != protocol.KindStruct && t.Kind != protocol.KindEnum {
			return nil, fmt.Errorf("csharp: unsupported named type %q: %s", t.ID, t.Kind)
		}
		members := map[string]bool{}
		if t.Kind == protocol.KindEnum {
			if t.EnumBase != protocol.KindString {
				return nil, fmt.Errorf("csharp: enum %q must have string base", t.ID)
			}
			values := map[string]bool{}
			for _, v := range t.EnumValues {
				n := names.PascalCase(v)
				if !identifier(n) || members[n] || values[v] {
					return nil, fmt.Errorf("csharp: invalid or duplicate enum value %q", v)
				}
				members[n] = true
				values[v] = true
			}
		} else {
			numbers := map[uint64]bool{}
			for i, f := range t.Fields {
				number := fieldNumber(f, i)
				if number == 0 || number > math.MaxUint32 || numbers[number] {
					return nil, fmt.Errorf("csharp: invalid or duplicate field number %d in %q", number, t.ID)
				}
				numbers[number] = true
				n := names.PascalCase(f.GoName)
				if !identifier(n) || members[n] || n == "FromLatch" || n == "ToLatch" || n == ns[t.ID] {
					return nil, fmt.Errorf("csharp: invalid or duplicate field %q", f.GoName)
				}
				members[n] = true
				if err := checkRef(f.Type, ns); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := checkRef(event, ns); err != nil {
		return nil, err
	}
	methodMembers := map[string]bool{"DisposeAsync": true, "OnEvent": true, "OnEventError": true, "ConnectAsync": true}
	for _, m := range p.Methods {
		n := names.PascalCase(m.Name) + "Async"
		if methodMembers[n] {
			return nil, fmt.Errorf("csharp: duplicate or reserved method %q", n)
		}
		methodMembers[n] = true
		if err := checkRef(m.RequestType, ns); err != nil {
			return nil, err
		}
		if err := checkRef(m.ResponseType, ns); err != nil {
			return nil, err
		}
	}
	var b strings.Builder
	b.WriteString(codegen.HeaderComment(p.Version))
	b.WriteString("#nullable enable\nusing System;\nusing System.Collections.Generic;\nusing System.Linq;\nusing System.Threading.Tasks;\n\n")
	b.WriteString(runtimeSource)
	b.WriteString(valueSource)
	sorted := append([]*protocol.NamedType(nil), p.Types...)
	sort.Slice(sorted, func(i, j int) bool { return ns[sorted[i].ID] < ns[sorted[j].ID] })
	for _, t := range sorted {
		renderType(&b, t, ns)
	}
	fmt.Fprintf(&b, "\npublic sealed class %s\n{\n    private readonly string _url;\n    private readonly Action<%s> _onEvent;\n    private readonly Action<ConnectionState>? _onConnectionStateChange;\n    private readonly Action<Exception>? _onEventError;\n    public %s(string url, Action<%s> onEvent, Action<ConnectionState>? onConnectionStateChange = null, Action<Exception>? onEventError = null)\n    {\n        _url = url;\n        _onEvent = onEvent ?? throw new ArgumentNullException(nameof(onEvent));\n        _onConnectionStateChange = onConnectionStateChange;\n        _onEventError = onEventError;\n    }\n    public async Task<Connected%s> ConnectAsync() => new Connected%s(await LatchTransport.ConnectAsync(_url, %s, payload => _onEvent(%s), _onEventError, _onConnectionStateChange));\n}\n", client, csType(event, ns), client, csType(event, ns), client, client, quote(p.Version), decode("LatchBinary.Decode(payload)", event, ns))
	fmt.Fprintf(&b, "\npublic sealed class Connected%s : IAsyncDisposable\n{\n    private readonly LatchTransport _transport;\n    internal Connected%s(LatchTransport transport) => _transport = transport;\n", client, client)
	for _, m := range p.Methods {
		fmt.Fprintf(&b, "\n    public async Task<%s> %sAsync(%s request)\n    {\n        var response = await _transport.CallAsync(%s, LatchBinary.Encode(%s));\n        return %s;\n    }\n", csType(m.ResponseType, ns), id(names.PascalCase(m.Name)), csType(m.RequestType, ns), quote(m.Name), encode("request", m.RequestType, ns), decode("LatchBinary.Decode(response)", m.ResponseType, ns))
	}
	b.WriteString("\n    public ValueTask DisposeAsync() => _transport.DisposeAsync();\n}\n")
	return map[string][]byte{"LatchClient.cs": []byte(b.String())}, nil
}

func checkRef(r protocol.TypeRef, ns map[string]string) error {
	switch r.Kind {
	case protocol.KindString, protocol.KindBool, protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt64, protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64, protocol.KindFloat32, protocol.KindFloat64, protocol.KindTime:
		return nil
	case protocol.KindStruct, protocol.KindEnum:
		if ns[r.NamedType] == "" {
			return fmt.Errorf("csharp: unknown named type %q", r.NamedType)
		}
		return nil
	case protocol.KindPointer, protocol.KindSlice, protocol.KindArray:
		if r.Elem == nil {
			return fmt.Errorf("csharp: missing element for %s", r.Kind)
		}
		if r.Kind == protocol.KindArray && r.ArrayLen < 0 {
			return fmt.Errorf("csharp: negative array length")
		}
		return checkRef(*r.Elem, ns)
	case protocol.KindMap:
		if r.MapValue == nil {
			return fmt.Errorf("csharp: missing map value")
		}
		return checkRef(*r.MapValue, ns)
	default:
		return fmt.Errorf("csharp: unsupported kind %q", r.Kind)
	}
}
func fieldNumber(f protocol.Field, i int) uint64 {
	if f.Number != 0 {
		return f.Number
	}
	return uint64(i + 1)
}
func csType(r protocol.TypeRef, ns map[string]string) string {
	switch r.Kind {
	case protocol.KindString:
		return "string"
	case protocol.KindBool:
		return "bool"
	case protocol.KindInt, protocol.KindInt64:
		return "long"
	case protocol.KindInt8:
		return "sbyte"
	case protocol.KindInt16:
		return "short"
	case protocol.KindInt32:
		return "int"
	case protocol.KindUint, protocol.KindUint64:
		return "ulong"
	case protocol.KindUint8:
		return "byte"
	case protocol.KindUint16:
		return "ushort"
	case protocol.KindUint32:
		return "uint"
	case protocol.KindFloat32:
		return "float"
	case protocol.KindFloat64:
		return "double"
	case protocol.KindTime:
		return "DateTimeOffset"
	case protocol.KindPointer:
		return strings.TrimSuffix(csType(*r.Elem, ns), "?") + "?"
	case protocol.KindSlice, protocol.KindArray:
		if r.Kind == protocol.KindSlice && r.Elem.Kind == protocol.KindUint8 {
			return "byte[]"
		}
		return "List<" + csType(*r.Elem, ns) + ">"
	case protocol.KindMap:
		return "Dictionary<string, " + csType(*r.MapValue, ns) + ">"
	default:
		return ns[r.NamedType]
	}
}
func fieldType(f protocol.Field, ns map[string]string) string {
	t := csType(f.Type, ns)
	if (f.Optional || f.Nullable) && !strings.HasSuffix(t, "?") {
		t += "?"
	}
	return t
}
func encode(v string, r protocol.TypeRef, ns map[string]string) string {
	switch r.Kind {
	case protocol.KindPointer:
		return "(" + v + " == null ? null : " + encode(nonNull(v, *r.Elem, ns), *r.Elem, ns) + ")"
	case protocol.KindStruct:
		return v + ".ToLatch()"
	case protocol.KindEnum:
		return ns[r.NamedType] + "Wire.ToWire(" + v + ")"
	case protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt, protocol.KindInt64:
		return "(long)(" + v + ")"
	case protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint, protocol.KindUint64:
		return "(ulong)(" + v + ")"
	case protocol.KindArray:
		return "LatchValue.ArrayInput(" + v + "," + fmt.Sprint(r.ArrayLen) + ").Select(item => (object?)" + encode("item", *r.Elem, ns) + ").ToList()"
	case protocol.KindSlice:
		if r.Elem.Kind == protocol.KindUint8 {
			return v
		}
		return "(" + v + ").Select(item => (object?)" + encode("item", *r.Elem, ns) + ").ToList()"
	case protocol.KindMap:
		return "(" + v + ").ToDictionary(entry => entry.Key, entry => (object?)" + encode("entry.Value", *r.MapValue, ns) + ")"
	default:
		return v
	}
}
func nonNull(v string, r protocol.TypeRef, ns map[string]string) string {
	if isValue(r) || (r.Kind == protocol.KindPointer && isValue(*r.Elem)) {
		return "(" + v + ").Value"
	}
	return v + "!"
}
func isValue(r protocol.TypeRef) bool {
	switch r.Kind {
	case protocol.KindBool, protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt64, protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64, protocol.KindFloat32, protocol.KindFloat64, protocol.KindTime, protocol.KindEnum:
		return true
	}
	return false
}
func decode(v string, r protocol.TypeRef, ns map[string]string) string {
	switch r.Kind {
	case protocol.KindPointer:
		return "(" + v + " == null ? null : " + decode(v, *r.Elem, ns) + ")"
	case protocol.KindString:
		return "LatchValue.AsString(" + v + ")"
	case protocol.KindBool:
		return "LatchValue.AsBool(" + v + ")"
	case protocol.KindInt, protocol.KindInt64:
		return "LatchValue.AsSigned(" + v + ")"
	case protocol.KindInt8:
		return "checked((sbyte)LatchValue.AsSigned(" + v + "))"
	case protocol.KindInt16:
		return "checked((short)LatchValue.AsSigned(" + v + "))"
	case protocol.KindInt32:
		return "checked((int)LatchValue.AsSigned(" + v + "))"
	case protocol.KindUint, protocol.KindUint64:
		return "LatchValue.AsUnsigned(" + v + ")"
	case protocol.KindUint8:
		return "checked((byte)LatchValue.AsUnsigned(" + v + "))"
	case protocol.KindUint16:
		return "checked((ushort)LatchValue.AsUnsigned(" + v + "))"
	case protocol.KindUint32:
		return "checked((uint)LatchValue.AsUnsigned(" + v + "))"
	case protocol.KindFloat32:
		return "LatchValue.AsFloat(" + v + ")"
	case protocol.KindFloat64:
		return "LatchValue.AsDouble(" + v + ")"
	case protocol.KindTime:
		return "LatchValue.AsTime(" + v + ")"
	case protocol.KindStruct:
		return ns[r.NamedType] + ".FromLatch(" + v + ")"
	case protocol.KindEnum:
		return ns[r.NamedType] + "Wire.FromWire(LatchValue.AsString(" + v + "))"
	case protocol.KindSlice:
		if r.Elem.Kind == protocol.KindUint8 {
			return "LatchValue.AsBytes(" + v + ")"
		}
		return "LatchValue.AsList(" + v + ").Select(item => " + decode("item", *r.Elem, ns) + ").ToList()"
	case protocol.KindArray:
		return "LatchValue.Array(LatchValue.AsList(" + v + ")," + fmt.Sprint(r.ArrayLen) + ").Select(item => " + decode("item", *r.Elem, ns) + ").ToList()"
	case protocol.KindMap:
		return "LatchValue.AsMap(" + v + ").ToDictionary(entry => entry.Key, entry => " + decode("entry.Value", *r.MapValue, ns) + ")"
	default:
		panic("unvalidated kind")
	}
}
func renderType(b *strings.Builder, t *protocol.NamedType, ns map[string]string) {
	n := ns[t.ID]
	if t.Kind == protocol.KindEnum {
		fmt.Fprintf(b, "\npublic enum %s\n{\n", n)
		for _, v := range t.EnumValues {
			fmt.Fprintf(b, "    %s,\n", id(names.PascalCase(v)))
		}
		b.WriteString("}\n")
		fmt.Fprintf(b, "internal static class %sWire\n{\n    internal static string ToWire(%s value) => value switch\n    {\n", n, n)
		for _, v := range t.EnumValues {
			fmt.Fprintf(b, "        %s.%s => %s,\n", n, id(names.PascalCase(v)), quote(v))
		}
		fmt.Fprintf(b, "        _ => throw new ArgumentOutOfRangeException(nameof(value))\n    };\n    internal static %s FromWire(string value) => value switch\n    {\n", n)
		for _, v := range t.EnumValues {
			fmt.Fprintf(b, "        %s => %s.%s,\n", quote(v), n, id(names.PascalCase(v)))
		}
		fmt.Fprintf(b, "        _ => throw new FormatException(\"Unknown %s value: \" + value)\n    };\n}\n", n)
		return
	}
	fmt.Fprintf(b, "\npublic sealed class %s\n{\n", n)
	for _, f := range t.Fields {
		fmt.Fprintf(b, "    public %s %s { get; set; }%s\n", fieldType(f, ns), id(names.PascalCase(f.GoName)), propertyDefault(f, ns))
	}
	b.WriteString("\n    internal object ToLatch()\n    {\n        var fields = new Dictionary<ulong, object?>();\n")
	for i, f := range t.Fields {
		v := id(names.PascalCase(f.GoName))
		number := fieldNumber(f, i)
		if f.Optional {
			ref := f.Type
			if ref.Kind == protocol.KindPointer {
				ref = *ref.Elem
			}
			fmt.Fprintf(b, "        if (%s != null) fields[%dUL] = %s;\n", v, number, encode(nonNull(v, ref, ns), ref, ns))
		} else if f.Nullable && f.Type.Kind != protocol.KindPointer {
			fmt.Fprintf(b, "        fields[%dUL] = %s == null ? null : %s;\n", number, v, encode(nonNull(v, f.Type, ns), f.Type, ns))
		} else {
			fmt.Fprintf(b, "        fields[%dUL] = %s;\n", number, encode(v, f.Type, ns))
		}
	}
	fmt.Fprintf(b, "        return fields;\n    }\n\n    internal static %s FromLatch(object? value)\n    {\n        var fields = LatchValue.AsStruct(value);\n        return new %s\n        {\n", n, n)
	for i, f := range t.Fields {
		number := fieldNumber(f, i)
		expr := ""
		if f.Optional || f.Nullable || f.Type.Kind == protocol.KindPointer {
			ref := f.Type
			if ref.Kind == protocol.KindPointer {
				ref = *ref.Elem
			}
			expr = fmt.Sprintf("(fields.TryGetValue(%dUL, out var field%d) && field%d != null ? %s : null)", number, i, i, decode(fmt.Sprintf("field%d", i), ref, ns))
		} else {
			expr = decode(fmt.Sprintf("LatchValue.Required(fields,%dUL)", number), f.Type, ns)
		}
		fmt.Fprintf(b, "            %s = %s,\n", id(names.PascalCase(f.GoName)), expr)
	}
	b.WriteString("        };\n    }\n}\n")
}
func propertyDefault(f protocol.Field, ns map[string]string) string {
	if f.Optional || f.Nullable || f.Type.Kind == protocol.KindPointer || isValue(f.Type) {
		return ""
	}
	return " = null!;"
}
func quote(s string) string { data, _ := json.Marshal(s); return string(data) }
func identifier(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func keyword(s string) bool { return csharpKeywords[s] }
func id(s string) string {
	if keyword(s) {
		return "@" + s
	}
	return s
}

var csharpKeywords = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Fields("abstract as base bool break byte case catch char checked class const continue decimal default delegate do double else enum event explicit extern false finally fixed float for foreach goto if implicit in int interface internal is lock long namespace new null object operator out override params private protected public readonly ref return sbyte sealed short sizeof stackalloc static string struct switch this throw true try typeof uint ulong unchecked unsafe ushort using virtual void volatile while add alias and ascending args async await by descending dynamic equals file from get global group init into join let managed nameof not notnull on or orderby partial record remove required scoped select set unmanaged value var when where with yield") {
		m[s] = true
	}
	return m
}()

// Typed value conversion lives in the generated file, not in runtime.go.
const valueSource = `
internal static class LatchValue
{
    internal static Dictionary<ulong, object?> AsStruct(object? value) => value as Dictionary<ulong, object?> ?? throw Invalid("structure");
    internal static Dictionary<string, object?> AsMap(object? value) => value as Dictionary<string, object?> ?? throw Invalid("map");
    internal static List<object?> AsList(object? value) => value as List<object?> ?? throw Invalid("list");
    internal static byte[] AsBytes(object? value) => value as byte[] ?? throw Invalid("bytes");
    internal static string AsString(object? value) => value as string ?? throw Invalid("string");
    internal static bool AsBool(object? value) => value is bool result ? result : throw Invalid("bool");
    internal static long AsSigned(object? value) => value is long result ? result : throw Invalid("signed integer");
    internal static ulong AsUnsigned(object? value) => value is ulong result ? result : throw Invalid("unsigned integer");
    internal static float AsFloat(object? value) => value is float result ? result : throw Invalid("float32");
    internal static double AsDouble(object? value) => value is double result ? result : throw Invalid("float64");
    internal static DateTimeOffset AsTime(object? value) => value is DateTimeOffset result ? result : throw Invalid("time");
    internal static object? Required(Dictionary<ulong, object?> fields, ulong number) =>
        fields.TryGetValue(number, out var value) && value != null ? value : throw Invalid("required field " + number);
    internal static List<T> Array<T>(List<T> items, int length) => items.Count == length ? items : throw Invalid("array length");
    internal static List<T> ArrayInput<T>(List<T> items, int length) => Array(items, length);
    private static FormatException Invalid(string expected) => new FormatException("Invalid Latch " + expected);
}
`
