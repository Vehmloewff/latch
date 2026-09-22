package protocol

import "testing"

func TestKindIntegerAnd64BitHelpers(t *testing.T) {
	integerKinds := []Kind{
		KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
	}
	for _, kind := range integerKinds {
		if !kind.IsInteger() {
			t.Errorf("%q.IsInteger() = false, want true", kind)
		}
	}
	for _, kind := range []Kind{KindString, KindBool, KindFloat32, KindFloat64, KindStruct, KindSlice, KindTime} {
		if kind.IsInteger() {
			t.Errorf("%q.IsInteger() = true, want false", kind)
		}
	}

	if !KindInt64.Is64Bit() || !KindUint64.Is64Bit() {
		t.Fatal("int64 and uint64 must be reported as 64-bit")
	}
	for _, kind := range []Kind{KindInt, KindInt32, KindUint, KindFloat64, KindString} {
		if kind.Is64Bit() {
			t.Errorf("%q.Is64Bit() = true, want false", kind)
		}
	}
}

func TestProtocolEventRefPrefersCurrentEventType(t *testing.T) {
	current := TypeRef{Kind: KindStruct, NamedType: "current.Event"}
	legacy := TypeRef{Kind: KindStruct, NamedType: "legacy.Event"}

	p := Protocol{EventType: current, Events: []LegacyEvent{{Name: "old", PayloadType: legacy}}}
	got, ok := p.EventRef()
	if !ok || got != current {
		t.Fatalf("EventRef() = %#v, %v, want current event %#v, true", got, ok, current)
	}
}

func TestProtocolEventRefSupportsOnlyOneLegacyEvent(t *testing.T) {
	legacy := TypeRef{Kind: KindEnum, NamedType: "legacy.Event"}
	p := Protocol{Events: []LegacyEvent{{Name: "old", PayloadType: legacy}}}
	got, ok := p.EventRef()
	if !ok || got != legacy {
		t.Fatalf("single legacy EventRef() = %#v, %v, want %#v, true", got, ok, legacy)
	}

	p.Events = append(p.Events, LegacyEvent{Name: "another", PayloadType: legacy})
	if _, ok := p.EventRef(); ok {
		t.Fatal("EventRef succeeded for multiple legacy events")
	}
	empty := Protocol{}
	if _, ok := empty.EventRef(); ok {
		t.Fatal("EventRef succeeded for protocol with no event")
	}
}

func TestProtocolTypeByID(t *testing.T) {
	first := &NamedType{ID: "example.First", GoName: "First", Kind: KindStruct}
	second := &NamedType{ID: "example.Second", GoName: "Second", Kind: KindEnum}
	p := Protocol{Types: []*NamedType{first, second}}

	if got := p.TypeByID("example.First"); got != first {
		t.Fatalf("TypeByID(first) = %p, want %p", got, first)
	}
	if got := p.TypeByID("missing"); got != nil {
		t.Fatalf("TypeByID(missing) = %#v, want nil", got)
	}
}
