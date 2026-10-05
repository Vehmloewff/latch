package rust_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vehmloewff/latch/chat_app_example/api"
	"github.com/vehmloewff/latch/codegen/rust"
	"github.com/vehmloewff/latch/protocol"
)

func sample() *protocol.Protocol {
	return &protocol.Protocol{Version: "v1", EventType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "Event"}, Methods: []protocol.Method{{Name: "user.get", RequestType: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "Query"}, ResponseType: protocol.TypeRef{Kind: protocol.KindArray, Elem: &protocol.TypeRef{Kind: protocol.KindEnum, NamedType: "Status"}, ArrayLen: 2}}}, Types: []*protocol.NamedType{
		{ID: "Event", GoName: "Event", Kind: protocol.KindStruct, Fields: []protocol.Field{{Number: 7, GoName: "Status", Type: protocol.TypeRef{Kind: protocol.KindEnum, NamedType: "Status"}}}},
		{ID: "Query", GoName: "Query", Kind: protocol.KindStruct, Fields: []protocol.Field{{Number: 9, GoName: "Items", Type: protocol.TypeRef{Kind: protocol.KindMap, MapValue: &protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}}}, {Number: 12, GoName: "Optional", Optional: true, Nullable: true, Type: protocol.TypeRef{Kind: protocol.KindPointer, Elem: &protocol.TypeRef{Kind: protocol.KindTime}}}}},
		{ID: "Status", GoName: "Status", Kind: protocol.KindEnum, EnumBase: protocol.KindString, EnumValues: []string{"ready", "in_progress"}},
	}}
}

func TestGenerate(t *testing.T) {
	files, err := rust.Generate(sample(), rust.Options{})
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource, err := os.ReadFile("runtime.rs")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || !bytes.Equal(files["src/runtime.rs"], runtimeSource) {
		t.Fatalf("incorrect files: %v", files)
	}
	cargo := string(files["Cargo.toml"])
	for _, part := range []string{"name = \"latch_client\"", "tungstenite = { version = \"0.24\", features = [\"rustls-tls-native-roots\"] }", "rustls = { version = \"0.23\", default-features = false, features = [\"ring\", \"std\"] }", "url = \"2.5\""} {
		if !strings.Contains(cargo, part) {
			t.Errorf("cargo missing %q", part)
		}
	}
	lib := string(files["src/lib.rs"])
	for _, part := range []string{"pub mod runtime;", "pub struct Query", "pub items: BTreeMap<String, Vec<u8>>", "pub optional: Option<LatchTime>", "fields.insert(9, Value::Map(", "fields.remove(&12)", "missing required field Items", "pub enum Status", "unknown enum value", "pub fn user_get(&self, request: Query) -> Result<[Status; 2], Error>", "self.transport.call(\"user.get\", payload)", "Transport::connect(url, \"v1\"", "pub fn connect_with_headers(", "Transport::connect_with_headers(url, \"v1\""} {
		if !strings.Contains(lib, part) {
			t.Errorf("lib missing %q", part)
		}
	}
	again, err := rust.Generate(sample(), rust.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range files {
		if !bytes.Equal(data, again[path]) {
			t.Errorf("nondeterministic %s", path)
		}
	}
}

func TestGeneratedCargoCompiles(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		if os.Getenv("LATCH_RUST_REQUIRE") != "" {
			t.Fatalf("cargo required for Rust CI tests: %v", err)
		}
		t.Skipf("cargo unavailable: %v", err)
	}
	target := filepath.Join(t.TempDir(), "target")
	t.Run("sample", func(t *testing.T) {
		p := sample()
		query := p.Types[1]
		query.Fields = append(query.Fields,
			protocol.Field{Number: 13, GoName: "Enabled", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindBool}},
			protocol.Field{Number: 14, GoName: "Count", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindInt32}},
			protocol.Field{Number: 15, GoName: "Ratio", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindFloat64}},
			protocol.Field{Number: 16, GoName: "Label", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindString}},
			protocol.Field{Number: 17, GoName: "Blob", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}},
			protocol.Field{Number: 18, GoName: "Fixed", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindArray, ArrayLen: 2, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}},
			protocol.Field{Number: 19, GoName: "RequiredPtr", Nullable: true, Type: protocol.TypeRef{Kind: protocol.KindPointer, Elem: &protocol.TypeRef{Kind: protocol.KindString}}},
			protocol.Field{Number: 20, GoName: "When", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindTime}},
			protocol.Field{Number: 21, GoName: "MaybeStatus", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindEnum, NamedType: "Status"}},
			protocol.Field{Number: 22, GoName: "Extra", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindStruct, NamedType: "Event"}},
			protocol.Field{Number: 23, GoName: "Nested", Type: protocol.TypeRef{Kind: protocol.KindMap, MapValue: &protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindUint8}}}}},
			protocol.Field{Number: 24, GoName: "Tags", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindSlice, Elem: &protocol.TypeRef{Kind: protocol.KindString}}},
			protocol.Field{Number: 25, GoName: "OptionalMap", Optional: true, Type: protocol.TypeRef{Kind: protocol.KindMap, MapValue: &protocol.TypeRef{Kind: protocol.KindUint8}}},
		)
		runCargo(t, p, target, sampleRustTests)
	})
	t.Run("chat", func(t *testing.T) {
		p, err := api.Build().Schema()
		if err != nil {
			t.Fatal(err)
		}
		runCargo(t, p, target, chatRustTests)
	})
	t.Run("recursive", func(t *testing.T) {
		p := recursiveSchema()
		files, err := rust.Generate(p, rust.Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"pub next: Option<Box<Node>>", "pub required: Option<Box<Node>>", "pub inline: Option<Box<[Node; 1]>>", "pub children: Vec<Option<Box<Node>>>", "pub parent: Option<Box<Parent>>"} {
			if !strings.Contains(string(files["src/lib.rs"]), want) {
				t.Errorf("generated Rust missing %q", want)
			}
		}
		runCargo(t, p, target, recursiveRustTests)
	})
}

