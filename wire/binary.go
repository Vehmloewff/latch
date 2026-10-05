package wire

// This file implements Latch's compact binary value format. It deliberately
// does not use encoding/json: values carry a small kind byte, structs carry
// numeric field IDs, and every value is self-delimiting so unknown fields can
// be skipped without knowing their Go type.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	KindNull byte = iota
	KindBoolFalse
	KindBoolTrue
	KindInt
	KindUint
	KindFloat32
	KindFloat64
	KindString
	KindBytes
	KindStruct
	KindList
	KindMap
	KindTime
)

const maxDepth = 128
const maxContainer = 1 << 24

// DefaultMaxDecodeBytes is the default per-value allocation-accounting budget.
// Map costs are conservative estimates, not an exact Go heap-memory limit.
const DefaultMaxDecodeBytes = 64 << 20

var (
	ErrMalformed          = errors.New("wire: malformed binary value")
	ErrDecodeLimit        = errors.New("wire: decode allocation limit exceeded")
	ErrMissingFieldNumber = errors.New("wire: exported field must have a non-zero latch field number")
	ErrInvalidFieldNumber = errors.New("wire: invalid latch field number")
)

type encoder struct{ b []byte }

func (e *encoder) byte(v byte) { e.b = append(e.b, v) }
func (e *encoder) u(v uint64) {
	var x [10]byte
	n := binary.PutUvarint(x[:], v)
	e.b = append(e.b, x[:n]...)
}
func (e *encoder) n(v int64)     { e.u(uint64(v<<1) ^ uint64(v>>63)) }
func (e *encoder) blob(v []byte) { e.u(uint64(len(v))); e.b = append(e.b, v...) }

