package reflectapi

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vehmloewff/latch/protocol"
)

type registryCoverageMarshaler struct{}

func (registryCoverageMarshaler) MarshalJSON() ([]byte, error) {
	return json.Marshal("custom")
}

type registryCoverageString string

type registryCoverageEnum string

type registryCoverageEnumRepeat struct {
	Value registryCoverageEnum  `latch:"1" jsonschema_enum:"draft, sent, paid"`
	Alias *registryCoverageEnum `latch:"2" jsonschema_enum:"draft, sent, paid"`
}

type registryCoverageEnumConflict struct {
	First  registryCoverageEnum `latch:"1" jsonschema_enum:"draft,sent"`
	Second registryCoverageEnum `latch:"2" jsonschema_enum:"draft,paid"`
}

type registryCoverageInvalidPlain struct {
	Value string `latch:"1" jsonschema_enum:"one,two"`
}

type registryCoverageInvalidNumber struct {
	Value int `latch:"1" jsonschema_enum:"one,two"`
}

type registryCoverageInvalidPointer struct {
	Value *int `latch:"1" jsonschema_enum:"one,two"`
}

type registryCoverageTagged struct {
	Required string  `latch:"1"`
	Optional string  `latch:"2,omitempty"`
	Nullable *string `latch:"3"`
}

type registryCoverageOrderZulu struct {
	Value string `latch:"1"`
}

type registryCoverageOrderAlpha struct {
	Value string `latch:"1"`
}

func TestRegistryRejectsCustomJSONMarshalers(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(registryCoverageMarshaler{}),
		reflect.TypeOf((*registryCoverageMarshaler)(nil)),
	} {
		t.Run(typ.String(), func(t *testing.T) {
			_, err := NewRegistry().Resolve(typ)
			if err == nil || !strings.Contains(err.Error(), "custom marshaler") {
				t.Fatalf("Resolve(%s) error = %v, want custom marshaler rejection", typ, err)
			}
		})
	}
}

func TestRegistryTreatsNamedStringWithoutEnumAsString(t *testing.T) {
	r := NewRegistry()
	ref, err := r.Resolve(reflect.TypeOf(registryCoverageString("value")))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref != (protocol.TypeRef{Kind: protocol.KindString}) {
		t.Fatalf("named string ref = %#v, want plain string", ref)
	}
	if types := r.Types(); len(types) != 0 {
		t.Fatalf("named string registered types = %#v, want none", types)
	}
}

func TestRegistryEnumRepeatConflictAndPointerSemantics(t *testing.T) {
	t.Run("repeat and pointer", func(t *testing.T) {
		r := NewRegistry()
		ref, err := r.Resolve(reflect.TypeOf(registryCoverageEnumRepeat{}))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if ref.Kind != protocol.KindStruct {
			t.Fatalf("ref kind = %s, want struct", ref.Kind)
		}
		types := r.Types()
		if len(types) != 2 {
			t.Fatalf("registered types = %#v, want struct plus enum", types)
		}
		var enum *protocol.NamedType
		for _, typ := range types {
			if typ.GoName == "registryCoverageEnum" {
				enum = typ
			}
		}
		if enum == nil || enum.Kind != protocol.KindEnum {
			t.Fatalf("enum type = %#v, want named enum", enum)
		}
		if got, want := enum.EnumValues, []string{"draft", "sent", "paid"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("enum values = %v, want %v", got, want)
		}

		var structType *protocol.NamedType
		for _, typ := range types {
			if typ.GoName == "registryCoverageEnumRepeat" {
				structType = typ
			}
		}
		if structType == nil {
			t.Fatal("repeat fixture struct was not registered")
		}
		byName := map[string]protocol.Field{}
		for _, field := range structType.Fields {
			byName[field.GoName] = field
		}
		value := byName["Value"]
		alias := byName["Alias"]
		if value.Type.Kind != protocol.KindEnum || value.Type.NamedType != enum.ID {
			t.Fatalf("value field = %#v, want enum reference %q", value.Type, enum.ID)
		}
		if alias.Type.Kind != protocol.KindPointer || alias.Type.Elem == nil || alias.Type.Elem.Kind != protocol.KindEnum || alias.Type.Elem.NamedType != enum.ID {
			t.Fatalf("alias field = %#v, want pointer to enum %q", alias.Type, enum.ID)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		_, err := NewRegistry().Resolve(reflect.TypeOf(registryCoverageEnumConflict{}))
		if err == nil || !strings.Contains(err.Error(), "conflicting enum values") {
			t.Fatalf("Resolve conflict error = %v, want conflicting enum values", err)
		}
	})
}

func TestRegistryRejectsInvalidEnumTags(t *testing.T) {
	cases := []reflect.Type{
		reflect.TypeOf(registryCoverageInvalidPlain{}),
		reflect.TypeOf(registryCoverageInvalidNumber{}),
		reflect.TypeOf(registryCoverageInvalidPointer{}),
	}
	for _, typ := range cases {
		t.Run(typ.Name(), func(t *testing.T) {
			_, err := NewRegistry().Resolve(typ)
			if err == nil || !strings.Contains(err.Error(), "jsonschema_enum tag only applies to named string types") {
				t.Fatalf("Resolve(%s) error = %v, want invalid enum tag error", typ, err)
			}
		})
	}
}

func TestRegistryCarriesLatchTags(t *testing.T) {
	r := NewRegistry()
	ref, err := r.Resolve(reflect.TypeOf(registryCoverageTagged{}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref.Kind != protocol.KindStruct || len(r.Types()) != 1 {
		t.Fatalf("ref/types = %#v/%#v, want one named struct", ref, r.Types())
	}

	var named *protocol.NamedType
	for _, typ := range r.Types() {
		if typ.GoName == "registryCoverageTagged" {
			named = typ
		}
	}
	if named == nil {
		t.Fatal("registryCoverageTagged was not registered")
	}
	fields := make(map[string]protocol.Field, len(named.Fields))
	for _, field := range named.Fields {
		fields[field.GoName] = field
	}
	if len(fields) != 3 {
		t.Fatalf("fields = %#v, want all exported fields", fields)
	}
	if fields["Required"].Optional {
		t.Fatal("required latch field should remain required")
	}
	if !fields["Optional"].Optional {
		t.Fatal("latch omitempty field should be optional")
	}
	if !fields["Nullable"].Nullable || fields["Nullable"].Type.Kind != protocol.KindPointer {
		t.Fatalf("nullable field = %#v, want nullable pointer", fields["Nullable"])
	}
}

func TestRegistryTypesAreDeterministicallySortedByID(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Resolve(reflect.TypeOf(registryCoverageOrderZulu{})); err != nil {
		t.Fatalf("Resolve Zulu: %v", err)
	}
	if _, err := r.Resolve(reflect.TypeOf(registryCoverageOrderAlpha{})); err != nil {
		t.Fatalf("Resolve Alpha: %v", err)
	}

	first := r.Types()
	second := r.Types()
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("Types lengths = %d/%d, want 2/2", len(first), len(second))
	}
	ids := []string{first[0].ID, first[1].ID}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("Types IDs = %v, want sorted order", ids)
	}
	if first[0].GoName != "registryCoverageOrderAlpha" || first[1].GoName != "registryCoverageOrderZulu" {
		t.Fatalf("Types order = %q, %q, want Alpha then Zulu", first[0].GoName, first[1].GoName)
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("repeated Types call changed order: %v then %v", first, second)
		}
	}
}
