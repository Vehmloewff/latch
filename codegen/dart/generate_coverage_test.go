package dart

import (
	"strings"
	"testing"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

func TestDartTypeBranchesCoverWidthsFloatsAndWrappers(t *testing.T) {
	typeNames := map[string]string{"example.Widget": "Widget", "example.Status": "Status"}
	stringRef := protocol.TypeRef{Kind: protocol.KindString}
	intRef := protocol.TypeRef{Kind: protocol.KindInt32}
	tests := []struct {
		name string
		ref  protocol.TypeRef
		want string
	}{
		{"string", protocol.TypeRef{Kind: protocol.KindString}, "String"},
		{"time", protocol.TypeRef{Kind: protocol.KindTime}, "DateTime"},
		{"bool", protocol.TypeRef{Kind: protocol.KindBool}, "bool"},
		{"int", protocol.TypeRef{Kind: protocol.KindInt}, "int"},
		{"int8", protocol.TypeRef{Kind: protocol.KindInt8}, "int"},
		{"int16", protocol.TypeRef{Kind: protocol.KindInt16}, "int"},
		{"int32", protocol.TypeRef{Kind: protocol.KindInt32}, "int"},
		{"int64", protocol.TypeRef{Kind: protocol.KindInt64}, "int"},
		{"uint", protocol.TypeRef{Kind: protocol.KindUint}, "int"},
		{"uint8", protocol.TypeRef{Kind: protocol.KindUint8}, "int"},
		{"uint16", protocol.TypeRef{Kind: protocol.KindUint16}, "int"},
		{"uint32", protocol.TypeRef{Kind: protocol.KindUint32}, "int"},
		{"uint64", protocol.TypeRef{Kind: protocol.KindUint64}, "BigInt"},
		{"float32", protocol.TypeRef{Kind: protocol.KindFloat32}, "double"},
		{"float64", protocol.TypeRef{Kind: protocol.KindFloat64}, "double"},
		{"pointer", protocol.TypeRef{Kind: protocol.KindPointer, Elem: &stringRef}, "String?"},
		{"nested pointer", protocol.TypeRef{Kind: protocol.KindPointer, Elem: &protocol.TypeRef{Kind: protocol.KindPointer, Elem: &stringRef}}, "String?"},
		{"slice", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &intRef}, "List<int>"},
		{"byte slice", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}, "Uint8List"},
		{"array", protocol.TypeRef{Kind: protocol.KindArray, Elem: &intRef, ArrayLen: 2}, "List<int>"},
		{"byte array", protocol.TypeRef{Kind: protocol.KindArray, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}, ArrayLen: 4}, "Uint8List"},
		{"map", protocol.TypeRef{Kind: protocol.KindMap, MapValue: &intRef}, "Map<String, int>"},
		{"struct", protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "example.Widget"}, "Widget"},
		{"enum", protocol.TypeRef{Kind: protocol.KindEnum, NamedType: "example.Status"}, "Status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dartType(tt.ref, typeNames); got != tt.want {
				t.Fatalf("dartType(%#v) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestDartEncodeDecodeAndFieldBranches(t *testing.T) {
	typeNames := map[string]string{"example.Widget": "Widget", "example.Status": "Status"}
	widget := protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "example.Widget"}
	status := protocol.TypeRef{Kind: protocol.KindEnum, NamedType: "example.Status"}
	stringRef := protocol.TypeRef{Kind: protocol.KindString}
	refs := []struct {
		name string
		ref  protocol.TypeRef
		want string
	}{
		{"string decode", stringRef, "as String"},
		{"time decode", protocol.TypeRef{Kind: protocol.KindTime}, "as DateTime"},
		{"bool decode", protocol.TypeRef{Kind: protocol.KindBool}, "as bool"},
		{"signed decode", protocol.TypeRef{Kind: protocol.KindInt64}, "as int"},
		{"unsigned decode", protocol.TypeRef{Kind: protocol.KindUint32}, "UIntValue"},
		{"uint64 decode", protocol.TypeRef{Kind: protocol.KindUint64}, "UIntValue"},
		{"float32 decode", protocol.TypeRef{Kind: protocol.KindFloat32}, "Float32Value"},
		{"float64 decode", protocol.TypeRef{Kind: protocol.KindFloat64}, "as double"},
		{"pointer decode", protocol.TypeRef{Kind: protocol.KindPointer, Elem: &stringRef}, "== null ? null"},
		{"list decode", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &stringRef}, ".map((e)"},
		{"bytes decode", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}, "Uint8List"},
		{"map decode", protocol.TypeRef{Kind: protocol.KindMap, MapValue: &stringRef}, "MapEntry"},
		{"struct decode", widget, "Widget.fromBinary"},
		{"enum decode", status, "Status.fromBinary"},
	}
	for _, tt := range refs {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeExpr("value", tt.ref, typeNames); !strings.Contains(got, tt.want) {
				t.Fatalf("decodeExpr(%#v) = %q, missing %q", tt.ref, got, tt.want)
			}
		})
	}
	for _, tt := range []struct {
		name string
		ref  protocol.TypeRef
		want string
	}{
		{"primitive", stringRef, "value"},
		{"unsigned", protocol.TypeRef{Kind: protocol.KindUint64}, "UIntValue"},
		{"float32", protocol.TypeRef{Kind: protocol.KindFloat32}, "Float32Value"},
		{"pointer", protocol.TypeRef{Kind: protocol.KindPointer, Elem: &stringRef}, "value == null"},
		{"list", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &stringRef}, ".map((e)"},
		{"bytes", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}, "Uint8List.fromList"},
		{"map", protocol.TypeRef{Kind: protocol.KindMap, MapValue: &stringRef}, "MapEntry"},
		{"named", widget, "value.toBinary()"},
	} {
		t.Run("encode "+tt.name, func(t *testing.T) {
			if got := encodeExpr("value", tt.ref, typeNames); !strings.Contains(got, tt.want) {
				t.Fatalf("encodeExpr(%#v) = %q, missing %q", tt.ref, got, tt.want)
			}
		})
	}

	optionalValue := protocol.Field{GoName: "OptionalValue", Type: stringRef, Optional: true}
	optionalPointer := protocol.Field{GoName: "OptionalPointer", Type: protocol.TypeRef{Kind: protocol.KindPointer, Elem: &stringRef}, Optional: true}
	named := &protocol.NamedType{ID: "example.Record", GoName: "Record", Kind: protocol.KindStruct, Fields: []protocol.Field{optionalValue, optionalPointer}}
	if got := fieldEncodeStatement(named, optionalValue, typeNames); !strings.Contains(got, "if (optionalValue != null)") {
		t.Fatalf("optional field encode = %q", got)
	}
	if got := fieldDecodeExpr(optionalValue, 1, typeNames); !strings.Contains(got, "containsKey") {
		t.Fatalf("optional field decode = %q", got)
	}
	if got := dartFieldName(protocol.Field{GoName: "SomeValue"}); got != "someValue" {
		t.Fatalf("dartFieldName = %q", got)
	}
}

