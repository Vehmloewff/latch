// Package rust generates a standalone Cargo client for a Latch protocol.
package rust

import (
	_ "embed"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/vehmloewff/latch/codegen"
	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

//go:embed runtime.rs
var runtimeSource []byte

type Options struct {
	Package    string // Cargo package name; defaults to latch_client.
	ClientName string // Public client struct; defaults to LatchClient.
}

func Generate(p *protocol.Protocol, opts Options) (map[string][]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("rust: nil protocol")
	}
	event, ok := p.EventRef()
	if !ok {
		return nil, fmt.Errorf("rust: protocol has no event type")
	}
	if opts.Package == "" {
		opts.Package = "latch_client"
	}
	if !validPackage(opts.Package) {
		return nil, fmt.Errorf("rust: invalid package name %q", opts.Package)
	}
	if opts.ClientName == "" {
		opts.ClientName = "LatchClient"
	}
	if !validIdent(opts.ClientName) || keyword(opts.ClientName) || reservedType(opts.ClientName) {
		return nil, fmt.Errorf("rust: invalid client name %q", opts.ClientName)
	}
	ids := make(map[string]protocol.Kind, len(p.Types))
	for _, t := range p.Types {
		if t == nil {
			return nil, fmt.Errorf("rust: nil named type")
		}
		if t.ID == "" {
			return nil, fmt.Errorf("rust: empty named type ID")
		}
		if _, exists := ids[t.ID]; exists {
			return nil, fmt.Errorf("rust: duplicate named type ID %q", t.ID)
		}
		ids[t.ID] = t.Kind
	}
	typeNames, err := names.AssignTypeNames(p.Types)
	if err != nil {
		return nil, fmt.Errorf("rust: %w", err)
	}
	usedTypes := map[string]bool{opts.ClientName: true}
	for _, name := range typeNames {
		if !validIdent(name) || keyword(name) || reservedType(name) || usedTypes[name] {
			return nil, fmt.Errorf("rust: invalid or conflicting type name %q", name)
		}
		usedTypes[name] = true
	}
	if err := checkRef(event, ids); err != nil {
		return nil, err
	}
	for _, t := range p.Types {
		if t.Kind != protocol.KindStruct && t.Kind != protocol.KindEnum {
			return nil, fmt.Errorf("rust: unsupported named kind %q", t.Kind)
		}
		members := map[string]bool{}
		if t.Kind == protocol.KindEnum {
			if t.EnumBase != protocol.KindString {
				return nil, fmt.Errorf("rust: enum %q must be string based", t.ID)
			}
			if len(t.EnumValues) == 0 {
				return nil, fmt.Errorf("rust: enum %q has no variants", t.ID)
			}
			for _, v := range t.EnumValues {
				n := names.PascalCase(v)
				if !validIdent(n) || keyword(n) || members[n] {
					return nil, fmt.Errorf("rust: invalid or duplicate enum variant %q", v)
				}
				members[n] = true
			}
		} else {
			numbers := map[uint64]bool{}
			for i, f := range t.Fields {
				n := fieldNumber(f, i)
				if n == 0 || n > math.MaxUint32 || numbers[n] {
					return nil, fmt.Errorf("rust: invalid or duplicate field number %d in %q", n, t.ID)
				}
				numbers[n] = true
				name := names.SnakeCase(f.GoName)
				if !validIdent(name) || name == "self" || name == "super" || name == "crate" || name == "Self" || members[name] {
					return nil, fmt.Errorf("rust: invalid or duplicate field %q", f.GoName)
				}
				members[name] = true
				if err := checkRef(f.Type, ids); err != nil {
					return nil, err
				}
			}
		}
	}
	methods := map[string]bool{"connect": true, "close": true, "transport": true}
	for _, m := range p.Methods {
		if !names.IsIdentifier(m.Name) {
			return nil, fmt.Errorf("rust: invalid method %q", m.Name)
		}
		n := names.SnakeCase(m.Name)
		if !validIdent(n) || methods[n] {
			return nil, fmt.Errorf("rust: duplicate or reserved method %q", m.Name)
		}
		methods[n] = true
		if err := checkRef(m.RequestType, ids); err != nil {
			return nil, err
		}
		if err := checkRef(m.ResponseType, ids); err != nil {
			return nil, err
		}
	}

	var b strings.Builder
	b.WriteString(codegen.HeaderComment(p.Version))
	b.WriteString("pub mod runtime;\npub use runtime::{Codec, ConnectionState, Error, Transport, Value};\nuse std::collections::BTreeMap;\n\n")
	b.WriteString("fn invalid_value(message: &str) -> Error { Error { code: \"invalid_value\".into(), message: message.into() } }\n\nimpl<T: Codec> Codec for Box<T> {\n    fn into_value(&self) -> Value { self.as_ref().into_value() }\n    fn from_value(value: Value) -> Result<Self, Error> { T::from_value(value).map(Box::new) }\n}\n\n/// Nanoseconds since the Unix epoch, encoded with the Latch time wire tag.\n#[derive(Debug, Clone, Copy, PartialEq, Eq)]\npub struct LatchTime(pub i64);\nimpl Codec for LatchTime {\n    fn into_value(&self) -> Value { Value::Time(self.0) }\n    fn from_value(value: Value) -> Result<Self, Error> { match value { Value::Time(n) => Ok(Self(n)), _ => Err(invalid_value(\"expected time\")) } }\n}\n")
	types := append([]*protocol.NamedType(nil), p.Types...)
	sort.Slice(types, func(i, j int) bool { return typeNames[types[i].ID] < typeNames[types[j].ID] })
	for _, t := range types {
		renderType(&b, t, typeNames)
	}
	fmt.Fprintf(&b, "\npub struct %s { transport: Transport }\nimpl %s {\n", opts.ClientName, opts.ClientName)
	fmt.Fprintf(&b, "    pub fn connect(url: &str, on_event: impl Fn(%s) + Send + Sync + 'static, on_state: impl Fn(ConnectionState) + Send + Sync + 'static) -> Result<Self, Error> {\n", rustType(event, typeNames))
	fmt.Fprintf(&b, "        let transport = Transport::connect(url, %s, move |payload| {\n            if let Ok(value) = runtime::decode_value(&payload).and_then(|value| %s) { on_event(value); }\n        }, on_state)?;\n        Ok(Self { transport })\n    }\n", strconv.Quote(p.Version), decodeRef(event, "value", typeNames))
	fmt.Fprintf(&b, "    /// Customize fresh WebSocket handshake headers for each dial and reconnect.\n    pub fn connect_with_headers(url: &str, on_event: impl Fn(%s) + Send + Sync + 'static, on_state: impl Fn(ConnectionState) + Send + Sync + 'static, hook: impl Fn(&mut tungstenite::http::HeaderMap) -> Result<(), Error> + Send + 'static) -> Result<Self, Error> {\n", rustType(event, typeNames))
	fmt.Fprintf(&b, "        let transport = Transport::connect_with_headers(url, %s, move |payload| {\n            if let Ok(value) = runtime::decode_value(&payload).and_then(|value| %s) { on_event(value); }\n        }, on_state, hook)?;\n        Ok(Self { transport })\n    }\n", strconv.Quote(p.Version), decodeRef(event, "value", typeNames))
	for _, m := range p.Methods {
		fmt.Fprintf(&b, "\n    pub fn %s(&self, request: %s) -> Result<%s, Error> {\n        let payload = runtime::encode_value(&%s)?;\n        let response = self.transport.call(%s, payload)?;\n        %s\n    }\n", rustIdent(names.SnakeCase(m.Name)), rustType(m.RequestType, typeNames), rustType(m.ResponseType, typeNames), encodeRef(m.RequestType, "request", typeNames), strconv.Quote(m.Name), decodeRef(m.ResponseType, "runtime::decode_value(&response)?", typeNames))
	}
	b.WriteString("\n    pub fn close(&self) { self.transport.close(); }\n}\n")
	cargo := fmt.Sprintf("[package]\nname = %s\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[dependencies]\ntungstenite = { version = \"0.24\", features = [\"rustls-tls-native-roots\"] }\nrustls = { version = \"0.23\", default-features = false, features = [\"ring\", \"std\"] }\nurl = \"2.5\"\n", strconv.Quote(opts.Package))
	return map[string][]byte{"Cargo.toml": []byte(cargo), "src/lib.rs": []byte(b.String()), "src/runtime.rs": append([]byte(nil), runtimeSource...)}, nil
}

