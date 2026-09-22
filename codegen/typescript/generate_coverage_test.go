package typescript

import (
	"strings"
	"testing"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

func TestTypeScriptTypeBranchesCoverWidthsFloatsAndWrappers(t *testing.T) {
	typeNames := map[string]string{"example.Widget": "Widget", "example.Status": "Status"}
	stringRef := protocol.TypeRef{Kind: protocol.KindString}
	intRef := protocol.TypeRef{Kind: protocol.KindInt32}
	pointerRef := protocol.TypeRef{Kind: protocol.KindPointer, Elem: &stringRef}
	tests := []struct {
		name string
		ref  protocol.TypeRef
		want string
	}{
		{"string", protocol.TypeRef{Kind: protocol.KindString}, "string"},
		{"bool", protocol.TypeRef{Kind: protocol.KindBool}, "boolean"},
		{"int", protocol.TypeRef{Kind: protocol.KindInt}, "number"},
		{"int8", protocol.TypeRef{Kind: protocol.KindInt8}, "number"},
		{"int16", protocol.TypeRef{Kind: protocol.KindInt16}, "number"},
		{"int32", protocol.TypeRef{Kind: protocol.KindInt32}, "number"},
		{"int64", protocol.TypeRef{Kind: protocol.KindInt64}, "bigint"},
		{"uint", protocol.TypeRef{Kind: protocol.KindUint}, "number"},
		{"uint8", protocol.TypeRef{Kind: protocol.KindUint8}, "number"},
		{"uint16", protocol.TypeRef{Kind: protocol.KindUint16}, "number"},
		{"uint32", protocol.TypeRef{Kind: protocol.KindUint32}, "number"},
		{"uint64", protocol.TypeRef{Kind: protocol.KindUint64}, "bigint"},
		{"float32", protocol.TypeRef{Kind: protocol.KindFloat32}, "number"},
		{"float64", protocol.TypeRef{Kind: protocol.KindFloat64}, "number"},
		{"time", protocol.TypeRef{Kind: protocol.KindTime}, "bigint"},
		{"pointer", pointerRef, "string | null"},
		{"slice", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &intRef}, "number[]"},
		{"byte slice", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}, "Uint8Array"},
		{"array", protocol.TypeRef{Kind: protocol.KindArray, Elem: &intRef, ArrayLen: 2}, "[number, number]"},
		{"empty array", protocol.TypeRef{Kind: protocol.KindArray, Elem: &intRef}, "[]"},
		{"byte array", protocol.TypeRef{Kind: protocol.KindArray, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}, ArrayLen: 4}, "Uint8Array"},
		{"map", protocol.TypeRef{Kind: protocol.KindMap, MapValue: &intRef}, "Record<string, number>"},
		{"struct", protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "example.Widget"}, "Widget"},
		{"enum", protocol.TypeRef{Kind: protocol.KindEnum, NamedType: "example.Status"}, "Status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tsType(tt.ref, typeNames); got != tt.want {
				t.Fatalf("tsType(%#v) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
	if got := arrayElemType(pointerRef, typeNames); got != "(string | null)" {
		t.Fatalf("arrayElemType(pointer) = %q", got)
	}
}

func TestTypeScriptWireTypeBranchesAndIntegerMetadata(t *testing.T) {
	for _, tt := range []struct {
		kind protocol.Kind
		want string
	}{
		{protocol.KindInt, `{ kind: "int" }`},
		{protocol.KindUint, `{ kind: "uint" }`},
		{protocol.KindInt8, `bits: 8, min: -128, max: 127`},
		{protocol.KindUint8, `bits: 8, min: 0, max: 255`},
		{protocol.KindInt16, `bits: 16, min: -32768, max: 32767`},
		{protocol.KindUint16, `bits: 16, min: 0, max: 65535`},
		{protocol.KindInt32, `bits: 32, min: -2147483648, max: 2147483647`},
		{protocol.KindUint32, `bits: 32, min: 0, max: 4294967295`},
		{protocol.KindInt64, `bits: 64, min: -9223372036854775808n, max: 9223372036854775807n`},
		{protocol.KindUint64, `bits: 64, min: 0n, max: 18446744073709551615n`},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := integerWireType(tt.kind); !strings.Contains(got, tt.want) {
				t.Fatalf("integerWireType(%q) = %q, missing %q", tt.kind, got, tt.want)
			}
		})
	}

	elem := protocol.TypeRef{Kind: protocol.KindString}
	for _, tt := range []struct {
		name string
		ref  protocol.TypeRef
		want string
	}{
		{"string", protocol.TypeRef{Kind: protocol.KindString}, `{ kind: "string" }`},
		{"bool", protocol.TypeRef{Kind: protocol.KindBool}, `{ kind: "bool" }`},
		{"float32", protocol.TypeRef{Kind: protocol.KindFloat32}, `{ kind: "float32" }`},
		{"float64", protocol.TypeRef{Kind: protocol.KindFloat64}, `{ kind: "float64" }`},
		{"time", protocol.TypeRef{Kind: protocol.KindTime}, `{ kind: "time", unit: "nanoseconds" }`},
		{"nullable", protocol.TypeRef{Kind: protocol.KindPointer, Elem: &elem}, `{ kind: "nullable", elem: { kind: "string" } }`},
		{"list", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &elem}, `{ kind: "list", elem: { kind: "string" } }`},
		{"bytes", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}, `{ kind: "bytes" }`},
		{"array", protocol.TypeRef{Kind: protocol.KindArray, Elem: &elem, ArrayLen: 3}, `{ kind: "array", length: 3`},
		{"fixed bytes", protocol.TypeRef{Kind: protocol.KindArray, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}, ArrayLen: 3}, `{ kind: "bytes", length: 3 }`},
		{"map", protocol.TypeRef{Kind: protocol.KindMap, MapValue: &elem}, `{ kind: "map", value: { kind: "string" } }`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := wireTypeExpr(tt.ref, map[string]string{}); !strings.Contains(got, tt.want) {
				t.Fatalf("wireTypeExpr(%#v) = %q, missing %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestTypeScriptNamespaceCollisionAndIndexHelpers(t *testing.T) {
	requestID := "example.Request"
	responseID := "example.Response"
	ref := func(id string) protocol.TypeRef { return protocol.TypeRef{Kind: protocol.KindStruct, NamedType: id} }
	p := &protocol.Protocol{Methods: []protocol.Method{
		{Name: "billing.invoice.get", RequestType: ref(requestID), ResponseType: ref(responseID)},
		{Name: "billing.list", RequestType: ref(requestID), ResponseType: ref(responseID)},
	}}
	tree, err := names.BuildMethodTree([]string{"billing.invoice.get", "billing.list"})
	if err != nil {
		t.Fatal(err)
	}
	namespace := renderMethodNamespace(tree, methodIndex(p), map[string]string{requestID: "Request", responseID: "Response"}, "")
	for _, want := range []string{"invoice:", "list:", `"billing.invoice.get"`, "Promise<Response>"} {
		if !strings.Contains(namespace, want) {
			t.Errorf("renderMethodNamespace missing %q: %s", want, namespace)
		}
	}
	if got := generateIndexFile("WebClient"); !strings.Contains(got, "export { WebClient, ConnectedWebClient }") || !strings.Contains(got, "export * from \"./types\"") {
		t.Fatalf("generateIndexFile = %q", got)
	}
	if got := stripImports("import { X } from \"x\";\nconst value = 1;\n"); strings.Contains(got, "import ") || !strings.Contains(got, "const value") {
		t.Fatalf("stripImports = %q", got)
	}

	collision := &protocol.Protocol{Methods: []protocol.Method{{Name: "not valid"}}, EventType: protocol.TypeRef{Kind: protocol.KindString}}
	if _, err := Generate(collision, Options{}); err == nil || !strings.Contains(err.Error(), "method name") {
		t.Fatalf("invalid method error = %v", err)
	}
	if _, err := Generate(&protocol.Protocol{Methods: p.Methods}, Options{}); err == nil || !strings.Contains(err.Error(), "event type") {
		t.Fatalf("missing event error = %v", err)
	}
}

func TestTypeScriptGeneratorReportsTypeNameCollisions(t *testing.T) {
	p := &protocol.Protocol{
		Methods:   []protocol.Method{{Name: "inspect", RequestType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "one.Item"}, ResponseType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "two.Item"}}},
		EventType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "one.Item"},
		Types: []*protocol.NamedType{
			{ID: "one.Item", GoPkgPath: "first/api", GoName: "Item", Kind: protocol.KindStruct},
			{ID: "two.Item", GoPkgPath: "second/service", GoName: "Item", Kind: protocol.KindStruct},
		},
	}
	if _, err := Generate(p, Options{}); err != nil {
		// Distinct package prefixes are expected to disambiguate this case.
		t.Fatalf("Generate rejected disambiguated names: %v", err)
	}
	p.Types[1].GoPkgPath = "first/api"
	if _, err := Generate(p, Options{}); err == nil || !strings.Contains(err.Error(), "type name collision") {
		t.Fatalf("ambiguous type error = %v", err)
	}
}