func TestDartNamespaceAndPackageFileHelpers(t *testing.T) {
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
	typeNames := map[string]string{requestID: "Request", responseID: "Response"}
	var classes []string
	collectNamespaceClasses("Web", "ConnectedWeb", tree, nil, methodIndex(p), typeNames, &classes)
	if len(classes) != 2 || !strings.Contains(strings.Join(classes, "\n"), "_WebBillingInvoiceNamespace") {
		t.Fatalf("collectNamespaceClasses = %#v", classes)
	}
	if got := namespaceClassName("Web", []string{"billing", "invoice"}); got != "_WebBillingInvoiceNamespace" {
		t.Fatalf("namespaceClassName = %q", got)
	}
	root := renderRootMembers("Web", tree, methodIndex(p), typeNames)
	if !strings.Contains(root, "late final _WebBillingNamespace billing") {
		t.Fatalf("renderRootMembers = %q", root)
	}
	if _, err := eventPropertyNames(&protocol.Protocol{Events: []protocol.Event{{Name: "billing.invoice.updated"}, {Name: "billingInvoiceUpdated"}}}); err == nil {
		t.Fatal("eventPropertyNames accepted a camelCase collision")
	}

	pubspec := generatePubspec(Options{Package: "api_client", WebSocketChannelVersion: "^9.1.0"})
	for _, want := range []string{"name: api_client", "web_socket_channel: ^9.1.0", "publish_to: \"none\""} {
		if !strings.Contains(pubspec, want) {
			t.Errorf("pubspec missing %q: %s", want, pubspec)
		}
	}
	barrel := generateBarrelFile("WebClient")
	for _, want := range []string{"show WebClient, ConnectedWebClient", "src/models.dart", "LatchError"} {
		if !strings.Contains(barrel, want) {
			t.Errorf("barrel missing %q: %s", want, barrel)
		}
	}
}

