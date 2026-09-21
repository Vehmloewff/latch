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

var (
	ErrMalformed          = errors.New("wire: malformed binary value")
	ErrMissingFieldNumber = errors.New("wire: exported field must have a non-zero latch field number")
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

// Encode encodes v using latch numeric tags. The returned buffer is owned by
// the caller and may be reused after the call.
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
			if sf.PkgPath != "" || sf.Tag.Get("json") == "-" {
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
	t := sf.Tag.Get("latch")
	if t == "" {
		return 0, ErrMissingFieldNumber
	}
	t = strings.Split(t, ",")[0]
	n, err := strconv.ParseUint(t, 10, 32)
	if err != nil || n == 0 {
		return 0, ErrMissingFieldNumber
	}
	return n, nil
}

type decoder struct {
	b   []byte
	pos int
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
	x, n := binary.Uvarint(d.b[d.pos:])
	if n <= 0 {
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

// Decode decodes exactly one value into dst, which must be a non-nil pointer.
// Byte slices retain the input backing array; strings are converted to Go
// strings. No intermediate JSON tree is allocated.
func Decode(data []byte, dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.New("wire: destination must be a non-nil pointer")
	}
	d := decoder{b: data}
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
			out.Set(reflect.New(out.Type().Elem()))
		}
		return d.decodeKnown(k, out.Elem(), depth)
	}
	return d.decodeKnown(k, out, depth)
}
func (d *decoder) decodeKnown(k byte, o reflect.Value, depth int) error {
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
		if o.Kind() < reflect.Uint || o.Kind() > reflect.Uint64 || o.OverflowUint(u) {
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
		x, e := d.blob()
		if e != nil || o.Kind() != reflect.String {
			return ErrMalformed
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
func (d *decoder) list(o reflect.Value, depth int) error {
	n, e := d.u()
	if e != nil || n > maxContainer {
		return ErrMalformed
	}
	if o.Kind() != reflect.Slice && o.Kind() != reflect.Array {
		return ErrMalformed
	}
	if o.Kind() == reflect.Slice {
		o.Set(reflect.MakeSlice(o.Type(), int(n), int(n)))
	}
	if o.Kind() == reflect.Array && int(n) != o.Len() {
		return ErrMalformed
	}
	for i := 0; i < int(n); i++ {
		if e := d.value(o.Index(i), depth+1); e != nil {
			return e
		}
	}
	return nil
}
func (d *decoder) dict(o reflect.Value, depth int) error {
	n, e := d.u()
	if e != nil || n > maxContainer || o.Kind() != reflect.Map || o.Type().Key().Kind() != reflect.String {
		return ErrMalformed
	}
	o.Set(reflect.MakeMapWithSize(o.Type(), int(n)))
	for i := uint64(0); i < n; i++ {
		k, e := d.byte()
		if e != nil || k != KindString {
			return ErrMalformed
		}
		x, e := d.blob()
		if e != nil {
			return e
		}
		v := reflect.New(o.Type().Elem()).Elem()
		if e = d.value(v, depth+1); e != nil {
			return e
		}
		o.SetMapIndex(reflect.ValueOf(string(x)).Convert(o.Type().Key()), v)
	}
	return nil
}
func (d *decoder) structValue(o reflect.Value, depth int) error {
	n, e := d.u()
	if e != nil || n > maxContainer || o.Kind() != reflect.Struct {
		return ErrMalformed
	}
	by := map[uint64]int{}
	for i := 0; i < o.NumField(); i++ {
		sf := o.Type().Field(i)
		if sf.PkgPath != "" || sf.Tag.Get("json") == "-" {
			continue
		}
		id, e := fieldNumber(sf)
		if e != nil {
			// Decoding legacy Go structs without latch tags is supported using
			// declaration-order IDs; encoders remain strict and require tags.
			if errors.Is(e, ErrMissingFieldNumber) {
				id = uint64(i + 1)
			} else {
				return fmt.Errorf("wire: %s.%s: %w", o.Type(), sf.Name, e)
			}
		}
		if _, ok := by[id]; ok {
			return fmt.Errorf("wire: duplicate field number %d", id)
		}
		by[id] = i
	}
	for i := uint64(0); i < n; i++ {
		id, e := d.u()
		if e != nil {
			return e
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
	case KindString, KindBytes:
		_, e = d.blob()
		return e
	case KindList, KindMap:
		n, e := d.u()
		if e != nil || n > maxContainer {
			return ErrMalformed
		}
		for i := uint64(0); i < n; i++ {
			if k == KindMap {
				t, e := d.byte()
				if e != nil || t != KindString {
					return ErrMalformed
				}
				if _, e = d.blob(); e != nil {
					return e
				}
			}
			if e = d.skip(depth + 1); e != nil {
				return e
			}
		}
		return nil
	case KindStruct:
		n, e := d.u()
		if e != nil || n > maxContainer {
			return ErrMalformed
		}
		for i := uint64(0); i < n; i++ {
			if _, e = d.u(); e != nil {
				return e
			}
			if e = d.skip(depth + 1); e != nil {
				return e
			}
		}
		return nil
	}
	return ErrMalformed
}