func recursiveSchema() *protocol.Protocol {
	named := func(id string) protocol.TypeRef { return protocol.TypeRef{Kind: protocol.KindStruct, NamedType: id} }
	ptr := func(r protocol.TypeRef) protocol.TypeRef {
		return protocol.TypeRef{Kind: protocol.KindPointer, Elem: &r}
	}
	node := named("Node")
	parent := named("Parent")
	child := named("Child")
	array := protocol.TypeRef{Kind: protocol.KindArray, ArrayLen: 1, Elem: &node}
	list := protocol.TypeRef{Kind: protocol.KindSlice, Elem: ref(ptr(node))}
	return &protocol.Protocol{Version: "v1", EventType: node, Types: []*protocol.NamedType{
		{ID: "Node", GoName: "Node", Kind: protocol.KindStruct, Fields: []protocol.Field{
			{Number: 1, GoName: "Name", Type: protocol.TypeRef{Kind: protocol.KindString}},
			{Number: 2, GoName: "Next", Type: ptr(node), Optional: true, Nullable: true},
			{Number: 3, GoName: "Required", Type: ptr(node), Nullable: true},
			{Number: 4, GoName: "Children", Type: list},
			{Number: 5, GoName: "Peers", Type: protocol.TypeRef{Kind: protocol.KindMap, MapValue: ref(ptr(node))}},
			{Number: 6, GoName: "Inline", Type: ptr(array), Optional: true, Nullable: true},
		}},
		{ID: "Parent", GoName: "Parent", Kind: protocol.KindStruct, Fields: []protocol.Field{{Number: 1, GoName: "Child", Type: ptr(child), Optional: true, Nullable: true}}},
		{ID: "Child", GoName: "Child", Kind: protocol.KindStruct, Fields: []protocol.Field{{Number: 1, GoName: "Parent", Type: ptr(parent), Optional: true, Nullable: true}}},
	}}
}
func ref(r protocol.TypeRef) *protocol.TypeRef { return &r }

func runCargo(t *testing.T, p *protocol.Protocol, target string, rustTests string) {
	t.Helper()
	files, err := rust.Generate(p, rust.Options{})
	if err != nil {
		t.Fatal(err)
	}
	files["tests/generated.rs"] = []byte(rustTests)
	// CI fetches this locked graph before running the generated crates offline.
	// Re-resolving without it can select uncached or newly yanked dependencies.
	lock, err := os.ReadFile("../../chat_app_example/rust/Cargo.lock")
	if err != nil {
		t.Fatal(err)
	}
	const examplePackage = "name = \"chat_app_client\""
	if bytes.Count(lock, []byte(examplePackage)) != 1 {
		t.Fatal("Rust example lockfile must contain exactly one chat_app_client package")
	}
	files["Cargo.lock"] = bytes.Replace(lock, []byte(examplePackage), []byte("name = \"latch_client\""), 1)
	dir := t.TempDir()
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// The example integration runner owns the shared runtime tests. These
	// crates only need their schema-specific generation and codec checks.
	cmd := exec.Command("cargo", "test", "--locked", "--offline", "--quiet", "--test", "generated")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target)
	if out, err := cmd.CombinedOutput(); err != nil {
		if os.Getenv("LATCH_RUST_REQUIRE") == "" && (strings.Contains(string(out), "no matching package named") || strings.Contains(string(out), "failed to download")) {
			t.Skipf("cargo dependencies unavailable offline: %s", out)
		}
		t.Fatal(fmt.Sprintf("cargo test: %v\n%s", err, out))
	}
}