func TestDartLegacyEventRenderingHelpers(t *testing.T) {
	stringRef := protocol.TypeRef{Kind: protocol.KindString}
	intRef := protocol.TypeRef{Kind: protocol.KindInt}
	p := &protocol.Protocol{
		EventType: stringRef,
		Events: []protocol.Event{
			{Name: "user.created", PayloadType: stringRef},
			{Name: "count.updated", PayloadType: intRef},
		},
	}

	if got, err := eventType(p); err != nil || got != stringRef {
		t.Fatalf("eventType = %#v, %v; want %#v, nil", got, err, stringRef)
	}
	if _, err := eventType(&protocol.Protocol{}); err == nil {
		t.Fatal("eventType accepted a protocol without an event type")
	}

	typeNames := map[string]string{}
	fields := renderEventFields(p, typeNames)
	for _, want := range []string{
		"StreamController<String> _userCreatedController",
		"StreamController<int> _countUpdatedController",
	} {
		if !strings.Contains(fields, want) {
			t.Errorf("renderEventFields missing %q: %s", want, fields)
		}
	}

	dispatch := renderDispatchEvent(p, typeNames)
	for _, want := range []string{
		`case "user.created":`,
		"_userCreatedController.add(payload as String);",
		`case "count.updated":`,
		"_countUpdatedController.add(payload as int);",
		"default:",
	} {
		if !strings.Contains(dispatch, want) {
			t.Errorf("renderDispatchEvent missing %q: %s", want, dispatch)
		}
	}

	accessor, className := renderEventsAccessorClass(
		"Demo",
		"ConnectedDemo",
		p,
		map[string]string{"user.created": "userCreated", "count.updated": "countUpdated"},
		typeNames,
	)
	if className != "DemoEvents" {
		t.Fatalf("renderEventsAccessorClass name = %q, want DemoEvents", className)
	}
	for _, want := range []string{
		"class DemoEvents",
		"Stream<String> get userCreated",
		"_client._userCreatedController.stream",
		"Stream<int> get countUpdated",
		"_client._countUpdatedController.stream",
	} {
		if !strings.Contains(accessor, want) {
			t.Errorf("renderEventsAccessorClass missing %q: %s", want, accessor)
		}
	}
}

func TestDartGeneratorReportsInvalidIRAndTypeCollisions(t *testing.T) {
	if _, err := Generate(&protocol.Protocol{Methods: []protocol.Method{{Name: "not valid"}}, EventType: protocol.TypeRef{Kind: protocol.KindString}}, Options{}); err == nil || !strings.Contains(err.Error(), "method name") {
		t.Fatalf("invalid method error = %v", err)
	}
	if _, err := Generate(&protocol.Protocol{Methods: []protocol.Method{{Name: "ok"}}}, Options{}); err == nil || !strings.Contains(err.Error(), "event type") {
		t.Fatalf("missing event error = %v", err)
	}
	p := &protocol.Protocol{
		Methods:   []protocol.Method{{Name: "inspect", RequestType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "one.Item"}, ResponseType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "two.Item"}}},
		EventType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "one.Item"},
		Types: []*protocol.NamedType{
			{ID: "one.Item", GoPkgPath: "first/api", GoName: "Item", Kind: protocol.KindStruct},
			{ID: "two.Item", GoPkgPath: "second/service", GoName: "Item", Kind: protocol.KindStruct},
		},
	}
	if _, err := Generate(p, Options{}); err != nil {
		t.Fatalf("Generate rejected disambiguated names: %v", err)
	}
	p.Types[1].GoPkgPath = "first/api"
	if _, err := Generate(p, Options{}); err == nil || !strings.Contains(err.Error(), "type name collision") {
		t.Fatalf("ambiguous type error = %v", err)
	}
}
