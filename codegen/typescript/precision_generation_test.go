package typescript

import (
	"strings"
	"testing"

	"github.com/vehmloewff/latch/protocol"
)

func precisionProtocol(fields ...protocol.Field) *protocol.Protocol {
	const packetID = "precision.Packet"
	packetRef := protocol.TypeRef{Kind: protocol.KindStruct, NamedType: packetID}
	return &protocol.Protocol{
		Version: "precision-v1",
		Methods: []protocol.Method{{
			Name:         "inspect",
			RequestType:  packetRef,
			ResponseType: packetRef,
		}},
		EventType: packetRef,
		Types: []*protocol.NamedType{{
			ID:        packetID,
			GoName:    "Packet",
			GoPkgPath: "example/precision",
			Kind:      protocol.KindStruct,
			Fields:    fields,
		}},
	}
}

func precisionField(goName string, typ protocol.TypeRef) protocol.Field {
	return protocol.Field{GoName: goName, Type: typ}
}

func precisionSlice(kind protocol.Kind) protocol.TypeRef {
	elem := protocol.TypeRef{Kind: kind}
	return protocol.TypeRef{Kind: protocol.KindSlice, Elem: &elem}
}

func precisionArray(kind protocol.Kind, length int) protocol.TypeRef {
	elem := protocol.TypeRef{Kind: kind}
	return protocol.TypeRef{Kind: protocol.KindArray, Elem: &elem, ArrayLen: length}
}

func generatePrecisionTypeScript(t *testing.T, p *protocol.Protocol) string {
	t.Helper()
	files, err := Generate(p, Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return string(files["client.ts"])
}

func assertGeneratedTypeScriptContains(t *testing.T, generated string, snippets ...string) {
	t.Helper()
	for _, snippet := range snippets {
		if !strings.Contains(generated, snippet) {
			t.Errorf("generated client.ts missing %q", snippet)
		}
	}
}

func TestGenerateTypeScriptPrecisionCases(t *testing.T) {
	t.Run("Go field names become camel case", func(t *testing.T) {
		generated := generatePrecisionTypeScript(t, precisionProtocol(
			precisionField("UserID", protocol.TypeRef{Kind: protocol.KindString}),
		))
		assertGeneratedTypeScriptContains(t, generated, `userId: string;`)
	})

	t.Run("bytes public type", func(t *testing.T) {
		generated := generatePrecisionTypeScript(t, precisionProtocol(
			precisionField("Bytes", precisionSlice(protocol.KindUint8)),
		))
		assertGeneratedTypeScriptContains(t, generated,
			`bytes: Uint8Array;`,
			`type: { kind: "bytes" }`,
		)
	})

	t.Run("fixed array length", func(t *testing.T) {
		generated := generatePrecisionTypeScript(t, precisionProtocol(
			precisionField("FixedBytes", precisionArray(protocol.KindUint8, 4)),
			precisionField("FixedInts", precisionArray(protocol.KindInt, 3)),
		))
		assertGeneratedTypeScriptContains(t, generated,
			`fixedBytes: Uint8Array;`,
			`fixedInts: [number, number, number];`,
			`type: { kind: "bytes", length: 4 }`,
			`type: { kind: "array", length: 3, elem: { kind: "int"`,
		)
	})

	t.Run("integer width metadata", func(t *testing.T) {
		generated := generatePrecisionTypeScript(t, precisionProtocol(
			precisionField("Int8Value", protocol.TypeRef{Kind: protocol.KindInt8}),
			precisionField("Uint8Value", protocol.TypeRef{Kind: protocol.KindUint8}),
		))
		assertGeneratedTypeScriptContains(t, generated,
			`int8Value: number;`,
			`uint8Value: number;`,
			`type: { kind: "int", bits: 8, min: -128, max: 127 }`,
			`type: { kind: "uint", bits: 8, min: 0, max: 255 }`,
		)
	})

	t.Run("timestamp representation", func(t *testing.T) {
		generated := generatePrecisionTypeScript(t, precisionProtocol(
			precisionField("Timestamp", protocol.TypeRef{Kind: protocol.KindTime}),
		))
		assertGeneratedTypeScriptContains(t, generated,
			`timestamp: bigint;`,
			`type: { kind: "time", unit: "nanoseconds" }`,
		)
	})

	t.Run("64-bit integers are exact", func(t *testing.T) {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("Generate panicked instead of emitting exact 64-bit types and metadata: %v", recovered)
			}
		}()
		generated := generatePrecisionTypeScript(t, precisionProtocol(
			precisionField("Int64Value", protocol.TypeRef{Kind: protocol.KindInt64}),
			precisionField("Uint64Value", protocol.TypeRef{Kind: protocol.KindUint64}),
		))
		assertGeneratedTypeScriptContains(t, generated,
			`int64Value: bigint;`,
			`uint64Value: bigint;`,
			`type: { kind: "int", bits: 64, min: -9223372036854775808n, max: 9223372036854775807n }`,
			`type: { kind: "uint", bits: 64, min: 0n, max: 18446744073709551615n }`,
		)
	})
}
