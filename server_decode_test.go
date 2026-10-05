package latch_test

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/wire"
)

type serverDecodeRequest struct {
	Values []string `latch:"1"`
}

func TestServerRejectsHostileDecodeCountsAndKeepsConnectionUsable(t *testing.T) {
	// The maximum permitted container count must not cause eager allocation
	// when the payload contains only a header and no elements.
	var count [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(count[:], 1<<24)
	cases := []struct {
		name    string
		payload []byte
	}{
		{"struct header", append([]byte{wire.KindStruct}, count[:n]...)},
		{"known list header", append([]byte{wire.KindStruct, 1, 1, wire.KindList}, count[:n]...)},
		{"unknown list header", append([]byte{wire.KindStruct, 1, 99, wire.KindList}, count[:n]...)},
		{"unknown map header", append([]byte{wire.KindStruct, 1, 99, wire.KindMap}, count[:n]...)},
		{"unknown struct header", append([]byte{wire.KindStruct, 1, 99, wire.KindStruct}, count[:n]...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := newCoreServer(t, latch.Options{ProtocolVersion: "1"})
			server.Register("decode_values", func(_ context.Context, _ coreState, req serverDecodeRequest) (coreResponse, error) {
				calls.Add(1)
				return coreResponse{Value: len(req.Values)}, nil
			})
			_, client := serveCore(t, server, "?version=1")

			client.Send(wire.Envelope{
				Type: wire.FrameRequest, ID: "hostile", Method: "decode_values", Payload: tc.payload,
			})
			requireWireError(t, client, "hostile", latch.ErrCodeInvalidRequest)
			if got := calls.Load(); got != 0 {
				t.Fatalf("handler called %d times for hostile payload", got)
			}

			client.Request("valid", "decode_values", serverDecodeRequest{Values: []string{"ok"}})
			if response := readCoreResponse(t, client, "valid"); response.Value != 1 {
				t.Fatalf("response = %+v, want one value", response)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("handler called %d times, want exactly one valid request", got)
			}
		})
	}
}

func TestServerConfiguredDecodeBudget(t *testing.T) {
	request := serverDecodeRequest{Values: []string{strings.Repeat("x", 1024)}}
	payload, err := wire.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded serverDecodeRequest
	if err := wire.DecodeWithLimit(payload, &decoded, 1); !errors.Is(err, wire.ErrDecodeLimit) {
		t.Fatalf("decode with one-byte budget = %v, want ErrDecodeLimit", err)
	}
	if err := wire.DecodeWithLimit(payload, &decoded, 1<<20); err != nil {
		t.Fatalf("otherwise valid request: %v", err)
	}

	for _, tc := range []struct {
		name  string
		limit int64
		allow bool
	}{
		{"small limit", 1, false},
		{"larger limit", 1 << 20, true},
		{"default limit", 0, true},
		{"negative selects default", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := newCoreServer(t, latch.Options{ProtocolVersion: "1", MaxDecodeBytes: tc.limit})
			server.Register("decode_values", func(_ context.Context, _ coreState, req serverDecodeRequest) (coreResponse, error) {
				calls.Add(1)
				return coreResponse{Value: len(req.Values)}, nil
			})
			_, client := serveCore(t, server, "?version=1")
			client.Send(wire.Envelope{
				Type: wire.FrameRequest, ID: "budget", Method: "decode_values", Payload: payload,
			})
			if !tc.allow {
				requireWireError(t, client, "budget", latch.ErrCodeInvalidRequest)
				if got := calls.Load(); got != 0 {
					t.Fatalf("handler called %d times for over-budget request", got)
				}
				return
			}
			if response := readCoreResponse(t, client, "budget"); response.Value != 1 {
				t.Fatalf("response = %+v, want one value", response)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("handler called %d times, want one", got)
			}
		})
	}
}