// Encode encodes v using latch numeric tags. It panics if an exported struct
// field has no latch field number. The returned buffer is owned by the caller
// and may be reused after the call.
func Encode(v any) ([]byte, error) {
	var e encoder
	if err := e.value(reflect.ValueOf(v), 0); err != nil {
		return nil, err
	}
	return e.b, nil
}
func (e *encoder) value(v reflect.Value, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("wire: maximum nesting depth exceeded")
	}
	if !v.IsValid() {
		e.byte(KindNull)
		return nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			e.byte(KindNull)
			return nil
		}
		return e.value(v.Elem(), depth)
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			e.byte(KindNull)
			return nil
		}
		return e.value(v.Elem(), depth)
	}
	if v.Type() == reflect.TypeOf(time.Time{}) {
		e.byte(KindTime)
		e.n(v.Interface().(time.Time).UnixNano())
		return nil
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			e.byte(KindBoolTrue)
		} else {
			e.byte(KindBoolFalse)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.byte(KindInt)
		e.n(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.byte(KindUint)
		e.u(v.Uint())
	case reflect.Float32:
		e.byte(KindFloat32)
		var x [4]byte
		binary.LittleEndian.PutUint32(x[:], math.Float32bits(float32(v.Float())))
		e.b = append(e.b, x[:]...)
	case reflect.Float64:
		e.byte(KindFloat64)
		var x [8]byte
		binary.LittleEndian.PutUint64(x[:], math.Float64bits(v.Float()))
		e.b = append(e.b, x[:]...)
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return fmt.Errorf("wire: invalid UTF-8 string")
		}
		e.byte(KindString)
		e.blob([]byte(v.String()))
	case reflect.Slice:
		if v.IsNil() {
			e.byte(KindNull)
			break
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			e.byte(KindBytes)
			e.blob(v.Bytes())
			break
		}
		e.byte(KindList)
		e.u(uint64(v.Len()))
		for i := 0; i < v.Len(); i++ {
			if err := e.value(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Array:
		e.byte(KindList)
		e.u(uint64(v.Len()))
		for i := 0; i < v.Len(); i++ {
			if err := e.value(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if v.IsNil() {
			e.byte(KindNull)
			break
		}
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("wire: map key %s is not string", v.Type().Key())
		}
		e.byte(KindMap)
		e.u(uint64(v.Len()))
		iter := v.MapRange()
		for iter.Next() {
			if !utf8.ValidString(iter.Key().String()) {
				return fmt.Errorf("wire: invalid UTF-8 map key")
			}
			e.byte(KindString)
			e.blob([]byte(iter.Key().String()))
			if err := e.value(iter.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		e.byte(KindStruct)
		var fields []reflect.StructField
		for i := 0; i < v.NumField(); i++ {
			sf := v.Type().Field(i)
			if sf.PkgPath != "" {
				continue
			}
			if strings.Contains(sf.Tag.Get("latch"), ",omitempty") && v.Field(i).IsZero() {
				continue
			}
			fields = append(fields, sf)
		}
		e.u(uint64(len(fields)))
		seen := make(map[uint64]struct{}, len(fields))
		for _, sf := range fields {
			n, err := fieldNumber(sf)
			if err != nil {
				if errors.Is(err, ErrMissingFieldNumber) {
					panicMissingFieldNumber(sf)
				}
				return fmt.Errorf("wire: %s.%s: %w", v.Type(), sf.Name, err)
			}
			if _, exists := seen[n]; exists {
				return fmt.Errorf("wire: duplicate field number %d", n)
			}
			seen[n] = struct{}{}
			e.u(uint64(n))
			if err := e.value(v.FieldByIndex(sf.Index), depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("wire: unsupported Go kind %s", v.Kind())
	}
	return nil
}

func fieldNumber(sf reflect.StructField) (uint64, error) {
	t, ok := sf.Tag.Lookup("latch")
	if !ok {
		return 0, ErrMissingFieldNumber
	}
	t = strings.Split(t, ",")[0]
	n, err := strconv.ParseUint(t, 10, 32)
	if err != nil || n == 0 {
		return 0, ErrInvalidFieldNumber
	}
	return n, nil
}

func panicMissingFieldNumber(sf reflect.StructField) {
	panic(fmt.Errorf("wire: %s.%s: %w", sf.Type, sf.Name, ErrMissingFieldNumber))
}

type decoder struct {
	b         []byte
	pos       int
	remaining uint64
}

// reserve charges allocations before they happen, including old slice backing
// arrays retained during growth. Small allocations include rounding overhead.
func (d *decoder) reserve(n, size uint64) error {
	if size != 0 && n > math.MaxUint64/size {
		return ErrDecodeLimit
	}
	cost := n * size
	if cost < 64 {
		cost = 64
	}
	if cost > d.remaining {
		return ErrDecodeLimit
	}
	d.remaining -= cost
	return nil
}

// A value needs at least a kind byte; a struct entry also needs a field ID,
// and a map entry needs a string kind and a length before its value.
func (d *decoder) count(minBytes uint64) (uint64, error) {
	n, err := d.u()
	if err != nil || n > maxContainer || n > uint64(len(d.b)-d.pos)/minBytes {
		return 0, ErrMalformed
	}
	return n, nil
}

// Account for map growth, spare slots, and runtime metadata without relying on
// a particular Go map implementation. Maps never receive an untrusted hint.
func (d *decoder) mapEntry(keySize, valueSize uintptr) error {
	return d.reserve(2, uint64(keySize)+uint64(valueSize)+32)
}

func (d *decoder) fieldSeen(seen map[uint64]struct{}, id uint64) error {
	if _, exists := seen[id]; exists {
		return fmt.Errorf("wire: duplicate incoming field number %d", id)
	}
	if err := d.mapEntry(8, 0); err != nil {
		return err
	}
	seen[id] = struct{}{}
	return nil
}

func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || n > len(d.b)-d.pos {
		return nil, ErrMalformed
	}
	x := d.b[d.pos : d.pos+n]
	d.pos += n
	return x, nil
}
func (d *decoder) byte() (byte, error) {
	x, e := d.take(1)
	if e != nil {
		return 0, e
	}
	return x[0], nil
}
func (d *decoder) u() (uint64, error) {
	if d.pos < 0 || d.pos > len(d.b) {
		return 0, ErrMalformed
	}
	x, n := binary.Uvarint(d.b[d.pos:])
	if n <= 0 {
		return 0, ErrMalformed
	}
	var encoded [binary.MaxVarintLen64]byte
	if n != binary.PutUvarint(encoded[:], x) {
		return 0, ErrMalformed
	}
	d.pos += n
	return x, nil
}
func (d *decoder) n() (int64, error) {
	u, e := d.u()
	if e != nil {
		return 0, e
	}
	return int64(u>>1) ^ -int64(u&1), nil
}
func (d *decoder) blob() ([]byte, error) {
	n, e := d.u()
	if e != nil || n > uint64(len(d.b)-d.pos) || n > maxContainer {
		return nil, ErrMalformed
	}
	return d.take(int(n))
}

func (d *decoder) stringBlob() ([]byte, error) {
	x, err := d.blob()
	if err != nil || !utf8.Valid(x) {
		return nil, ErrMalformed
	}
	return x, nil
}

// Decode decodes exactly one value into dst, which must be a non-nil pointer.
// It panics if an exported struct field has no latch field number. Byte slices
// retain the input backing array; strings are converted to Go strings. No
// intermediate JSON tree is allocated. Allocations are accounted against
// DefaultMaxDecodeBytes; use DecodeWithLimit to select a different budget.
func Decode(data []byte, dst any) error {
	return DecodeWithLimit(data, dst, DefaultMaxDecodeBytes)
}

// DecodeWithLimit decodes exactly one value with a positive aggregate allocation
// budget. It includes nested values, copied strings, container growth, and
// duplicate tracking. Map costs are estimated conservatively; this is not an
// exact heap-memory ceiling. The input and caller-owned destination are not
// charged. On error, dst may be partially modified, as with Decode.
func DecodeWithLimit(data []byte, dst any, maxAllocationBytes int64) error {
	if maxAllocationBytes <= 0 {
		return errors.New("wire: decode allocation limit must be positive")
	}
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.New("wire: destination must be a non-nil pointer")
	}
	d := decoder{b: data, remaining: uint64(maxAllocationBytes)}
	if err := d.value(v.Elem(), 0); err != nil {
		return err
	}
	if d.pos != len(data) {
		return ErrMalformed
	}
	return nil
}
func (d *decoder) value(out reflect.Value, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("wire: maximum nesting depth exceeded")
	}
	k, err := d.byte()
	if err != nil {
		return err
	}
	if k == KindNull {
		if out.Kind() == reflect.Pointer || out.Kind() == reflect.Interface || out.Kind() == reflect.Slice || out.Kind() == reflect.Map {
			out.Set(reflect.Zero(out.Type()))
			return nil
		}
		return fmt.Errorf("wire: null for %s", out.Type())
	}
	if out.Kind() == reflect.Pointer {
		if out.IsNil() {
			if err := d.reserve(1, uint64(out.Type().Elem().Size())); err != nil {
				return err
			}
			out.Set(reflect.New(out.Type().Elem()))
		}
		return d.decodeKnown(k, out.Elem(), depth)
	}
	return d.decodeKnown(k, out, depth)
}
func (d *decoder) decodeKnown(k byte, o reflect.Value, depth int) error {
	if o.Kind() == reflect.Interface {
		return d.decodeInterface(k, o, depth)
	}
	switch k {
	case KindBoolFalse, KindBoolTrue:
		if o.Kind() != reflect.Bool {
			return fmt.Errorf("wire: bool for %s", o.Type())
		}
		o.SetBool(k == KindBoolTrue)
	case KindInt:
		u, e := d.n()
		if e != nil {
			return e
		}
		if o.Kind() < reflect.Int || o.Kind() > reflect.Int64 || o.OverflowInt(u) {
			return fmt.Errorf("wire: integer out of range for %s", o.Type())
		}
		o.SetInt(u)
	case KindUint:
		u, e := d.u()
		if e != nil {
			return e
		}
		if !isUnsignedKind(o.Kind()) || o.OverflowUint(u) {
			return fmt.Errorf("wire: unsigned integer out of range for %s", o.Type())
		}
		o.SetUint(u)
	case KindFloat32:
		x, e := d.take(4)
		if e != nil || o.Kind() != reflect.Float32 {
			return ErrMalformed
		}
		o.SetFloat(float64(math.Float32frombits(binary.LittleEndian.Uint32(x))))
	case KindFloat64:
		x, e := d.take(8)
		if e != nil || o.Kind() != reflect.Float64 {
			return ErrMalformed
		}
		o.SetFloat(math.Float64frombits(binary.LittleEndian.Uint64(x)))
	case KindString:
		x, e := d.stringBlob()
		if e != nil || o.Kind() != reflect.String {
			return ErrMalformed
		}
		if err := d.reserve(uint64(len(x)), 1); err != nil {
			return err
		}
		o.SetString(string(x))
	case KindBytes:
		x, e := d.blob()
		if e != nil || o.Kind() != reflect.Slice || o.Type().Elem().Kind() != reflect.Uint8 {
			return ErrMalformed
		}
		o.SetBytes(x)
	case KindTime:
		u, e := d.n()
		if e != nil || o.Type() != reflect.TypeOf(time.Time{}) {
			return ErrMalformed
		}
		o.Set(reflect.ValueOf(time.Unix(0, u).UTC()))
	case KindList:
		return d.list(o, depth)
	case KindMap:
		return d.dict(o, depth)
	case KindStruct:
		return d.structValue(o, depth)
	default:
		return ErrMalformed
	}
	return nil
}

func isUnsignedKind(k reflect.Kind) bool {
	return k == reflect.Uint || k == reflect.Uint8 || k == reflect.Uint16 || k == reflect.Uint32 || k == reflect.Uint64 || k == reflect.Uintptr
}

func setInterfaceValue(out reflect.Value, value any) error {
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		out.Set(reflect.Zero(out.Type()))
		return nil
	}
	if !v.Type().AssignableTo(out.Type()) {
		return fmt.Errorf("wire: decoded %s does not implement %s", v.Type(), out.Type())
	}
	out.Set(v)
	return nil
}

func (d *decoder) decodeInterface(k byte, out reflect.Value, depth int) error {
	// Scalar boxes and container descriptors are all smaller than this charge.
	if err := d.reserve(1, 64); err != nil {
		return err
	}
	var value any
	switch k {
	case KindBoolFalse, KindBoolTrue:
		value = k == KindBoolTrue
	case KindInt:
		v, err := d.n()
		if err != nil {
			return err
		}
		value = v
	case KindUint:
		v, err := d.u()
		if err != nil {
			return err
		}
		value = v
	case KindFloat32:
		x, err := d.take(4)
		if err != nil {
			return err
		}
		value = math.Float32frombits(binary.LittleEndian.Uint32(x))
	case KindFloat64:
		x, err := d.take(8)
		if err != nil {
			return err
		}
		value = math.Float64frombits(binary.LittleEndian.Uint64(x))
	case KindString:
		x, err := d.stringBlob()
		if err != nil {
			return err
		}
		if err := d.reserve(uint64(len(x)), 1); err != nil {
			return err
		}
		value = string(x)
	case KindBytes:
		x, err := d.blob()
		if err != nil {
			return err
		}
		value = x
	case KindTime:
		n, err := d.n()
		if err != nil {
			return err
		}
		value = time.Unix(0, n).UTC()
	case KindList:
		var values []any
		if err := d.list(reflect.ValueOf(&values).Elem(), depth); err != nil {
			return err
		}
		value = values
	case KindMap:
		var values map[string]any
		if err := d.dict(reflect.ValueOf(&values).Elem(), depth); err != nil {
			return err
		}
		value = values
	case KindStruct:
		n, err := d.count(2)
		if err != nil {
			return err
		}
		if err := d.reserve(1, 128); err != nil {
			return err
		}
		values := make(map[uint64]any)
		for i := uint64(0); i < n; i++ {
			id, err := d.u()
			if err != nil || id == 0 || id > math.MaxUint32 {
				return ErrMalformed
			}
			if _, exists := values[id]; exists {
				return fmt.Errorf("wire: duplicate incoming field number %d", id)
			}
			if err := d.mapEntry(8, reflect.TypeOf((*any)(nil)).Elem().Size()); err != nil {
				return err
			}
			if err := d.reserve(1, uint64(reflect.TypeOf((*any)(nil)).Elem().Size())); err != nil {
				return err
			}
			var item any
			if err := d.value(reflect.ValueOf(&item).Elem(), depth+1); err != nil {
				return err
			}
			values[id] = item
		}
		value = values
	default:
		return ErrMalformed
	}
	return setInterfaceValue(out, value)
}

func (d *decoder) list(o reflect.Value, depth int) error {
	n, err := d.count(1)
	if err != nil {
		return err
	}
	if o.Kind() != reflect.Slice && o.Kind() != reflect.Array {
		return ErrMalformed
	}
	if o.Kind() == reflect.Array && int(n) != o.Len() {
		return ErrMalformed
	}
	if o.Kind() == reflect.Slice {
		if err := d.reserve(1, 24); err != nil {
			return err
		}
		o.Set(reflect.MakeSlice(o.Type(), 0, 0))
	}
	for i := 0; i < int(n); i++ {
		if o.Kind() == reflect.Slice {
			if i == o.Cap() {
				capacity := min(max(1, o.Cap()*2), int(n))
				if err := d.reserve(uint64(capacity), uint64(o.Type().Elem().Size())); err != nil {
					return err
				}
				if err := d.reserve(1, 24); err != nil {
					return err
				}
				grown := reflect.MakeSlice(o.Type(), i, capacity)
				reflect.Copy(grown, o)
				o.Set(grown)
			}
			o.SetLen(i + 1)
		}
		if err := d.value(o.Index(i), depth+1); err != nil {
			return err
		}
	}
	return nil
}
func (d *decoder) dict(o reflect.Value, depth int) error {
	n, e := d.count(3)
	if e != nil {
		return e
	}
	if o.Kind() != reflect.Map || o.Type().Key().Kind() != reflect.String {
		return ErrMalformed
	}
	if err := d.reserve(1, 128); err != nil {
		return err
	}
	o.Set(reflect.MakeMap(o.Type()))
	for i := uint64(0); i < n; i++ {
		k, e := d.byte()
		if e != nil || k != KindString {
			return ErrMalformed
		}
		x, e := d.stringBlob()
		if e != nil {
			return e
		}
		if err := d.reserve(uint64(len(x)), 1); err != nil {
			return err
		}
		key := string(x)
		keyValue := reflect.ValueOf(key).Convert(o.Type().Key())
		if o.MapIndex(keyValue).IsValid() {
			return errors.New("wire: duplicate map key")
		}
		if err := d.mapEntry(o.Type().Key().Size(), o.Type().Elem().Size()); err != nil {
			return err
		}
		if err := d.reserve(1, uint64(o.Type().Elem().Size())); err != nil {
			return err
		}
		v := reflect.New(o.Type().Elem()).Elem()
		if e = d.value(v, depth+1); e != nil {
			return e
		}
		o.SetMapIndex(keyValue, v)
	}
	return nil
}
func (d *decoder) structValue(o reflect.Value, depth int) error {
	n, e := d.count(2)
	if e != nil {
		return e
	}
	if o.Kind() != reflect.Struct {
		return ErrMalformed
	}
	if err := d.reserve(1, 256); err != nil {
		return err
	}
	by := map[uint64]int{}
	for i := 0; i < o.NumField(); i++ {
		sf := o.Type().Field(i)
		if sf.PkgPath != "" {
			continue
		}
		id, e := fieldNumber(sf)
		if e != nil {
			if errors.Is(e, ErrMissingFieldNumber) {
				panicMissingFieldNumber(sf)
			}
			return fmt.Errorf("wire: %s.%s: %w", o.Type(), sf.Name, e)
		}
		if _, ok := by[id]; ok {
			return fmt.Errorf("wire: duplicate field number %d", id)
		}
		if err := d.mapEntry(8, 8); err != nil {
			return err
		}
		by[id] = i
	}
	seen := make(map[uint64]struct{})
	for i := uint64(0); i < n; i++ {
		id, e := d.u()
		if e != nil || id == 0 || id > math.MaxUint32 {
			return ErrMalformed
		}
		if err := d.fieldSeen(seen, id); err != nil {
			return err
		}
		idx, ok := by[id]
		if !ok {
			if e := d.skip(depth + 1); e != nil {
				return e
			}
			continue
		}
		if e := d.value(o.Field(idx), depth+1); e != nil {
			return e
		}
	}
	return nil
}
func (d *decoder) skip(depth int) error {
	if depth > maxDepth {
		return ErrMalformed
	}
	k, e := d.byte()
	if e != nil {
		return e
	}
	switch k {
	case KindNull, KindBoolFalse, KindBoolTrue:
		return nil
	case KindInt, KindUint, KindTime:
		_, e = d.u()
		return e
	case KindFloat32:
		_, e = d.take(4)
		return e
	case KindFloat64:
		_, e = d.take(8)
		return e
	case KindString:
		_, e = d.stringBlob()
		return e
	case KindBytes:
		_, e = d.blob()
		return e
	case KindList, KindMap:
		minBytes := uint64(1)
		if k == KindMap {
			minBytes = 3
		}
		n, e := d.count(minBytes)
		if e != nil {
			return e
		}
		var seen map[string]struct{}
		if k == KindMap {
			if err := d.reserve(1, 128); err != nil {
				return err
			}
			seen = make(map[string]struct{})
		}
		for i := uint64(0); i < n; i++ {
			if k == KindMap {
				t, e := d.byte()
				if e != nil || t != KindString {
					return ErrMalformed
				}
				key, e := d.stringBlob()
				if e != nil {
					return e
				}
				if err := d.reserve(uint64(len(key)), 1); err != nil {
					return err
				}
				keyString := string(key)
				if _, exists := seen[keyString]; exists {
					return errors.New("wire: duplicate map key")
				}
				if err := d.mapEntry(reflect.TypeOf(keyString).Size(), 0); err != nil {
					return err
				}
				seen[keyString] = struct{}{}
			}
			if e = d.skip(depth + 1); e != nil {
				return e
			}
		}
		return nil
	case KindStruct:
		n, e := d.count(2)
		if e != nil {
			return e
		}
		if err := d.reserve(1, 128); err != nil {
			return err
		}
		seen := make(map[uint64]struct{})
		for i := uint64(0); i < n; i++ {
			id, e := d.u()
			if e != nil || id == 0 || id > math.MaxUint32 {
				return ErrMalformed
			}
			if err := d.fieldSeen(seen, id); err != nil {
				return err
			}
			if e = d.skip(depth + 1); e != nil {
				return e
			}
		}
		return nil
	}
	return ErrMalformed
}
