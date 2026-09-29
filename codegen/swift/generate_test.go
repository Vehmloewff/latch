package swift

import (
	"bytes"
	"testing"

	"github.com/vehmloewff/latch/protocol"
)

func TestGenerateProducesStandaloneSwiftClient(t *testing.T) {
	p := &protocol.Protocol{
		Version:   "v1",
		Methods:   []protocol.Method{{Name: "chat_send_message", RequestType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "request"}, ResponseType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "response"}}},
		EventType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "event"},
		Types: []*protocol.NamedType{
			{ID: "event", GoName: "Event", Kind: protocol.KindStruct, Fields: []protocol.Field{{GoName: "Kind", Type: protocol.TypeRef{Kind: protocol.KindString}}}},
			{ID: "request", GoName: "Request", Kind: protocol.KindStruct, Fields: []protocol.Field{{GoName: "Text", Type: protocol.TypeRef{Kind: protocol.KindString}}}},
			{ID: "response", GoName: "Response", Kind: protocol.KindStruct, Fields: []protocol.Field{{GoName: "Text", Type: protocol.TypeRef{Kind: protocol.KindString}}}},
		},
	}
	first, err := Generate(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first["LatchClient.swift"], second["LatchClient.swift"]) {
		t.Fatal("generation is not deterministic")
	}
	for _, want := range []string{"public final class LatchClient", "chatSendMessage", "public init(url: URL, onEvent: @escaping @Sendable (Event) -> Void, onConnectionStateChange: (@Sendable (ConnectionState) -> Void)? = nil, onRequestConstructed: (@Sendable (inout URLRequest) -> Void)? = nil)", "public enum ConnectionState: String, Sendable { case connecting, offline, connected }", "onConnectionStateChange?(.connecting)", "onState?(.connected)", "onState?(.offline)", "Task.sleep(for: .seconds(2))", "onEvent(value)", "onRequestConstructed?(&request)", "webSocketTask(with:request)", "onRequestConstructed: onRequestConstructed", "private struct Reader", "case .string(let s): if T.self == String.self{return s as! T}; if let t=T.self as? any LatchCodable.Type", "guard r.done() else { throw LatchError.malformed }"} {
		if !bytes.Contains(first["LatchClient.swift"], []byte(want)) {
			t.Errorf("generated Swift source missing %q", want)
		}
	}
}

func TestGenerateMapsSwiftTypesAndOptionalFields(t *testing.T) {
	stringRef := protocol.TypeRef{Kind: protocol.KindString}
	p := &protocol.Protocol{
		Version:   "v1",
		Methods:   []protocol.Method{{Name: "codec_round_trip", RequestType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "payload"}, ResponseType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "payload"}}},
		EventType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "payload"},
		Types: []*protocol.NamedType{
			{ID: "payload", GoName: "Payload", Kind: protocol.KindStruct, Fields: []protocol.Field{
				{GoName: "Count", Type: protocol.TypeRef{Kind: protocol.KindUint32}},
				{GoName: "Ratio", Type: protocol.TypeRef{Kind: protocol.KindFloat64}},
				{GoName: "Labels", Type: protocol.TypeRef{Kind: protocol.KindSlice, Elem: &stringRef}},
				{GoName: "Scores", Type: protocol.TypeRef{Kind: protocol.KindMap, MapValue: &protocol.TypeRef{Kind: protocol.KindFloat32}}},
				{GoName: "OptionalName", Type: stringRef, Optional: true},
				{GoName: "State", Type: protocol.TypeRef{Kind: protocol.KindEnum, NamedType: "state"}},
			}},
			{ID: "state", GoName: "State", Kind: protocol.KindEnum, EnumBase: protocol.KindString, EnumValues: []string{"open", "closed"}},
		},
	}
	files, err := Generate(p, Options{ClientName: "Chat"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"public final class Chat", "public var count: UInt32", "public var ratio: Double", "public var labels: [String]", "public var scores: [String: Float]", "public var optionalName: String?", `case Open = "open"`, "LatchValue.decodeMap"} {
		if !bytes.Contains(files["LatchClient.swift"], []byte(want)) {
			t.Errorf("generated Swift source missing %q", want)
		}
	}
}

func TestGenerateRejectsInvalidProtocols(t *testing.T) {
	p := &protocol.Protocol{Version: "v1", EventType: protocol.TypeRef{Kind: protocol.KindString}}
	p.Methods = []protocol.Method{{Name: "not valid", RequestType: protocol.TypeRef{Kind: protocol.KindString}, ResponseType: protocol.TypeRef{Kind: protocol.KindString}}}
	if _, err := Generate(p, Options{}); err == nil {
		t.Fatal("expected invalid method name error")
	}
	p.Methods = nil
	p.EventType = protocol.TypeRef{}
	if _, err := Generate(p, Options{}); err == nil {
		t.Fatal("expected missing event type error")
	}
}
