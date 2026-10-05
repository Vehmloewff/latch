package wire

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"runtime"
	"strconv"
	"testing"
)

func decodeHeader(kind byte, count uint64) []byte {
	return wireAppendUvarint([]byte{kind}, count)
}

func unknownDecodeValue(value []byte) []byte {
	return append([]byte{KindStruct, 1, 99}, value...)
}

// Error-only tests miss this vulnerability: the old decoder also rejected the
// payload, but only after allocating megabytes from its count header.
func requireSmallDecodeAllocation(t *testing.T, decode func() error, want error) {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := decode()
	runtime.ReadMemStats(&after)
	if !errors.Is(err, want) {
		t.Fatalf("decode error = %v, want %v", err, want)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 128<<10 {
		t.Fatalf("decode allocated %d bytes, want at most 128 KiB", allocated)
	}
}

func TestDecodeRejectsHostileCountsBeforeAllocation(t *testing.T) {
	for _, count := range []uint64{1 << 16, maxContainer, maxContainer + 1} {
		for _, tc := range []struct {
			name string
			kind byte
			dst  func() any
			skip bool
		}{
			{"typed list", KindList, func() any { return new([]int64) }, false},
			{"typed map", KindMap, func() any { return new(map[string]int64) }, false},
			{"typed struct", KindStruct, func() any { return new(struct{}) }, false},
			{"interface list", KindList, func() any { return new(any) }, false},
			{"interface map", KindMap, func() any { return new(any) }, false},
			{"interface struct", KindStruct, func() any { return new(any) }, false},
			{"skipped list", KindList, func() any { return new(struct{}) }, true},
			{"skipped map", KindMap, func() any { return new(struct{}) }, true},
			{"skipped struct", KindStruct, func() any { return new(struct{}) }, true},
		} {
			t.Run(tc.name+"/"+stringCount(count), func(t *testing.T) {
				raw := decodeHeader(tc.kind, count)
				if tc.skip {
					raw = unknownDecodeValue(raw)
				}
				dst := tc.dst()
				requireSmallDecodeAllocation(t, func() error { return Decode(raw, dst) }, ErrMalformed)
			})
		}
	}
}

func stringCount(n uint64) string {
	// Keep test labels independent of the binary count representation.
	return strconv.FormatUint(n, 10)
}

func TestDecodePaddedMalformedContainersGrowIncrementally(t *testing.T) {
	const count = 1 << 16
	for _, kind := range []byte{KindList, KindMap, KindStruct} {
		minBytes := map[byte]int{KindList: 1, KindMap: 3, KindStruct: 2}[kind]
		raw := append(decodeHeader(kind, count), bytes.Repeat([]byte{0xff}, count*minBytes)...)
		for _, mode := range []string{"typed", "interface", "skipped"} {
			t.Run(mode+"/"+stringCount(uint64(kind)), func(t *testing.T) {
				data := raw
				var dst any
				switch mode {
				case "interface":
					dst = new(any)
				case "skipped":
					data = unknownDecodeValue(raw)
					dst = new(struct{})
				default:
					switch kind {
					case KindList:
						dst = new([]int64)
					case KindMap:
						dst = new(map[string]int64)
					case KindStruct:
						dst = new(struct{})
					}
				}
				requireSmallDecodeAllocation(t, func() error { return Decode(data, dst) }, ErrMalformed)
			})
		}
	}
}

func TestDecodeDefaultBudgetRejectsLargeElementBeforeAllocation(t *testing.T) {
	var out []struct {
		padding [DefaultMaxDecodeBytes + 1]byte
	}
	raw := []byte{KindList, 1, KindStruct, 0}
	requireSmallDecodeAllocation(t, func() error { return Decode(raw, &out) }, ErrDecodeLimit)
	if len(out) != 0 || cap(out) != 0 {
		t.Fatalf("oversized backing array allocated: len=%d cap=%d", len(out), cap(out))
	}
}

func TestDecodeBudgetIsAggregateAcrossNestedContainers(t *testing.T) {
	item := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	single, err := Encode(item)
	if err != nil {
		t.Fatal(err)
	}
	var one []int64
	if err := DecodeWithLimit(single, &one, 2048); err != nil {
		t.Fatalf("individual list exceeds budget: %v", err)
	}
	input := make([][]int64, 20)
	for i := range input {
		input[i] = item
	}
	raw, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]int64
	if err := DecodeWithLimit(raw, &out, 2048); !errors.Is(err, ErrDecodeLimit) {
		t.Fatalf("nested lists error = %v, want ErrDecodeLimit", err)
	}
	if err := DecodeWithLimit(raw, &out, 1<<20); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, out) {
		t.Fatalf("nested list mismatch: %v", out)
	}
}

