package wire

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"
	"unicode/utf8"
)

func wireAppendUvarint(dst []byte, value uint64) []byte {
	var encoded [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(encoded[:], value)
	return append(dst, encoded[:n]...)
}

func wireZigzag(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}

func wireValue(kind byte, payload ...byte) []byte {
	return append([]byte{kind}, payload...)
}

func wireStruct(fields ...struct {
	id    uint64
	value []byte
}) []byte {
	raw := wireValue(KindStruct)
	raw = wireAppendUvarint(raw, uint64(len(fields)))
	for _, field := range fields {
		raw = wireAppendUvarint(raw, field.id)
		raw = append(raw, field.value...)
	}
	return raw
}

func wireEnvelopeRaw(version byte, code byte, fields ...[]byte) []byte {
	raw := []byte{version, code}
	for _, field := range fields {
		raw = wireAppendUvarint(raw, uint64(len(field)))
		raw = append(raw, field...)
	}
	return raw
}

func wireEnvelopeFields(values ...[]byte) [][]byte {
	fields := make([][]byte, 7)
	copy(fields, values)
	return fields
}

func TestWireIntegerBoundariesAndOverflow(t *testing.T) {
	minInt := -int(^uint(0)>>1) - 1
	maxInt := int(^uint(0) >> 1)
	maxUint := ^uint(0)

	values := []any{
		int8(-128), int8(127),
		int16(-32768), int16(32767),
		int32(-2147483648), int32(2147483647),
		int64(math.MinInt64), int64(math.MaxInt64),
		minInt, maxInt,
		uint8(0), uint8(255),
		uint16(0), uint16(65535),
		uint32(0), uint32(math.MaxUint32),
		uint64(0), uint64(math.MaxUint64),
		uint(0), maxUint,
	}
	for _, input := range values {
		t.Run(reflect.TypeOf(input).String(), func(t *testing.T) {
			raw, err := Encode(input)
			if err != nil {
				t.Fatal(err)
			}
			out := reflect.New(reflect.TypeOf(input))
			if err := Decode(raw, out.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Elem().Interface(), input) {
				t.Fatalf("round trip changed %v into %v", input, out.Elem().Interface())
			}
		})
	}

	intOverflow := []struct {
		name  string
		dst   any
		value int64
	}{
		{"int8_high", new(int8), 128},
		{"int8_low", new(int8), -129},
		{"int16_high", new(int16), 32768},
		{"int32_high", new(int32), 2147483648},
	}
	for _, tc := range intOverflow {
		t.Run(tc.name, func(t *testing.T) {
			raw := wireValue(KindInt)
			raw = wireAppendUvarint(raw, wireZigzag(tc.value))
			if err := Decode(raw, tc.dst); err == nil {
				t.Fatalf("accepted signed overflow %d for %T", tc.value, tc.dst)
			}
		})
	}

	uintOverflow := []struct {
		name  string
		dst   any
		value uint64
	}{
		{"uint8", new(uint8), 256},
		{"uint16", new(uint16), 65536},
		{"uint32", new(uint32), uint64(math.MaxUint32) + 1},
	}
	for _, tc := range uintOverflow {
		t.Run(tc.name, func(t *testing.T) {
			raw := wireValue(KindUint)
			raw = wireAppendUvarint(raw, tc.value)
			if err := Decode(raw, tc.dst); err == nil {
				t.Fatalf("accepted unsigned overflow %d for %T", tc.value, tc.dst)
			}
		})
	}

	var signed int64
	if err := Decode(wireValue(KindUint, 1), &signed); err == nil {
		t.Fatal("decoded an unsigned wire value into a signed destination")
	}
}

func TestWireUintptrRoundTrip(t *testing.T) {
	input := uintptr(^uint(0))
	raw, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	var output uintptr
	if err := Decode(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output != input {
		t.Fatalf("uintptr round trip changed %d into %d", input, output)
	}
}

func TestWireFloatSpecialValuesPreserveBits(t *testing.T) {
	float32Values := []uint32{0x00000000, 0x80000000, 0x7f800000, 0xff800000, 0x7fc00001}
	for _, bits := range float32Values {
		t.Run("float32_"+string(rune(bits&0xff)), func(t *testing.T) {
			input := math.Float32frombits(bits)
			raw, err := Encode(input)
			if err != nil {
				t.Fatal(err)
			}
			var output float32
			if err := Decode(raw, &output); err != nil {
				t.Fatal(err)
			}
			if got := math.Float32bits(output); got != bits {
				t.Fatalf("float32 bits changed from %#x to %#x", bits, got)
			}
		})
	}

	float64Values := []uint64{0x0000000000000000, 0x8000000000000000, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000001}
	for _, bits := range float64Values {
		t.Run("float64", func(t *testing.T) {
			input := math.Float64frombits(bits)
			raw, err := Encode(input)
			if err != nil {
				t.Fatal(err)
			}
			var output float64
			if err := Decode(raw, &output); err != nil {
				t.Fatal(err)
			}
			if got := math.Float64bits(output); got != bits {
				t.Fatalf("float64 bits changed from %#x to %#x", bits, got)
			}
		})
	}
}

func TestWireRejectsMalformedOverflowAndNoncanonicalVarints(t *testing.T) {
	overflow := []byte{KindUint}
	for i := 0; i < 9; i++ {
		overflow = append(overflow, 0x80)
	}
	overflow = append(overflow, 0x02)

	unterminated := []byte{KindUint}
	for i := 0; i < 10; i++ {
		unterminated = append(unterminated, 0x80)
	}

	cases := []struct {
		name string
		raw  []byte
		dst  any
	}{
		{"signed_noncanonical_zero", wireValue(KindInt, 0x80, 0x00), new(int64)},
		{"unsigned_noncanonical_zero", wireValue(KindUint, 0x80, 0x00), new(uint64)},
		{"string_length_noncanonical_zero", wireValue(KindString, 0x80, 0x00), new(string)},
		{"overflow", overflow, new(uint64)},
		{"unterminated", unterminated, new(uint64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Decode(tc.raw, tc.dst); err == nil {
				t.Fatalf("accepted malformed varint %x", tc.raw)
			}
		})
	}

	listCount := wireValue(KindList)
	listCount = append(listCount, 0x80, 0x00, KindNull)
	var list []any
	if err := Decode(listCount, &list); err == nil {
		t.Fatal("accepted a noncanonical list count")
	}
}

func TestWireRejectsInvalidTags(t *testing.T) {
	invalidTagCases := []struct {
		name string
		dst  any
	}{
		{"text", new(struct {
			A int `latch:"not-a-number"`
		})},
		{"zero", new(struct {
			A int `latch:"0"`
		})},
		{"overflow", new(struct {
			A int `latch:"4294967296"`
		})},
	}
	for _, tc := range invalidTagCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Decode(wireStruct(struct {
				id    uint64
				value []byte
			}{1, wireValue(KindInt, 2)}), tc.dst); err == nil {
				t.Fatal("accepted an invalid latch tag")
			}
		})
	}
}

