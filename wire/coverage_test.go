package wire

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

type coverageWireField struct {
	id    uint64
	value []byte
}

func coverageWireInt(value int64) []byte {
	raw := wireValue(KindInt)
	return wireAppendUvarint(raw, wireZigzag(value))
}

func coverageWireStruct(fields ...coverageWireField) []byte {
	raw := []byte{KindStruct}
	raw = wireAppendUvarint(raw, uint64(len(fields)))
	for _, field := range fields {
		raw = wireAppendUvarint(raw, field.id)
		raw = append(raw, field.value...)
	}
	return raw
}

func TestCoverageDecodesInterfaceKindsAndInterfaceFields(t *testing.T) {
	type nested struct {
		Name string `latch:"1"`
	}
	type message struct {
		Value any `latch:"1"`
	}

	when := time.Unix(123, 456).UTC()
	cases := []struct {
		name  string
		input any
		want  any
	}{
		{"false", false, false},
		{"true", true, true},
		{"int", int64(-42), int64(-42)},
		{"uint", uint64(42), uint64(42)},
		{"float32", float32(1.25), float32(1.25)},
		{"float64", float64(2.5), float64(2.5)},
		{"string", "text", "text"},
		{"bytes", []byte{1, 2, 3}, []byte{1, 2, 3}},
		{"time", when, when},
		{"list", []any{false, int64(3)}, []any{false, int64(3)}},
		{"map", map[string]any{"answer": int64(42)}, map[string]any{"answer": int64(42)}},
		{"struct", nested{Name: "inside"}, map[uint64]any{1: "inside"}},
		{"null", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Encode(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			var output any
			if err := Decode(raw, &output); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(output, tc.want) {
				t.Fatalf("decoded %#v, want %#v", output, tc.want)
			}
		})
	}

	raw, err := Encode(message{Value: map[string]any{
		"answer": int64(42),
		"nested": []any{false, when},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var output message
	if err := Decode(raw, &output); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"answer": int64(42),
		"nested": []any{false, when},
	}
	if !reflect.DeepEqual(output.Value, want) {
		t.Fatalf("interface field = %#v, want %#v", output.Value, want)
	}
}

func TestCoverageInterfaceDecodeRejectsNonAssignableValue(t *testing.T) {
	raw, err := Encode("text")
	if err != nil {
		t.Fatal(err)
	}
	var output fmt.Stringer
	if err := Decode(raw, &output); err == nil {
		t.Fatal("decoded a string into an incompatible interface")
	}
}

func TestCoverageUnknownNestedValuesAreSkipped(t *testing.T) {
	unknownList := wireValue(KindList)
	unknownList = wireAppendUvarint(unknownList, 2)
	unknownList = append(unknownList, wireValue(KindInt, 14)...)
	unknownList = append(unknownList, wireValue(KindMap, 1, KindString, 4, 'd', 'e', 'e', 'p', KindNull)...)

	unknownStruct := coverageWireStruct(
		coverageWireField{id: 1, value: wireValue(KindString, 4, 'i', 'g', 'n', 'o')},
		coverageWireField{id: 99, value: unknownList},
	)
	raw := coverageWireStruct(
		coverageWireField{id: 1, value: coverageWireInt(7)},
		coverageWireField{id: 99, value: unknownStruct},
	)

	var output struct {
		Known int `latch:"1"`
	}
	if err := Decode(raw, &output); err != nil {
		t.Fatalf("decoding unknown nested values: %v", err)
	}
	if output.Known != 7 {
		t.Fatalf("known field = %d, want 7", output.Known)
	}
}

func TestCoverageSkipHandlesEveryValueKind(t *testing.T) {
	type nested struct {
		Number int    `latch:"1"`
		Text   string `latch:"2"`
	}
	type message struct {
		Known    int            `latch:"1"`
		Flag     bool           `latch:"2"`
		Unsigned uint64         `latch:"3"`
		When     time.Time      `latch:"4"`
		Float32  float32        `latch:"5"`
		Float64  float64        `latch:"6"`
		Text     string         `latch:"7"`
		Bytes    []byte         `latch:"8"`
		List     []int          `latch:"9"`
		Map      map[string]int `latch:"10"`
		Nested   nested         `latch:"11"`
	}
	raw, err := Encode(message{
		Known:    7,
		Flag:     true,
		Unsigned: 8,
		When:     time.Unix(9, 10).UTC(),
		Float32:  1.5,
		Float64:  2.5,
		Text:     "unknown",
		Bytes:    []byte{1, 2},
		List:     []int{3, 4},
		Map:      map[string]int{"key": 5},
		Nested:   nested{Number: 6, Text: "nested"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Known int `latch:"1"`
	}
	if err := Decode(raw, &output); err != nil {
		t.Fatalf("decoding unknown values: %v", err)
	}
	if output.Known != 7 {
		t.Fatalf("known field = %d, want 7", output.Known)
	}
}

func TestCoverageUnknownNestedValuesReportMalformedErrors(t *testing.T) {
	cases := []struct {
		name  string
		value []byte
	}{
		{
			name:  "truncated_string",
			value: wireValue(KindString, 2, 'x'),
		},
		{
			name:  "truncated_list_item",
			value: wireValue(KindList, 1),
		},
		{
			name:  "map_with_non_string_key",
			value: wireValue(KindMap, 1, KindInt, 0),
		},
		{
			name: "duplicate_nested_struct_field",
			value: coverageWireStruct(
				coverageWireField{id: 1, value: wireValue(KindNull)},
				coverageWireField{id: 1, value: wireValue(KindNull)},
			),
		},
		{
			name:  "zero_nested_struct_field_id",
			value: coverageWireStruct(coverageWireField{id: 0, value: wireValue(KindNull)}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := coverageWireStruct(coverageWireField{id: 99, value: tc.value})
			var output struct {
				Known int `latch:"1"`
			}
			if err := Decode(raw, &output); err == nil {
				t.Fatalf("accepted malformed unknown value %x", tc.value)
			}
		})
	}
}

func TestCoverageInterfaceDecodeReportsMalformedNestedValues(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"truncated_list", wireValue(KindList, 1)},
		{"non_string_map_key", wireValue(KindMap, 1, KindInt, 0)},
		{"invalid_struct_id", wireValue(KindStruct, 1, 0, KindNull)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output any
			if err := Decode(tc.raw, &output); err == nil {
				t.Fatalf("accepted malformed interface value %x", tc.raw)
			}
		})
	}
}
