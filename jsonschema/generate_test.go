package jsonschema

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/vehmloewff/latch/protocol"
	"github.com/vehmloewff/latch/reflectapi"
)

type connectParams struct {
	Token     string  `json:"token" jsonschema:"minLength=1"`
	ProjectID string  `json:"projectId" jsonschema:"minLength=1"`
	Nickname  *string `json:"nickname,omitempty"`
}

func buildProtocol(t *testing.T, v any) (*protocol.Protocol, protocol.TypeRef) {
	t.Helper()
	r := reflectapi.NewRegistry()
	ref, err := r.Resolve(reflect.TypeOf(v))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return &protocol.Protocol{Types: r.Types()}, ref
}

func TestBuildDocumentAndValidate(t *testing.T) {
	p, ref := buildProtocol(t, connectParams{})
	doc := BuildDocument(p, ref)

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal doc: %v", err)
	}
	t.Logf("schema:\n%s", raw)

	v, err := Compile("urn:test:connect", doc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	valid := []byte(`{"token":"abc","projectId":"proj-1"}`)
	if err := v.ValidateJSON(valid); err != nil {
		t.Fatalf("expected valid payload to pass, got %v", err)
	}

	validWithNull := []byte(`{"token":"abc","projectId":"proj-1","nickname":null}`)
	if err := v.ValidateJSON(validWithNull); err != nil {
		t.Fatalf("expected nullable optional field to accept null, got %v", err)
	}

	missing := []byte(`{"token":"abc"}`)
	if err := v.ValidateJSON(missing); err == nil {
		t.Fatalf("expected missing required field to fail validation")
	}

	tooShort := []byte(`{"token":"","projectId":"proj-1"}`)
	if err := v.ValidateJSON(tooShort); err == nil {
		t.Fatalf("expected minLength violation to fail validation")
	}

	malformed := []byte(`{not json`)
	if err := v.ValidateJSON(malformed); err == nil {
		t.Fatalf("expected malformed JSON to fail validation")
	}
}

type nested struct {
	Inner innerType `json:"inner"`
	List  []Tag     `json:"list"`
}

type innerType struct {
	Value int `json:"value"`
}

type Tag struct {
	Name string `json:"name"`
}

func TestBuildDocumentNestedAndArrays(t *testing.T) {
	p, ref := buildProtocol(t, nested{})
	doc := BuildDocument(p, ref)

	v, err := Compile("urn:test:nested", doc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	valid := []byte(`{"inner":{"value":1},"list":[{"name":"a"},{"name":"b"}]}`)
	if err := v.ValidateJSON(valid); err != nil {
		t.Fatalf("expected valid nested payload, got %v", err)
	}

	invalid := []byte(`{"inner":{"value":"not-a-number"},"list":[]}`)
	if err := v.ValidateJSON(invalid); err == nil {
		t.Fatalf("expected type mismatch to fail validation")
	}
}