func TestWireRejectsDuplicateIncomingStructIDs(t *testing.T) {
	raw := wireStruct(
		struct {
			id    uint64
			value []byte
		}{1, wireValue(KindInt, 2)},
		struct {
			id    uint64
			value []byte
		}{1, wireValue(KindInt, 4)},
	)
	var output struct {
		A int `latch:"1"`
	}
	if err := Decode(raw, &output); err == nil {
		t.Fatal("accepted duplicate incoming struct field IDs")
	}
}

func TestWireRejectsInvalidIncomingStructIDs(t *testing.T) {
	cases := []struct {
		name string
		id   uint64
	}{
		{"zero", 0},
		{"too_large", uint64(math.MaxUint32) + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := wireStruct(struct {
				id    uint64
				value []byte
			}{tc.id, wireValue(KindNull)})
			var output struct {
				Known int `latch:"1"`
			}
			if err := Decode(raw, &output); err == nil {
				t.Fatalf("accepted invalid incoming struct ID %d", tc.id)
			}
		})
	}
}

func TestWireUnknownNestedFieldsAreSkipped(t *testing.T) {
	type sourceNested struct {
		Known int              `latch:"1"`
		Extra []map[string]any `latch:"99"`
	}
	type source struct {
		Nested sourceNested `latch:"1"`
	}
	type targetNested struct {
		Known int `latch:"1"`
	}
	type target struct {
		Nested targetNested `latch:"1"`
	}

	raw, err := Encode(source{Nested: sourceNested{
		Known: 7,
		Extra: []map[string]any{{"deep": []any{int64(1), "two", nil}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var output target
	if err := Decode(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.Nested.Known != 7 {
		t.Fatalf("known nested field changed: %+v", output)
	}
}

func TestWireRejectsDuplicateMapKeys(t *testing.T) {
	raw := wireValue(KindMap)
	raw = wireAppendUvarint(raw, 2)
	raw = append(raw, KindString, 1, 'x', KindInt, 2)
	raw = append(raw, KindString, 1, 'x', KindInt, 4)

	var output map[string]int
	if err := Decode(raw, &output); err == nil {
		t.Fatal("accepted duplicate map keys")
	}
}

func TestWireRejectsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff, 0xfe})
	if utf8.ValidString(invalid) {
		t.Fatal("test input unexpectedly became valid UTF-8")
	}
	if _, err := Encode(invalid); err == nil {
		t.Fatal("encoded invalid UTF-8 string")
	}
	if _, err := Encode(map[string]int{invalid: 1}); err == nil {
		t.Fatal("encoded invalid UTF-8 map key")
	}

	raw := wireValue(KindString, 2, 0xff, 0xfe)
	var output string
	if err := Decode(raw, &output); err == nil {
		t.Fatal("decoded invalid UTF-8 string")
	}
}

func TestWireNilAndTopLevelValues(t *testing.T) {
	raw, err := Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw, []byte{KindNull}) {
		t.Fatalf("nil encoded as %x", raw)
	}
	var interfaceOutput any = "non-nil"
	if err := Decode(raw, &interfaceOutput); err != nil {
		t.Fatal(err)
	}
	if interfaceOutput != nil {
		t.Fatalf("decoded nil into %#v", interfaceOutput)
	}

	var pointer *int
	raw, err = Encode(pointer)
	if err != nil {
		t.Fatal(err)
	}
	var pointerOutput *int
	if err := Decode(raw, &pointerOutput); err != nil {
		t.Fatal(err)
	}
	if pointerOutput != nil {
		t.Fatal("typed nil pointer was not preserved")
	}

	var nilSlice []int
	raw, err = Encode(nilSlice)
	if err != nil {
		t.Fatal(err)
	}
	var sliceOutput []int
	if err := Decode(raw, &sliceOutput); err != nil {
		t.Fatal(err)
	}
	if sliceOutput != nil {
		t.Fatal("nil slice was not preserved")
	}

	if err := Decode([]byte{}, &interfaceOutput); err == nil {
		t.Fatal("accepted an empty top-level value")
	}
	if err := Decode(raw, nil); err == nil {
		t.Fatal("accepted a nil decode destination")
	}
}

func TestWireArraysRequireExactLengths(t *testing.T) {
	raw := wireValue(KindList, 1, KindInt, 2)
	var output [2]int
	if err := Decode(raw, &output); err == nil {
		t.Fatal("accepted a list with the wrong array length")
	}

	raw = wireValue(KindList, 2, KindInt, 2, KindInt, 4)
	if err := Decode(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output != [2]int{1, 2} {
		t.Fatalf("decoded array mismatch: %v", output)
	}
}

func TestWireContainerBounds(t *testing.T) {
	tooLarge := wireAppendUvarint(nil, maxContainer+1)
	cases := []struct {
		name string
		raw  []byte
		dst  any
	}{
		{"string", append([]byte{KindString}, tooLarge...), new(string)},
		{"bytes", append([]byte{KindBytes}, tooLarge...), new([]byte)},
		{"list", append([]byte{KindList}, tooLarge...), new([]int)},
		{"map", append([]byte{KindMap}, tooLarge...), new(map[string]int)},
		{"struct", append([]byte{KindStruct}, tooLarge...), new(struct{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Decode(tc.raw, tc.dst); err == nil {
				t.Fatalf("accepted container count/length above maxContainer: %x", tc.raw)
			}
		})
	}
}

func TestWireMaximumNestingDepth(t *testing.T) {
	raw := make([]byte, 0, (maxDepth+2)*4)
	for i := 0; i < maxDepth+2; i++ {
		raw = append(raw, KindStruct, 1, 1)
	}
	raw = append(raw, KindNull)

	type node struct {
		Child *node `latch:"1"`
	}
	var output node
	if err := Decode(raw, &output); err == nil {
		t.Fatal("accepted a value deeper than maxDepth")
	}
}

func TestWireDecodesIntoInterfaces(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  any
	}{
		{"int", int64(-7), int64(-7)},
		{"uint", uint64(9), uint64(9)},
		{"float32", float32(1.5), float32(1.5)},
		{"float64", float64(2.5), float64(2.5)},
		{"string", "value", "value"},
		{"bytes", []byte{1, 2, 255}, []byte{1, 2, 255}},
		{"list", []any{int64(1), "two", nil}, []any{int64(1), "two", nil}},
		{"map", map[string]any{"answer": int64(42)}, map[string]any{"answer": int64(42)}},
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
}

func TestWireEnvelopeFrameCodesAndRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		env  Envelope
		code byte
	}{
		{"connect", Envelope{Type: FrameConnect, Version: "1", Payload: []byte{KindNull}}, 1},
		{"connected", Envelope{Type: FrameConnected, Version: "1", Payload: []byte{}}, 2},
		{"request", Envelope{Type: FrameRequest, ID: "1", Method: "ping", Payload: []byte{KindNull}}, 3},
		{"response", Envelope{Type: FrameResponse, ID: "1", Payload: []byte{KindNull}}, 4},
		{"error", Envelope{Type: FrameError, ID: "1", Payload: []byte{}, Error: "bad", ErrorCode: "invalid_request"}, 5},
		{"event", Envelope{Type: FrameEvent, Payload: []byte{KindNull}}, 6},
		{"connection_error", Envelope{Type: FrameConnectionError, Payload: []byte{}, Error: "closed", ErrorCode: "connection_closed"}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.env.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if raw[0] != 1 || raw[1] != tc.code {
				t.Fatalf("header %x, want version 1/code %d", raw[:2], tc.code)
			}
			var output Envelope
			if err := output.UnmarshalBinary(raw); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(output, tc.env) {
				t.Fatalf("round trip changed %+v into %+v", tc.env, output)
			}
		})
	}

	if _, err := (Envelope{Type: FrameType("unknown")}).MarshalBinary(); err == nil {
		t.Fatal("encoded an unknown envelope frame type")
	}
}

func TestWireEnvelopeRejectsInvalidVersionCodesTruncationAndTrailingData(t *testing.T) {
	valid, err := (Envelope{Type: FrameResponse, ID: "42", Payload: []byte{1, 2, 3}}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []byte{0, 2, 0xff} {
		raw := append([]byte(nil), valid...)
		raw[0] = version
		var output Envelope
		if err := output.UnmarshalBinary(raw); err == nil {
			t.Fatalf("accepted protocol version %d", version)
		}
	}
	for n := 0; n < len(valid); n++ {
		var output Envelope
		if err := output.UnmarshalBinary(valid[:n]); err == nil {
			t.Fatalf("accepted truncated envelope at byte %d", n)
		}
	}
	withTrailing := append(append([]byte(nil), valid...), 0)
	var output Envelope
	if err := output.UnmarshalBinary(withTrailing); err == nil {
		t.Fatal("accepted envelope with trailing bytes")
	}
}

func TestWireEnvelopeRejectsNoncanonicalLengthsAndInvalidUTF8(t *testing.T) {
	fields := wireEnvelopeFields(nil, []byte("42"), nil, nil, []byte{KindNull}, nil, nil)
	raw := wireEnvelopeRaw(1, 4, fields...)
	// The first empty field has a noncanonical zero length.
	raw = append([]byte{1, 4, 0x80, 0x00}, raw[3:]...)
	var output Envelope
	if err := output.UnmarshalBinary(raw); err == nil {
		t.Fatal("accepted a noncanonical envelope length")
	}

	invalidFields := wireEnvelopeFields([]byte{0xff}, nil, nil, nil, nil, nil, nil)
	if err := output.UnmarshalBinary(wireEnvelopeRaw(1, 4, invalidFields...)); err == nil {
		t.Fatal("accepted invalid UTF-8 in an envelope string field")
	}
}

func TestWireEnvelopeRequiresSemanticFields(t *testing.T) {
	invalid := []struct {
		name string
		env  Envelope
		code byte
		want [][]byte
	}{
		{"connect_without_version", Envelope{Type: FrameConnect, Payload: []byte{KindNull}}, 1, wireEnvelopeFields(nil, nil, nil, nil, []byte{KindNull}, nil, nil)},
		{"connected_without_version", Envelope{Type: FrameConnected, Payload: []byte{}}, 2, wireEnvelopeFields(nil, nil, nil, nil, []byte{}, nil, nil)},
		{"request_without_id", Envelope{Type: FrameRequest, Method: "ping", Payload: []byte{KindNull}}, 3, wireEnvelopeFields(nil, nil, []byte("ping"), nil, []byte{KindNull}, nil, nil)},
		{"request_without_method", Envelope{Type: FrameRequest, ID: "1", Payload: []byte{KindNull}}, 3, wireEnvelopeFields(nil, []byte("1"), nil, nil, []byte{KindNull}, nil, nil)},
		{"response_without_id", Envelope{Type: FrameResponse, Payload: []byte{KindNull}}, 4, wireEnvelopeFields(nil, nil, nil, nil, []byte{KindNull}, nil, nil)},
		{"error_without_id", Envelope{Type: FrameError, Error: "bad", ErrorCode: "invalid_request"}, 5, wireEnvelopeFields(nil, nil, nil, nil, nil, []byte("bad"), []byte("invalid_request"))},
		{"error_without_code", Envelope{Type: FrameError, ID: "1", Error: "bad"}, 5, wireEnvelopeFields(nil, []byte("1"), nil, nil, nil, []byte("bad"), nil)},
		{"event_without_payload", Envelope{Type: FrameEvent, Payload: []byte{}}, 6, wireEnvelopeFields(nil, nil, nil, nil, nil, nil, nil)},
		{"connection_error_without_code", Envelope{Type: FrameConnectionError, Error: "closed"}, 7, wireEnvelopeFields(nil, nil, nil, nil, nil, []byte("closed"), nil)},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.env.MarshalBinary(); err == nil {
				t.Fatal("marshaled an envelope missing required semantic fields")
			}
			var output Envelope
			if err := output.UnmarshalBinary(wireEnvelopeRaw(1, tc.code, tc.want...)); err == nil {
				t.Fatal("decoded an envelope missing required semantic fields")
			}
		})
	}
}

func TestWireEnvelopeRejectsUnknownFrameCode(t *testing.T) {
	raw := wireEnvelopeRaw(1, 8, wireEnvelopeFields(nil, nil, nil, nil, nil, nil, nil)...)
	var output Envelope
	if err := output.UnmarshalBinary(raw); err == nil {
		t.Fatal("accepted an unknown envelope frame code")
	}
}
