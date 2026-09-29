package golang

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

func TestGoTypeCoversPrimitiveAndContainerKinds(t *testing.T) {
	named := map[string]string{"example.Widget": "Widget", "example.State": "State"}
	ptrElem := protocol.TypeRef{Kind: protocol.KindString}
	arrayElem := protocol.TypeRef{Kind: protocol.KindInt32}
	mapValue := protocol.TypeRef{Kind: protocol.KindFloat64}
	tests := []struct {
		name string
		ref  protocol.TypeRef
		want string
	}{
		{"string", protocol.TypeRef{Kind: protocol.KindString}, "string"},
		{"bool", protocol.TypeRef{Kind: protocol.KindBool}, "bool"},
		{"int", protocol.TypeRef{Kind: protocol.KindInt}, "int"},
		{"int8", protocol.TypeRef{Kind: protocol.KindInt8}, "int8"},
		{"int16", protocol.TypeRef{Kind: protocol.KindInt16}, "int16"},
		{"int32", protocol.TypeRef{Kind: protocol.KindInt32}, "int32"},
		{"uint", protocol.TypeRef{Kind: protocol.KindUint}, "uint"},
		{"uint8", protocol.TypeRef{Kind: protocol.KindUint8}, "uint8"},
		{"uint16", protocol.TypeRef{Kind: protocol.KindUint16}, "uint16"},
		{"uint32", protocol.TypeRef{Kind: protocol.KindUint32}, "uint32"},
		{"float32", protocol.TypeRef{Kind: protocol.KindFloat32}, "float32"},
		{"float64", protocol.TypeRef{Kind: protocol.KindFloat64}, "float64"},
		{"time", protocol.TypeRef{Kind: protocol.KindTime}, "time.Time"},
		{"pointer", protocol.TypeRef{Kind: protocol.KindPointer, Elem: &ptrElem}, "*string"},
		{"slice", protocol.TypeRef{Kind: protocol.KindSlice, Elem: &arrayElem}, "[]int32"},
		{"array", protocol.TypeRef{Kind: protocol.KindArray, Elem: &arrayElem, ArrayLen: 3}, "[]int32"},
		{"map", protocol.TypeRef{Kind: protocol.KindMap, MapValue: &mapValue}, "map[string]float64"},
		{"struct", protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "example.Widget"}, "Widget"},
		{"enum", protocol.TypeRef{Kind: protocol.KindEnum, NamedType: "example.State"}, "State"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := goType(tt.ref, named); got != tt.want {
				t.Fatalf("goType(%#v) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestUsesTimeAndGoBodyBranches(t *testing.T) {
	timeRef := protocol.TypeRef{Kind: protocol.KindTime}
	for _, ref := range []protocol.TypeRef{
		timeRef,
		{Kind: protocol.KindPointer, Elem: &timeRef},
		{Kind: protocol.KindSlice, Elem: &timeRef},
		{Kind: protocol.KindArray, Elem: &timeRef, ArrayLen: 2},
		{Kind: protocol.KindMap, MapValue: &timeRef},
	} {
		if !usesTime(ref) {
			t.Errorf("usesTime(%#v) = false, want true", ref)
		}
	}
	if usesTime(protocol.TypeRef{Kind: protocol.KindFloat64}) {
		t.Fatal("usesTime(float64) = true, want false")
	}

	if got := goBody("package x\n\nimport \"time\"\n\nvar _ time.Time\n"); got != "var _ time.Time" {
		t.Fatalf("goBody(single import) = %q", got)
	}
	if got := goBody("package x\n\nimport (\n\t\"time\"\n)\n\nvar _ time.Time\n"); got != "var _ time.Time" {
		t.Fatalf("goBody(grouped import) = %q", got)
	}
	if got := goBody("var x = 1"); got != "var x = 1" {
		t.Fatalf("goBody(without package) = %q", got)
	}
}

func TestGoGeneratorNamespaceAndTypeNameBranches(t *testing.T) {
	requestID := "example.Request"
	responseID := "example.Response"
	ref := func(id string) protocol.TypeRef { return protocol.TypeRef{Kind: protocol.KindStruct, NamedType: id} }
	p := &protocol.Protocol{
		Version: "namespace-v1",
		Methods: []protocol.Method{
			{Name: "billing.invoice.get", RequestType: ref(requestID), ResponseType: ref(responseID)},
			{Name: "billing.list", RequestType: ref(requestID), ResponseType: ref(responseID)},
		},
		EventType: ref(responseID),
		Types: []*protocol.NamedType{
			{ID: requestID, GoPkgPath: "example/api", GoName: "Request", Kind: protocol.KindStruct},
			{ID: responseID, GoPkgPath: "example/api", GoName: "Response", Kind: protocol.KindStruct},
		},
	}
	files, err := Generate(p, Options{Package: "generated", ClientName: "BillingClient"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	source := string(files["client.go"])
	if !strings.Contains(source, "func (c *ConnectedBillingClient) Get(") || !strings.Contains(source, "billing.invoice.get") {
		t.Fatalf("generated client did not render dotted method names: %s", source)
	}

	tree, err := names.BuildMethodTree([]string{"billing.invoice.get", "billing.list"})
	if err != nil {
		t.Fatal(err)
	}
	var rendered []string
	collectNamespaceTypes("BillingClient", tree, nil, methodIndex(p), map[string]string{requestID: "Request", responseID: "Response"}, &rendered)
	if len(rendered) != 2 {
		t.Fatalf("collectNamespaceTypes rendered %d namespaces, want 2", len(rendered))
	}
	if got := namespaceTypeName("BillingClient", []string{"billing", "invoice"}); got != "BillingClientBillingInvoiceNamespace" {
		t.Fatalf("namespaceTypeName = %q", got)
	}
	if init := buildNamespaceInit("BillingClient", "result", tree, nil); !strings.Contains(init, "result.Billing =") || !strings.Contains(init, "result.Billing.Invoice =") {
		t.Fatalf("buildNamespaceInit = %q", init)
	}

	if _, err := eventGetterNames(&protocol.Protocol{Events: []protocol.Event{
		{Name: "billing.invoice.updated"}, {Name: "billingInvoiceUpdated"},
	}}); err == nil {
		t.Fatal("eventGetterNames accepted a PascalCase collision")
	}
}

func TestGoGeneratorReportsInvalidIRAndNameCollisions(t *testing.T) {
	base := &protocol.Protocol{Version: "v1", Methods: []protocol.Method{{Name: "ok", RequestType: protocol.TypeRef{Kind: protocol.KindString}, ResponseType: protocol.TypeRef{Kind: protocol.KindString}}}, EventType: protocol.TypeRef{Kind: protocol.KindString}}
	if _, err := Generate(&protocol.Protocol{Methods: base.Methods}, Options{}); err == nil || !strings.Contains(err.Error(), "event type") {
		t.Fatalf("missing event error = %v", err)
	}
	badMethod := *base
	badMethod.Methods = []protocol.Method{{Name: "not valid", RequestType: base.Methods[0].RequestType, ResponseType: base.Methods[0].ResponseType}}
	if _, err := Generate(&badMethod, Options{}); err == nil || !strings.Contains(err.Error(), "method name") {
		t.Fatalf("invalid method error = %v", err)
	}
	collision := *base
	collision.Types = []*protocol.NamedType{
		{ID: "one.Item", GoPkgPath: "one/api", GoName: "Item", Kind: protocol.KindStruct},
		{ID: "two.Item", GoPkgPath: "two/api", GoName: "Item", Kind: protocol.KindStruct},
	}
	if _, err := Generate(&collision, Options{}); err == nil {
		t.Fatal("Generate accepted duplicate generated type names")
	}
}

func TestUnexported(t *testing.T) {
	if got := unexported(""); got != "" {
		t.Fatalf("unexported(\"\") = %q, want empty string", got)
	}
	for _, tt := range []struct {
		input string
		want  string
	}{
		{"Client", "client"},
		{"URL", "uRL"},
		{"x", "x"},
	} {
		if got := unexported(tt.input); got != tt.want {
			t.Errorf("unexported(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGeneratedGoCompilesAndExecutes(t *testing.T) {
	const requestID = "example/generated.Request"
	const responseID = "example/generated.Response"
	const eventID = "example/generated.Event"
	ref := func(id string) protocol.TypeRef { return protocol.TypeRef{Kind: protocol.KindStruct, NamedType: id} }
	p := &protocol.Protocol{
		Version:   "compile-v1",
		Methods:   []protocol.Method{{Name: "math_add", RequestType: ref(requestID), ResponseType: ref(responseID)}},
		EventType: ref(eventID),
		Types: []*protocol.NamedType{
			{ID: requestID, GoPkgPath: "example/generated", GoName: "Request", Kind: protocol.KindStruct, Fields: []protocol.Field{{GoName: "Value", Type: protocol.TypeRef{Kind: protocol.KindInt}}}},
			{ID: responseID, GoPkgPath: "example/generated", GoName: "Response", Kind: protocol.KindStruct, Fields: []protocol.Field{{GoName: "Value", Type: protocol.TypeRef{Kind: protocol.KindInt}}}},
			{ID: eventID, GoPkgPath: "example/generated", GoName: "Event", Kind: protocol.KindStruct, Fields: []protocol.Field{{GoName: "Message", Type: protocol.TypeRef{Kind: protocol.KindString}}}},
		},
	}
	files, err := Generate(p, Options{Package: "generated", ClientName: "Demo"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain is not installed")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	goMod := fmt.Appendf(nil, "module generated-check\n\ngo 1.27.1\n\nrequire github.com/vehmloewff/latch v0.0.0\n\nreplace github.com/vehmloewff/latch => %s\n", root)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	rootSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), rootSum, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client.go"), files["client.go"], 0o644); err != nil {
		t.Fatal(err)
	}
	testSource := `package generated

import (
    "encoding/json"
    "testing"
)

func TestGeneratedClientRuns(t *testing.T) {
    client := New("ws://example.invalid", func(Event) {})
    if client == nil {
        t.Fatal("New returned nil")
    }
    raw, err := json.Marshal(Request{Value: 7})
    if err != nil {
        t.Fatal(err)
    }
    if string(raw) != ` + "`{\"Value\":7}`" + ` {
        t.Fatalf("request JSON = %s", raw)
    }
}
`
	if err := os.WriteFile(filepath.Join(dir, "client_generated_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goTool, "test", "-mod=mod", ".")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated Go test failed: %v\n%s", err, output)
	}
}