func TestDecodeBudgetCoversAllocatingPaths(t *testing.T) {
	pointer := "pointer"
	for _, tc := range []struct {
		name  string
		value any
		dst   func() any
		skip  bool
	}{
		{"string", "copied string", func() any { return new(string) }, false},
		{"pointer", &pointer, func() any { return new(*string) }, false},
		{"list", []string{"one", "two", "three"}, func() any { return new([]string) }, false},
		{"map", map[string]string{"a": "one", "b": "two"}, func() any { return new(map[string]string) }, false},
		{"struct", struct {
			A string `latch:"1"`
		}{"one"}, func() any {
			return new(struct {
				A string `latch:"1"`
			})
		}, false},
		{"interface list", []string{"one", "two"}, func() any { return new(any) }, false},
		{"interface map", map[string]string{"a": "one"}, func() any { return new(any) }, false},
		{"interface struct", struct {
			A string `latch:"1"`
		}{"one"}, func() any { return new(any) }, false},
		{"skipped map", map[string]string{"a": "one"}, func() any { return new(struct{}) }, true},
		{"skipped struct", struct {
			A string `latch:"1"`
		}{"one"}, func() any { return new(struct{}) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Encode(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if tc.skip {
				raw = unknownDecodeValue(raw)
			}
			if err := DecodeWithLimit(raw, tc.dst(), 1); !errors.Is(err, ErrDecodeLimit) {
				t.Fatalf("small budget error = %v, want ErrDecodeLimit", err)
			}
			if err := DecodeWithLimit(raw, tc.dst(), 1<<20); err != nil {
				t.Fatalf("larger budget error = %v", err)
			}
		})
	}
}

func TestDecodeBudgetRejectsLargeMapValuesAndPointeesBeforeAllocation(t *testing.T) {
	type large struct {
		padding [1 << 20]byte
	}
	t.Run("map value", func(t *testing.T) {
		var out map[string]large
		raw := []byte{KindMap, 1, KindString, 0, KindStruct, 0}
		requireSmallDecodeAllocation(t, func() error { return DecodeWithLimit(raw, &out, 512) }, ErrDecodeLimit)
		if len(out) != 0 {
			t.Fatal("oversized map value inserted")
		}
	})
	t.Run("nil pointee", func(t *testing.T) {
		var out *large
		requireSmallDecodeAllocation(t, func() error { return DecodeWithLimit([]byte{KindStruct, 0}, &out, 512) }, ErrDecodeLimit)
		if out != nil {
			t.Fatal("oversized pointee allocated")
		}
	})
	t.Run("map key", func(t *testing.T) {
		raw := []byte{KindMap, 1, KindString}
		raw = wireAppendUvarint(raw, 1<<20)
		raw = append(raw, bytes.Repeat([]byte{'x'}, 1<<20)...)
		raw = append(raw, KindNull)
		var out map[string]any
		requireSmallDecodeAllocation(t, func() error { return DecodeWithLimit(raw, &out, 512) }, ErrDecodeLimit)
	})
}

func TestDecodeAggregateStringsAndMapEntries(t *testing.T) {
	strings := make([]string, 16)
	values := make(map[string]any)
	structRaw := decodeHeader(KindStruct, 16)
	for i := range strings {
		strings[i] = string(bytes.Repeat([]byte{'x'}, 1024))
		values[stringCount(uint64(i))] = nil
		structRaw = wireAppendUvarint(structRaw, uint64(i+1))
		structRaw = append(structRaw, KindNull)
	}
	stringRaw, err := Encode(strings)
	if err != nil {
		t.Fatal(err)
	}
	mapRaw, err := Encode(values)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		raw   []byte
		dst   any
		limit int64
	}{
		{"copied strings", stringRaw, new([]string), 8 << 10},
		// These budgets allow key copies and temporary values, but not all
		// map storage, so removing map-entry accounting would fail the tests.
		{"typed map entries", mapRaw, new(map[string]any), 3000},
		{"interface map entries", mapRaw, new(any), 3000},
		{"interface struct entries", structRaw, new(any), 1500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := DecodeWithLimit(tc.raw, tc.dst, tc.limit); !errors.Is(err, ErrDecodeLimit) {
				t.Fatalf("aggregate allocation error = %v, want ErrDecodeLimit", err)
			}
			if err := DecodeWithLimit(tc.raw, tc.dst, 1<<20); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDecodeDuplicateKeyErrorsDoNotExpandInput(t *testing.T) {
	const keyBytes = 1 << 20
	raw := decodeHeader(KindMap, 2)
	for i := 0; i < 2; i++ {
		raw = append(raw, KindString)
		raw = wireAppendUvarint(raw, keyBytes)
		raw = append(raw, make([]byte, keyBytes)...)
		raw = append(raw, KindNull)
	}
	for _, mode := range []string{"typed", "interface", "skipped"} {
		t.Run(mode, func(t *testing.T) {
			data := raw
			var dst any = new(map[string]any)
			if mode == "interface" {
				dst = new(any)
			} else if mode == "skipped" {
				dst = new(struct{})
				data = unknownDecodeValue(raw)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			err := Decode(data, dst)
			runtime.ReadMemStats(&after)
			if err == nil || err.Error() != "wire: duplicate map key" {
				t.Fatalf("duplicate-key error = %v", err)
			}
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 3*keyBytes {
				t.Fatalf("duplicate-key decode allocated %d bytes; error must not quote the key", allocated)
			}
		})
	}
}

func TestSkipListsDoNotAllocateTrackingMaps(t *testing.T) {
	const count = 1 << 16
	raw := append(decodeHeader(KindList, count), make([]byte, count)...)
	d := decoder{b: raw} // No allocation budget: lists of nulls need none.
	if allocations := testing.AllocsPerRun(10, func() {
		d.pos = 0
		if err := d.skip(0); err != nil {
			t.Fatalf("skip: %v", err)
		}
	}); allocations != 0 {
		t.Fatalf("skipping a list allocated %v times", allocations)
	}
}

func TestDecodeTrackingMapsShareBudget(t *testing.T) {
	for _, kind := range []byte{KindMap, KindStruct} {
		raw := decodeHeader(kind, 100)
		for i := uint64(1); i <= 100; i++ {
			if kind == KindStruct {
				raw = wireAppendUvarint(raw, i)
			} else {
				raw = append(raw, KindString, 1, byte(i))
			}
			raw = append(raw, KindNull)
		}
		t.Run(stringCount(uint64(kind)), func(t *testing.T) {
			d := decoder{b: raw, remaining: 1024}
			if err := d.skip(0); !errors.Is(err, ErrDecodeLimit) {
				t.Fatalf("tracking map error = %v, want ErrDecodeLimit", err)
			}
			d = decoder{b: raw, remaining: 1 << 20}
			if err := d.skip(0); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDecodeBudgetOverflowAndInvalidLimits(t *testing.T) {
	d := decoder{remaining: math.MaxUint64}
	if err := d.reserve(math.MaxUint64, 2); !errors.Is(err, ErrDecodeLimit) {
		t.Fatalf("overflow error = %v", err)
	}
	if d.remaining != math.MaxUint64 {
		t.Fatal("failed reservation changed budget")
	}
	for _, limit := range []int64{0, -1} {
		var out bool
		if err := DecodeWithLimit([]byte{KindBoolTrue}, &out, limit); err == nil {
			t.Fatalf("accepted invalid limit %d", limit)
		}
	}
}

func TestDecodeMinimumSizeValidContainers(t *testing.T) {
	for _, tc := range []struct {
		raw []byte
		dst any
	}{
		{[]byte{KindList, 0}, new([]any)},
		{[]byte{KindList, 1, KindNull}, new([]any)},
		{[]byte{KindMap, 0}, new(map[string]any)},
		{[]byte{KindMap, 1, KindString, 0, KindNull}, new(map[string]any)},
		{[]byte{KindStruct, 0}, new(struct{})},
		{[]byte{KindStruct, 1, 1, KindNull}, new(struct{})},
	} {
		if err := Decode(tc.raw, tc.dst); err != nil {
			t.Fatalf("rejected minimum-size valid container %x: %v", tc.raw, err)
		}
		if reflect.ValueOf(tc.dst).Elem().Kind() == reflect.Slice && reflect.ValueOf(tc.dst).Elem().IsNil() {
			t.Fatal("empty list decoded as nil")
		}
		if reflect.ValueOf(tc.dst).Elem().Kind() == reflect.Map && reflect.ValueOf(tc.dst).Elem().IsNil() {
			t.Fatal("empty map decoded as nil")
		}
	}
}

func FuzzDecodeContainerCounts(f *testing.F) {
	for _, kind := range []byte{KindList, KindMap, KindStruct} {
		f.Add(decodeHeader(kind, maxContainer))
		f.Add([]byte{kind, 0})
		f.Add([]byte{kind, 1, KindNull})
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 4096 {
			return
		}
		var out any
		_ = DecodeWithLimit(raw, &out, 16<<10)
		var unknown struct{}
		_ = DecodeWithLimit(unknownDecodeValue(raw), &unknown, 16<<10)
	})
}
