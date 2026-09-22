package wire

import (
	"math"
	"testing"
	"time"
)

type binaryNested struct {
	Name  string `latch:"1"`
	Count int32  `latch:"2"`
}
type binaryMessage struct {
	ID     uint64            `latch:"1"`
	OK     bool              `latch:"2"`
	Text   string            `latch:"3"`
	Ratio  float64           `latch:"4"`
	Nested *binaryNested     `latch:"5"`
	Values []int             `latch:"6"`
	Labels map[string]string `latch:"7"`
	Data   []byte            `latch:"8"`
}

func TestBinaryRoundTripAllKinds(t *testing.T) {
	in := binaryMessage{ID: math.MaxUint64, OK: true, Text: "hello 👋", Ratio: math.SmallestNonzeroFloat64, Nested: &binaryNested{Name: "nested", Count: -19}, Values: []int{-1, 0, 1, 100000}, Labels: map[string]string{"x": "y"}, Data: []byte{0, 1, 255}}
	raw, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	var out binaryMessage
	if err = Decode(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.OK != in.OK || out.Text != in.Text || out.Ratio != in.Ratio || out.Nested.Name != in.Nested.Name || out.Nested.Count != in.Nested.Count || len(out.Values) != 4 || out.Values[0] != -1 || out.Labels["x"] != "y" || string(out.Data) != string(in.Data) {
		t.Fatalf("round trip mismatch: %#v", out)
	}
}
func TestBinaryNulls(t *testing.T) {
	in := binaryMessage{}
	raw, e := Encode(in)
	if e != nil {
		t.Fatal(e)
	}
	var out binaryMessage
	if e = Decode(raw, &out); e != nil {
		t.Fatal(e)
	}
	if out.Nested != nil || out.Values != nil || out.Labels != nil || out.Data != nil {
		t.Fatal("nil values were not preserved")
	}
}
func TestBinaryUnknownFieldsAreSkipped(t *testing.T) {
	raw, _ := Encode(struct {
		A       int      `latch:"1"`
		Unknown []string `latch:"99"`
	}{A: 7, Unknown: []string{"ignored"}})
	var out struct {
		A int `latch:"1"`
	}
	if err := Decode(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.A != 7 {
		t.Fatal(out.A)
	}
}
func TestBinaryPanicsForMissingFieldNumbers(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("missing tag did not panic")
		}
	}()
	_, _ = Encode(struct{ A int }{A: 1})
}

func TestBinaryDecodePanicsForMissingFieldNumbers(t *testing.T) {
	raw, err := Encode(struct {
		A int `latch:"1"`
	}{A: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("missing tag did not panic during decode")
		}
	}()
	var out struct{ A int }
	_ = Decode(raw, &out)
}

func TestBinaryRejectsDuplicateTags(t *testing.T) {
	if _, e := Encode(struct {
		A int `latch:"1"`
		B int `latch:"1"`
	}{A: 1, B: 2}); e == nil {
		t.Fatal("duplicate tag accepted")
	}
	var out struct {
		A int `latch:"1"`
		B int `latch:"1"`
	}
	raw, _ := Encode(struct {
		A int `latch:"1"`
	}{A: 1})
	if e := Decode(raw, &out); e == nil {
		t.Fatal("duplicate tag accepted")
	}
}
func TestBinaryMalformedInputs(t *testing.T) {
	var out int
	cases := [][]byte{{}, {KindString, 0xff}, {KindInt, 0x80}, {KindFloat64, 1, 2}}
	for _, raw := range cases {
		if err := Decode(raw, &out); err == nil {
			t.Fatalf("accepted malformed %x", raw)
		}
	}
}
func TestBinaryTime(t *testing.T) {
	type T struct {
		When time.Time `latch:"1"`
	}
	in := T{When: time.Unix(123, 456).UTC()}
	raw, e := Encode(in)
	if e != nil {
		t.Fatal(e)
	}
	var out T
	if e = Decode(raw, &out); e != nil {
		t.Fatal(e)
	}
	if !out.When.Equal(in.When) {
		t.Fatal(out.When)
	}
}
func TestBinaryEnvelope(t *testing.T) {
	in := Envelope{Type: FrameResponse, ID: "42", Payload: []byte{1, 2, 3}, ErrorCode: "x"}
	raw, e := in.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	var out Envelope
	if e = out.UnmarshalBinary(raw); e != nil {
		t.Fatal(e)
	}
	if out.Type != in.Type || out.ID != in.ID || string(out.Payload) != string(in.Payload) || out.ErrorCode != "x" {
		t.Fatalf("%+v", out)
	}
}