const sampleRustTests = `
use latch_client::*;
use std::collections::BTreeMap;

#[test]
fn header_hook_client_signature() {
 let err=LatchClient::connect_with_headers("https://invalid", |_| {}, |_| {}, |_headers| Ok(())).err().unwrap();
 assert_eq!(err.code, "invalid_url");
}

fn query() -> Query {
 Query { items: BTreeMap::new(), optional: None, enabled: false, count: 0, ratio: 0.0,
 label: String::new(), blob: None, fixed: None, required_ptr: None, when: None,
 maybe_status: None, extra: None, nested: BTreeMap::new(), tags: None, optional_map: None }
}
#[test]
fn omitted_scalar_fields_and_required_nullable_pointer() {
 let value=query().into_value();
 let Value::Struct(fields)=value.clone() else {panic!("expected struct")};
 assert!(fields.contains_key(&9));
 assert_eq!(fields.get(&19),Some(&Value::Null));
 for n in [12,13,14,15,16,17,18,20,21,22,24,25] { assert!(!fields.contains_key(&n), "field {n} must be omitted"); }
 assert_eq!(Query::from_value(value).unwrap(),query());
 let mut missing=fields.clone(); missing.remove(&19);
 assert!(Query::from_value(Value::Struct(missing)).unwrap_err().message.contains("RequiredPtr"));
 let mut explicit_null=fields.clone(); explicit_null.insert(13,Value::Null);
 assert!(Query::from_value(Value::Struct(explicit_null)).is_err());
 let mut empty=fields; empty.insert(17, Value::Bytes(vec![]));
 assert_eq!(Query::from_value(Value::Struct(empty.clone())).unwrap().blob, Some(vec![]));
 empty.insert(17, Value::Null);
 assert_eq!(Query::from_value(Value::Struct(empty)).unwrap().blob, None);
}
#[test]
fn roundtrip_nonzero_and_nested_bytes() {
 let mut q=query();q.enabled=true;q.count=-12;q.ratio=1.5;q.label="hi".into();
 q.blob=Some(vec![]);q.fixed=Some([0,1]);q.required_ptr=Some("yes".into());
  q.tags=Some(vec![]);q.optional_map=Some(BTreeMap::new());
 q.when=Some(LatchTime(123456));q.maybe_status=Some(Status::InProgress);
 q.extra=Some(Event{status:Status::Ready});
 q.items.insert("a".into(),vec![0,255]);
 q.nested.insert("b".into(),vec![vec![1,2],vec![]]);
 let value=q.into_value();
 let Value::Struct(fields)=&value else {panic!()};
 assert_eq!(fields.get(&17),Some(&Value::Bytes(vec![])));
  assert_eq!(fields.get(&24),Some(&Value::List(vec![])));
  assert_eq!(fields.get(&25),Some(&Value::Map(BTreeMap::new())));
 assert_eq!(fields.get(&20),Some(&Value::Time(123456)));
 assert_eq!(fields.get(&18),Some(&Value::List(vec![Value::Uint(0),Value::Uint(1)])));
 let Value::Map(items)=fields.get(&9).unwrap() else {panic!()};
 assert_eq!(items.get("a"),Some(&Value::Bytes(vec![0,255])));
 let bytes=runtime::encode_value(&value).unwrap();
 let decoded=runtime::decode_value(&bytes).unwrap();
 assert_eq!(Query::from_value(decoded).unwrap(),q);
 let mut missing=fields.clone(); missing.remove(&13);missing.remove(&14);missing.remove(&15);missing.remove(&16);
 let defaults=Query::from_value(Value::Struct(missing)).unwrap();
 assert!(!defaults.enabled);assert_eq!(defaults.count,0);assert_eq!(defaults.ratio,0.0);assert!(defaults.label.is_empty());
}
#[test]
fn enum_array_and_time_validation() {
 assert!(Status::from_value(Value::String("invalid".into())).is_err());
 assert!(Status::from_value(Value::String("ready".into())).is_ok());
 assert!(LatchTime::from_value(Value::Time(-1)).is_ok());
 let mut q=query();q.fixed=Some([0,0]);
  let Value::Struct(fields)=q.into_value() else {panic!()};assert!(!fields.contains_key(&18));
  q.fixed=Some([0,1]);
 let Value::Struct(mut fields)=q.into_value() else {panic!()};
 fields.insert(18,Value::List(vec![Value::Uint(1)]));
 assert!(Query::from_value(Value::Struct(fields)).is_err());
}
`