func renderType(b *strings.Builder, t *protocol.NamedType, ns map[string]string) {
	name := ns[t.ID]
	if t.Kind == protocol.KindEnum {
		fmt.Fprintf(b, "\n#[derive(Debug, Clone, PartialEq, Eq)]\npub enum %s {\n", name)
		for _, v := range t.EnumValues {
			fmt.Fprintf(b, "    %s,\n", names.PascalCase(v))
		}
		fmt.Fprintf(b, "}\nimpl Codec for %s {\n    fn into_value(&self) -> Value { Value::String(match self {\n", name)
		for _, v := range t.EnumValues {
			fmt.Fprintf(b, "        Self::%s => %s,\n", names.PascalCase(v), strconv.Quote(v))
		}
		b.WriteString("    }.to_owned()) }\n    fn from_value(value: Value) -> Result<Self, Error> {\n        match value {\n            Value::String(s) => match s.as_str() {\n")
		for _, v := range t.EnumValues {
			fmt.Fprintf(b, "                %s => Ok(Self::%s),\n", strconv.Quote(v), names.PascalCase(v))
		}
		b.WriteString("                _ => Err(invalid_value(\"unknown enum value\")),\n            },\n            _ => Err(invalid_value(\"expected string enum\")),\n        }\n    }\n}\n")
		return
	}
	fmt.Fprintf(b, "\n#[derive(Debug, Clone, PartialEq)]\npub struct %s {\n", name)
	for _, f := range t.Fields {
		fmt.Fprintf(b, "    pub %s: %s,\n", rustIdent(names.SnakeCase(f.GoName)), fieldType(f, ns))
	}
	if len(t.Fields) == 0 {
		fmt.Fprintf(b, "}\nimpl Codec for %s {\n    fn into_value(&self) -> Value { Value::Struct(BTreeMap::new()) }\n    fn from_value(value: Value) -> Result<Self, Error> {\n        match value { Value::Struct(_) => Ok(Self {}), _ => Err(invalid_value(\"expected struct\")) }\n    }\n}\nimpl %s {\n    #[allow(dead_code)]\n    fn latch_is_zero(&self) -> bool { true }\n}\n", name, name)
		return
	}
	fmt.Fprintf(b, "}\nimpl Codec for %s {\n    fn into_value(&self) -> Value {\n        let mut fields = BTreeMap::new();\n", name)
	for i, f := range t.Fields {
		n := fieldNumber(f, i)
		field := rustIdent(names.SnakeCase(f.GoName))
		if f.Optional {
			if optionalField(f) {
				// Some(empty collection) is present, unlike a nil Go slice/map.
				fmt.Fprintf(b, "        if let Some(value) = &self.%s {\n", field)
				zero := ""
				if f.Type.Kind != protocol.KindPointer {
					zero = omitZeroExpr(baseRef(f.Type), "value", ns)
				}
				if zero != "" && zero != "false" {
					fmt.Fprintf(b, "            if !(%s) { fields.insert(%d, %s); }\n", zero, n, encodeRef(baseRef(f.Type), "value", ns))
				} else {
					fmt.Fprintf(b, "            fields.insert(%d, %s);\n", n, encodeRef(baseRef(f.Type), "value", ns))
				}
				b.WriteString("        }\n")
			} else {
				fmt.Fprintf(b, "        if !(%s) { fields.insert(%d, %s); }\n", omitZeroExpr(f.Type, "self."+field, ns), n, encodeRef(f.Type, "self."+field, ns))
			}
		} else {
			fmt.Fprintf(b, "        fields.insert(%d, %s);\n", n, encodeRef(f.Type, "self."+field, ns))
		}
	}
	b.WriteString("        Value::Struct(fields)\n    }\n    fn from_value(value: Value) -> Result<Self, Error> {\n        let Value::Struct(mut fields) = value else { return Err(invalid_value(\"expected struct\")); };\n        Ok(Self {\n")
	for i, f := range t.Fields {
		field := rustIdent(names.SnakeCase(f.GoName))
		n := fieldNumber(f, i)
		if f.Optional {
			if optionalField(f) {
				if f.Type.Kind == protocol.KindSlice || f.Type.Kind == protocol.KindMap {
					fmt.Fprintf(b, "            %s: match fields.remove(&%d) { None | Some(Value::Null) => None, Some(value) => Some(%s?), },\n", field, n, decodeRef(f.Type, "value", ns))
				} else if f.Type.Kind == protocol.KindPointer {
					inner := decodeRef(baseRef(f.Type), "value", ns)
					if boxedPointerTarget(*f.Type.Elem) {
						inner = "Box::new(" + inner + "?)"
					} else {
						inner += "?"
					}
					fmt.Fprintf(b, "            %s: match fields.remove(&%d) { None | Some(Value::Null) => None, Some(value) => Some(%s), },\n", field, n, inner)
				} else {
					fmt.Fprintf(b, "            %s: match fields.remove(&%d) { None => None, Some(value) => Some(%s?), },\n", field, n, decodeRef(f.Type, "value", ns))
				}
			} else {
				fmt.Fprintf(b, "            %s: match fields.remove(&%d) { None => %s, Some(value) => %s?, },\n", field, n, zeroDefault(f.Type), decodeRef(f.Type, "value", ns))
			}
		} else if optionalField(f) {
			fmt.Fprintf(b, "            %s: match fields.remove(&%d) { None => return Err(invalid_value(%s)), Some(value) => %s?, },\n", field, n, strconv.Quote("missing required field "+f.GoName), decodeRef(f.Type, "value", ns))
		} else {
			fmt.Fprintf(b, "            %s: %s?,\n", field, decodeRef(f.Type, fmt.Sprintf("fields.remove(&%d).ok_or_else(|| invalid_value(%s))?", n, strconv.Quote("missing required field "+f.GoName)), ns))
		}
	}
	b.WriteString("        })\n    }\n}\n")
	fmt.Fprintf(b, "impl %s {\n    #[allow(dead_code)]\n    fn latch_is_zero(&self) -> bool {\n", name)
	if len(t.Fields) == 0 {
		b.WriteString("        true\n")
	} else {
		b.WriteString("        true")
		for _, f := range t.Fields {
			field := "self." + rustIdent(names.SnakeCase(f.GoName))
			if optionalField(f) {
				zero := ""
				if f.Type.Kind != protocol.KindPointer {
					zero = omitZeroExpr(baseRef(f.Type), "value", ns)
				}
				if f.Optional && zero != "" && zero != "false" {
					fmt.Fprintf(b, "\n            && %s.as_ref().map_or(true, |value| %s)", field, zero)
				} else {
					fmt.Fprintf(b, "\n            && %s.is_none()", field)
				}
			} else {
				zero := omitZeroExpr(f.Type, field, ns)
				if zero == "" {
					zero = "(" + field + ").is_empty()"
				}
				fmt.Fprintf(b, "\n            && (%s)", zero)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("    }\n}\n")
}

func fieldNumber(f protocol.Field, i int) uint64 {
	if f.Number != 0 {
		return f.Number
	}
	return uint64(i + 1)
}
func optionalField(f protocol.Field) bool {
	if f.Nullable || f.Type.Kind == protocol.KindPointer {
		return true
	}
	if !f.Optional {
		return false
	}
	// These types need a presence bit: zero time has no representable Unix-nanos
	// value, and empty but present collections are not Go's nil zero value.
	switch f.Type.Kind {
	case protocol.KindTime, protocol.KindSlice, protocol.KindArray, protocol.KindMap, protocol.KindStruct, protocol.KindEnum:
		return true
	}
	return false
}

func zeroDefault(r protocol.TypeRef) string {
	switch r.Kind {
	case protocol.KindString:
		return "String::new()"
	case protocol.KindBool:
		return "false"
	default:
		return "0 as " + rustType(r, nil)
	}
}

// Return empty for kinds whose zero value is represented exclusively by None.
func omitZeroExpr(r protocol.TypeRef, expr string, ns map[string]string) string {
	switch r.Kind {
	case protocol.KindString:
		return "(" + expr + ").is_empty()"
	case protocol.KindBool:
		return "(" + expr + ").eq(&false)"
	case protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt64,
		protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64,
		protocol.KindFloat32, protocol.KindFloat64:
		return "(" + expr + ").eq(&(0 as " + rustType(r, ns) + "))"
	case protocol.KindPointer:
		return "(" + expr + ").is_none()"
	case protocol.KindTime:
		// No Unix-nanos value represents Go's zero time.Time.
		return "false"
	case protocol.KindArray:
		zero := omitZeroExpr(*r.Elem, "item", ns)
		if zero == "" {
			zero = "item.is_empty()"
		}
		return "(" + expr + ").iter().all(|item| " + zero + ")"
	case protocol.KindStruct:
		return "(" + expr + ").latch_is_zero()"
	case protocol.KindEnum:
		return "(" + expr + ").into_value() == Value::String(String::new())"
	}
	return ""
}
func fieldType(f protocol.Field, ns map[string]string) string {
	if optionalField(f) && f.Type.Kind != protocol.KindPointer {
		return "Option<" + rustType(f.Type, ns) + ">"
	}
	return rustType(f.Type, ns)
}

// Arrays are stored inline in Rust; a pointer to an array of structs must
// also box the target to break recursive struct layouts.
func boxedPointerTarget(r protocol.TypeRef) bool {
	if r.Kind == protocol.KindStruct {
		return true
	}
	if r.Kind == protocol.KindArray {
		return boxedPointerTarget(*r.Elem)
	}
	return false
}

func baseRef(r protocol.TypeRef) protocol.TypeRef {
	if r.Kind == protocol.KindPointer {
		return *r.Elem
	}
	return r
}

// Byte slices use the bytes wire tag, including inside nested containers.
func hasBytes(r protocol.TypeRef) bool {
	switch r.Kind {
	case protocol.KindSlice:
		return r.Elem.Kind == protocol.KindUint8 || hasBytes(*r.Elem)
	case protocol.KindArray, protocol.KindPointer:
		return hasBytes(*r.Elem)
	case protocol.KindMap:
		return hasBytes(*r.MapValue)
	}
	return false
}
func encodeRef(r protocol.TypeRef, expr string, ns map[string]string) string {
	if !hasBytes(r) {
		return "(" + expr + ").into_value()"
	}
	switch r.Kind {
	case protocol.KindSlice:
		if r.Elem.Kind == protocol.KindUint8 {
			return "Value::Bytes((" + expr + ").clone())"
		}
		fallthrough
	case protocol.KindArray:
		return "Value::List((" + expr + ").iter().map(|item| " + encodeRef(*r.Elem, "item", ns) + ").collect())"
	case protocol.KindMap:
		return "Value::Map((" + expr + ").iter().map(|(key, item)| (key.clone(), " + encodeRef(*r.MapValue, "item", ns) + ")).collect())"
	case protocol.KindPointer:
		return "(" + expr + ").as_ref().map_or(Value::Null, |item| " + encodeRef(*r.Elem, "item", ns) + ")"
	}
	return "(" + expr + ").into_value()"
}
func decodeRef(r protocol.TypeRef, expr string, ns map[string]string) string {
	if r.Kind == protocol.KindPointer && boxedPointerTarget(*r.Elem) {
		return "match " + expr + " { Value::Null => Ok(None), value => " + decodeRef(*r.Elem, "value", ns) + ".map(|value| Some(Box::new(value))) }"
	}
	if r.Kind == protocol.KindSlice || r.Kind == protocol.KindMap {
		// A nil Go slice/map is sent as Null; an absent required field is still an error.
		return "match " + expr + " { Value::Null => Ok(Default::default()), value => " + decodeNonNullRef(r, "value", ns) + " }"
	}
	return decodeNonNullRef(r, expr, ns)
}
func decodeNonNullRef(r protocol.TypeRef, expr string, ns map[string]string) string {
	if !hasBytes(r) {
		return "<" + rustType(r, ns) + " as Codec>::from_value(" + expr + ")"
	}
	switch r.Kind {
	case protocol.KindSlice:
		if r.Elem.Kind == protocol.KindUint8 {
			return "match " + expr + " { Value::Bytes(bytes) => Ok(bytes), _ => Err(invalid_value(\"expected bytes\")) }"
		}
		fallthrough
	case protocol.KindArray:
		inner := "items.into_iter().map(|item| " + decodeRef(*r.Elem, "item", ns) + ").collect::<Result<Vec<_>, Error>>()?"
		if r.Kind == protocol.KindArray {
			inner = inner + ".try_into().map_err(|_| invalid_value(\"invalid array length\"))"
		} else {
			inner = "Ok(" + inner + ")"
		}
		return "match " + expr + " { Value::List(items) => " + inner + ", _ => Err(invalid_value(\"expected list\")) }"
	case protocol.KindMap:
		return "match " + expr + " { Value::Map(items) => items.into_iter().map(|(key, item)| Ok((key, " + decodeRef(*r.MapValue, "item", ns) + "?))).collect::<Result<BTreeMap<_, _>, Error>>(), _ => Err(invalid_value(\"expected map\")) }"
	case protocol.KindPointer:
		return "match " + expr + " { Value::Null => Ok(None), item => " + decodeRef(*r.Elem, "item", ns) + ".map(Some) }"
	}
	return "<" + rustType(r, ns) + " as Codec>::from_value(" + expr + ")"
}
func rustType(r protocol.TypeRef, ns map[string]string) string {
	switch r.Kind {
	case protocol.KindString:
		return "String"
	case protocol.KindBool:
		return "bool"
	case protocol.KindInt, protocol.KindInt64:
		return "i64"
	case protocol.KindInt8:
		return "i8"
	case protocol.KindInt16:
		return "i16"
	case protocol.KindInt32:
		return "i32"
	case protocol.KindUint, protocol.KindUint64:
		return "u64"
	case protocol.KindUint8:
		return "u8"
	case protocol.KindUint16:
		return "u16"
	case protocol.KindUint32:
		return "u32"
	case protocol.KindFloat32:
		return "f32"
	case protocol.KindFloat64:
		return "f64"
	case protocol.KindTime:
		return "LatchTime"
	case protocol.KindStruct, protocol.KindEnum:
		return ns[r.NamedType]
	case protocol.KindPointer:
		if boxedPointerTarget(*r.Elem) {
			return "Option<Box<" + rustType(*r.Elem, ns) + ">>"
		}
		return "Option<" + rustType(*r.Elem, ns) + ">"
	case protocol.KindSlice:
		return "Vec<" + rustType(*r.Elem, ns) + ">"
	case protocol.KindArray:
		return fmt.Sprintf("[%s; %d]", rustType(*r.Elem, ns), r.ArrayLen)
	case protocol.KindMap:
		return "BTreeMap<String, " + rustType(*r.MapValue, ns) + ">"
	}
	panic("validated type reference required")
}
func checkRef(r protocol.TypeRef, ids map[string]protocol.Kind) error {
	switch r.Kind {
	case protocol.KindString, protocol.KindBool, protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt64, protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64, protocol.KindFloat32, protocol.KindFloat64, protocol.KindTime:
		return nil
	case protocol.KindStruct, protocol.KindEnum:
		if kind, ok := ids[r.NamedType]; !ok || kind != r.Kind {
			return fmt.Errorf("rust: unknown or mismatched named type %q (%s)", r.NamedType, r.Kind)
		}
		return nil
	case protocol.KindPointer, protocol.KindSlice, protocol.KindArray:
		if r.Elem == nil {
			return fmt.Errorf("rust: missing element for %s", r.Kind)
		}
		if r.Kind == protocol.KindArray && r.ArrayLen < 0 {
			return fmt.Errorf("rust: negative array length")
		}
		return checkRef(*r.Elem, ids)
	case protocol.KindMap:
		if r.MapValue == nil {
			return fmt.Errorf("rust: missing map value")
		}
		return checkRef(*r.MapValue, ids)
	default:
		return fmt.Errorf("rust: unsupported kind %q", r.Kind)
	}
}
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '_' && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return s != "_"
}
func validPackage(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
func reservedType(s string) bool {
	switch s {
	case "LatchTime", "ConnectionState", "Error", "Transport", "Codec", "Value", "BTreeMap", "String", "Option", "Vec", "Result", "Some", "None", "Ok", "Err", "runtime":
		return true
	}
	return false
}
func keyword(s string) bool {
	switch s {
	case "as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern", "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return", "self", "Self", "static", "struct", "super", "trait", "true", "type", "union", "unsafe", "use", "where", "while", "abstract", "become", "box", "do", "final", "gen", "macro", "override", "priv", "try", "typeof", "unsized", "virtual", "yield":
		return true
	}
	return false
}
func rustIdent(s string) string {
	if keyword(s) {
		return "r#" + s
	}
	return s
}
