package reflectapi

import (
	"reflect"
	"testing"
	"time"

	"github.com/vehmloewff/latch/protocol"
)

type Address struct {
	City string `latch:"1"`
	Zip  string `latch:"2,omitempty"`
}

type Status string

type Widget struct {
	ID        string            `latch:"1"`
	Name      string            `latch:"2,omitempty"`
	Nickname  *string           `latch:"3"`
	Alias     *string           `latch:"4,omitempty"`
	CreatedAt time.Time         `latch:"5"`
	Tags      []string          `latch:"6"`
	Home      Address           `latch:"7"`
	Backup    *Address          `latch:"8,omitempty"`
	Grid      [3]int            `latch:"9"`
	Meta      map[string]string `latch:"10"`
	Status    Status            `latch:"11" jsonschema_enum:"pending,active,disabled"`
	unexpo    string            //nolint:unused
}

func TestResolveStructShape(t *testing.T) {
	_ = Widget{}.unexpo // silence unused field lint in this scratch fixture
	r := NewRegistry()
	ref, err := r.Resolve(reflect.TypeOf(Widget{}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref.Kind != protocol.KindStruct {
		t.Fatalf("expected struct kind, got %s", ref.Kind)
	}

	types := r.Types()
	var widget, address, status *protocol.NamedType
	for _, nt := range types {
		switch nt.GoName {
		case "Widget":
			widget = nt
		case "Address":
			address = nt
		case "Status":
			status = nt
		}
	}
	if widget == nil || address == nil || status == nil {
		t.Fatalf("expected Widget, Address, Status named types, got %+v", types)
	}

	if status.Kind != protocol.KindEnum || len(status.EnumValues) != 3 {
		t.Fatalf("expected Status enum with 3 values, got %+v", status)
	}

	fieldsByName := map[string]protocol.Field{}
	for _, f := range widget.Fields {
		fieldsByName[f.GoName] = f
	}
	if len(widget.Fields) != 11 {
		t.Fatalf("expected 11 fields, got %d: %+v", len(widget.Fields), widget.Fields)
	}

	id := fieldsByName["ID"]
	if id.Optional || id.Nullable {
		t.Fatalf("id should be required+non-null, got %+v", id)
	}

	name := fieldsByName["Name"]
	if !name.Optional || name.Nullable {
		t.Fatalf("name should be optional+non-null, got %+v", name)
	}

	nickname := fieldsByName["Nickname"]
	if nickname.Optional || !nickname.Nullable {
		t.Fatalf("nickname should be required+nullable, got %+v", nickname)
	}
	if nickname.Type.Kind != protocol.KindPointer || nickname.Type.Elem.Kind != protocol.KindString {
		t.Fatalf("nickname should be pointer-to-string, got %+v", nickname.Type)
	}

	alias := fieldsByName["Alias"]
	if !alias.Optional || !alias.Nullable {
		t.Fatalf("alias should be optional+nullable, got %+v", alias)
	}

	createdAt := fieldsByName["CreatedAt"]
	if createdAt.Type.Kind != protocol.KindTime {
		t.Fatalf("createdAt should be KindTime, got %s", createdAt.Type.Kind)
	}

	tags := fieldsByName["Tags"]
	if tags.Type.Kind != protocol.KindSlice || tags.Type.Elem.Kind != protocol.KindString {
		t.Fatalf("tags should be []string, got %+v", tags.Type)
	}

	home := fieldsByName["Home"]
	if home.Type.Kind != protocol.KindStruct || home.Type.NamedType != address.ID {
		t.Fatalf("home should reference Address struct, got %+v", home.Type)
	}

	grid := fieldsByName["Grid"]
	if grid.Type.Kind != protocol.KindArray || grid.Type.ArrayLen != 3 {
		t.Fatalf("grid should be [3]int, got %+v", grid.Type)
	}

	meta := fieldsByName["Meta"]
	if meta.Type.Kind != protocol.KindMap || meta.Type.MapValue.Kind != protocol.KindString {
		t.Fatalf("meta should be map[string]string, got %+v", meta.Type)
	}

	statusField := fieldsByName["Status"]
	if statusField.Type.Kind != protocol.KindEnum {
		t.Fatalf("status field should be enum kind, got %+v", statusField.Type)
	}
}

func TestRejectAnonymousStruct(t *testing.T) {
	r := NewRegistry()
	type inline = struct {
		X string `latch:"1"`
	}
	_, err := r.Resolve(reflect.TypeOf(inline{}))
	if err == nil {
		t.Fatalf("expected error for anonymous struct")
	}
}

func TestRejectUnsupportedKinds(t *testing.T) {
	cases := []any{
		make(chan int),
		func() {},
		complex64(1),
		map[int]string{},
		int64(0),
		uint64(0),
	}
	for _, c := range cases {
		r := NewRegistry()
		_, err := r.Resolve(reflect.TypeOf(c))
		if err == nil {
			t.Fatalf("expected error resolving unsupported type %T", c)
		}
	}
}

func TestRejectEmbeddedField(t *testing.T) {
	type Base struct {
		X string `latch:"1"`
	}
	type WithEmbed struct {
		Base
		Y string `latch:"2"`
	}
	r := NewRegistry()
	_, err := r.Resolve(reflect.TypeOf(WithEmbed{}))
	if err == nil {
		t.Fatalf("expected error for embedded field")
	}
}

func TestSelfReferentialType(t *testing.T) {
	type Node struct {
		Value    string  `latch:"1"`
		Children []*Node `latch:"2"`
	}
	r := NewRegistry()
	ref, err := r.Resolve(reflect.TypeOf(Node{}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref.Kind != protocol.KindStruct {
		t.Fatalf("expected struct, got %s", ref.Kind)
	}
	types := r.Types()
	if len(types) != 1 {
		t.Fatalf("expected exactly 1 named type for self-referential Node, got %d", len(types))
	}
}

func TestDedupeSharedType(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Resolve(reflect.TypeOf(Widget{})); err != nil {
		t.Fatalf("Resolve Widget: %v", err)
	}
	if _, err := r.Resolve(reflect.TypeOf(Address{})); err != nil {
		t.Fatalf("Resolve Address: %v", err)
	}
	count := 0
	for _, nt := range r.Types() {
		if nt.GoName == "Address" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected Address registered exactly once, got %d", count)
	}
}