const recursiveRustTests = `
use latch_client::*;
use std::collections::BTreeMap;

fn node(name: &str) -> Node {
 Node { name: name.into(), next: None, required: None, children: vec![], peers: BTreeMap::new(), inline: None }
}
#[test]
fn self_recursive_pointer_roundtrip() {
 let mut root = node("root");
 root.next = Some(Box::new(node("next")));
 root.children = vec![Some(Box::new(node("child"))), None];
 root.peers.insert("friend".into(), Some(Box::new(node("peer"))));
 root.inline = Some(Box::new([node("inline")]));
 let value = root.into_value();
 let Value::Struct(fields) = &value else { panic!() };
 assert_eq!(fields.get(&3), Some(&Value::Null));
 let mut missing_required = fields.clone();
 missing_required.remove(&3);
 assert!(Node::from_value(Value::Struct(missing_required)).is_err());
 assert!(fields.contains_key(&2));
 assert!(fields.contains_key(&6));
 let bytes = runtime::encode_value(&value).unwrap();
 assert_eq!(Node::from_value(runtime::decode_value(&bytes).unwrap()).unwrap(), root);
 let mut absent = fields.clone();
 absent.remove(&2);
 assert!(Node::from_value(Value::Struct(absent)).unwrap().next.is_none());
 let mut explicit_null = fields.clone();
 explicit_null.insert(2, Value::Null);
 assert!(Node::from_value(Value::Struct(explicit_null)).unwrap().next.is_none());
 let mut required_null = fields.clone();
 required_null.insert(3, Value::Null);
 assert_eq!(Node::from_value(Value::Struct(required_null)).unwrap().required, None);
}
#[test]
fn mutual_recursive_pointers() {
 let parent = Parent { child: Some(Box::new(Child { parent: Some(Box::new(Parent { child: None })) })) };
 let encoded = runtime::encode_value(&parent.into_value()).unwrap();
 assert_eq!(Parent::from_value(runtime::decode_value(&encoded).unwrap()).unwrap(), parent);
 let empty = Parent { child: None };
 assert_eq!(empty.into_value(), Value::Struct(BTreeMap::new()));
}
`

const chatRustTests = `
use latch_client::*;
#[test]
fn chat_event_roundtrip() {
 let event=Event{kind:"message".into(),message:Some(Box::new(MessageReceived{message:ChatMessage{
 id:7,room:"general".into(),sender_id:"alice".into(),text:"hello".into(),sent_at:LatchTime(42)
 }})),presence:None};
 let value=event.into_value();
 let Value::Struct(fields)=&value else {panic!()};
 assert!(fields.contains_key(&2));assert!(!fields.contains_key(&3));
 let binary=runtime::encode_value(&value).unwrap();
 assert_eq!(Event::from_value(runtime::decode_value(&binary).unwrap()).unwrap(),event);
 let history=HistoryResponse::from_value(Value::Struct([(1,Value::Null)].into())).unwrap();
 assert!(history.messages.is_empty());
 assert!(HistoryResponse::from_value(Value::Struct(Default::default())).is_err());
}
`

func TestInvalidSchema(t *testing.T) {
	tests := []struct {
		name   string
		change func(*protocol.Protocol)
	}{
		{"missing event", func(p *protocol.Protocol) { p.EventType = protocol.TypeRef{} }},
		{"missing ref", func(p *protocol.Protocol) { p.Types[0].Fields[0].Type.NamedType = "Missing" }},
		{"duplicate tag", func(p *protocol.Protocol) { p.Types[1].Fields[1].Number = 9 }},
		{"duplicate field", func(p *protocol.Protocol) { p.Types[1].Fields[1].GoName = "Items" }},
		{"duplicate variant", func(p *protocol.Protocol) { p.Types[2].EnumValues = []string{"a_b", "aB"} }},
		{"invalid method", func(p *protocol.Protocol) { p.Methods[0].Name = "bad-name" }},
		{"missing element", func(p *protocol.Protocol) { p.Methods[0].ResponseType.Elem = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := sample()
			tt.change(p)
			if _, err := rust.Generate(p, rust.Options{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if _, err := rust.Generate(nil, rust.Options{}); err == nil {
		t.Fatal("expected nil protocol error")
	}
	if _, err := rust.Generate(sample(), rust.Options{ClientName: "Query"}); err == nil {
		t.Fatal("expected name conflict")
	}
}
