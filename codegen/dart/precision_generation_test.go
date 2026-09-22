package dart

import (
	"strings"
	"testing"

	"github.com/vehmloewff/latch/protocol"
)

func dartPrecisionProtocol(fields ...protocol.Field) *protocol.Protocol {
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

func dartPrecisionField(goName string, typ protocol.TypeRef) protocol.Field {
	return protocol.Field{GoName: goName, Type: typ}
}

func dartPrecisionSlice(kind protocol.Kind) protocol.TypeRef {
	elem := protocol.TypeRef{Kind: kind}
	return protocol.TypeRef{Kind: protocol.KindSlice, Elem: &elem}
}

func dartPrecisionArray(kind protocol.Kind, length int) protocol.TypeRef {
	elem := protocol.TypeRef{Kind: kind}
	return protocol.TypeRef{Kind: protocol.KindArray, Elem: &elem, ArrayLen: length}
}

func generatePrecisionDart(t *testing.T, p *protocol.Protocol) string {
	t.Helper()
	files, err := Generate(p, Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return string(files["lib/client.dart"])
}

func assertGeneratedDartContains(t *testing.T, generated string, snippets ...string) {
	t.Helper()
	for _, snippet := range snippets {
		if !strings.Contains(generated, snippet) {
			t.Errorf("generated client.dart missing %q", snippet)
		}
	}
}

func TestGenerateDartPrecisionCases(t *testing.T) {
	t.Run("Go field names become camel case", func(t *testing.T) {
		generated := generatePrecisionDart(t, dartPrecisionProtocol(
			dartPrecisionField("UserID", protocol.TypeRef{Kind: protocol.KindString}),
		))
		assertGeneratedDartContains(t, generated, `final String userId;`)
	})

	t.Run("bytes public type", func(t *testing.T) {
		generated := generatePrecisionDart(t, dartPrecisionProtocol(
			dartPrecisionField("Bytes", dartPrecisionSlice(protocol.KindUint8)),
		))
		assertGeneratedDartContains(t, generated, `final Uint8List bytes;`)
	})

	t.Run("fixed array element types", func(t *testing.T) {
		generated := generatePrecisionDart(t, dartPrecisionProtocol(
			dartPrecisionField("FixedBytes", dartPrecisionArray(protocol.KindUint8, 4)),
			dartPrecisionField("FixedInts", dartPrecisionArray(protocol.KindInt, 3)),
		))
		assertGeneratedDartContains(t, generated,
			`final Uint8List fixedBytes;`,
			`final List<int> fixedInts;`,
		)
	})

	t.Run("integer public types", func(t *testing.T) {
		generated := generatePrecisionDart(t, dartPrecisionProtocol(
			dartPrecisionField("Int8Value", protocol.TypeRef{Kind: protocol.KindInt8}),
			dartPrecisionField("Uint8Value", protocol.TypeRef{Kind: protocol.KindUint8}),
		))
		assertGeneratedDartContains(t, generated,
			`final int int8Value;`,
			`final int uint8Value;`,
		)
	})

	t.Run("unsigned 64-bit public type is exact", func(t *testing.T) {
		generated := generatePrecisionDart(t, dartPrecisionProtocol(
			dartPrecisionField("Uint64Value", protocol.TypeRef{Kind: protocol.KindUint64}),
		))
		assertGeneratedDartContains(t, generated, `final BigInt uint64Value;`)
	})

	t.Run("timestamp representation", func(t *testing.T) {
		generated := generatePrecisionDart(t, dartPrecisionProtocol(
			dartPrecisionField("Timestamp", protocol.TypeRef{Kind: protocol.KindTime}),
		))
		assertGeneratedDartContains(t, generated, `final DateTime timestamp;`)
	})

	t.Run("signed 64-bit integers are supported", func(t *testing.T) {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("Generate panicked instead of emitting the int64 Dart type: %v", recovered)
			}
		}()
		generated := generatePrecisionDart(t, dartPrecisionProtocol(
			dartPrecisionField("Int64Value", protocol.TypeRef{Kind: protocol.KindInt64}),
		))
		assertGeneratedDartContains(t, generated, `final int int64Value;`)
	})
}

func TestGenerateDartEmbedsTheStandaloneBinaryRuntime(t *testing.T) {
	generated := generatePrecisionDart(t, dartPrecisionProtocol(
		dartPrecisionField("Bytes", dartPrecisionSlice(protocol.KindUint8)),
		dartPrecisionField("FixedBytes", dartPrecisionArray(protocol.KindUint8, 4)),
		dartPrecisionField("Timestamp", protocol.TypeRef{Kind: protocol.KindTime}),
	))

	if !strings.Contains(generated, stripImports(binaryRuntimeSource)) {
		t.Fatal("generated client.dart does not contain the standalone embedded binary runtime")
	}
	assertGeneratedDartContains(t, generated,
		`class UIntValue {`,
		`static const int bytes = 8;`,
		`static const int time = 12;`,
		`DateTime.fromMicrosecondsSinceEpoch`,
		`microsecondsSinceEpoch * 1000`,
	)
	if strings.Contains(generated, "package:latch_binary_runtime") {
		t.Fatal("generated client.dart must not import the development binary runtime package")
	}
}
