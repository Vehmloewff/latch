package wire

import (
	"bufio"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestCrossLanguageVectors(t *testing.T) {
	f, err := os.Open("testdata/cross_language_vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]string{
		"int_minus_one": "0301", "int_64": "038001", "uint_300": "04ac02",
		"float32_one": "050000803f", "float64_one": "06000000000000f03f",
		"string_hello": "070568656c6c6f", "bytes_binary": "080300ff7f",
		"struct_nested":     "090401070568656c6c6f020325030a03000206000000000000f83f0408030102ff",
		"envelope_response": "010400023432000003010203000178",
	}
	s := bufio.NewScanner(f)
	seen := map[string]bool{}
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		name := parts[0]
		raw, err := hex.DecodeString(strings.Join(parts[1:], ""))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if hex.EncodeToString(raw) != want[name] {
			t.Fatalf("%s fixture changed", name)
		}
		seen[name] = true
		switch name {
		case "envelope_response":
			var env Envelope
			if err := env.UnmarshalBinary(raw); err != nil {
				t.Fatal(err)
			}
			got, err := env.MarshalBinary()
			if err != nil || string(got) != string(raw) {
				t.Fatalf("%s round trip", name)
			}
		default:
			switch name {
			case "int_minus_one":
				var v int
				if err := Decode(raw, &v); err != nil || v != -1 {
					t.Fatalf("%s decode: %v %v", name, v, err)
				}
			case "int_64":
				var v int
				if err := Decode(raw, &v); err != nil || v != 64 {
					t.Fatalf("%s decode: %v %v", name, v, err)
				}
			case "uint_300":
				var v uint
				if err := Decode(raw, &v); err != nil || v != 300 {
					t.Fatalf("%s decode: %v %v", name, v, err)
				}
			case "float32_one":
				var v float32
				if err := Decode(raw, &v); err != nil || v != 1 {
					t.Fatalf("%s decode: %v %v", name, v, err)
				}
			case "float64_one":
				var v float64
				if err := Decode(raw, &v); err != nil || v != 1 {
					t.Fatalf("%s decode: %v %v", name, v, err)
				}
			case "string_hello":
				var v string
				if err := Decode(raw, &v); err != nil || v != "hello" {
					t.Fatalf("%s decode: %q %v", name, v, err)
				}
			case "bytes_binary":
				var v []byte
				if err := Decode(raw, &v); err != nil || string(v) != string([]byte{0, 255, 127}) {
					t.Fatalf("%s decode: %v %v", name, v, err)
				}
			case "struct_nested":
				var v struct {
					A string `latch:"1"`
					B int    `latch:"2"`
					D []byte `latch:"4"`
				}
				if err := Decode(raw, &v); err != nil || v.A != "hello" || v.B != -19 || len(v.D) != 3 {
					t.Fatalf("%s decode: %+v %v", name, v, err)
				}
			}
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("missing vector %s", name)
		}
	}
}
