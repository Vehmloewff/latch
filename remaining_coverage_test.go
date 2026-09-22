package latch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type remainingCoverageState struct{}

type remainingCoverageEvent struct {
	Kind string `json:"kind"`
}

type remainingCoverageRequest struct {
	Value int `json:"value"`
}

type remainingCoverageResponse struct {
	Value int `json:"value"`
}

func TestServerMethodsReturnsSortedDescriptorsAndEventsCompatibilitySnapshot(t *testing.T) {
	server := New[remainingCoverageState](Options{ProtocolVersion: "coverage"})
	server.OnConnect(func(context.Context, Emitter[remainingCoverageEvent], *Conn) remainingCoverageState {
		return remainingCoverageState{}
	})
	server.Register("zulu_method", func(context.Context, remainingCoverageState, remainingCoverageRequest) (remainingCoverageResponse, error) {
		return remainingCoverageResponse{}, nil
	})
	server.Register("alpha_method", func(context.Context, remainingCoverageState, remainingCoverageRequest) (remainingCoverageResponse, error) {
		return remainingCoverageResponse{}, nil
	})

	methods := server.Methods()
	if len(methods) != 2 || methods[0].Name != "alpha_method" || methods[1].Name != "zulu_method" {
		t.Fatalf("Methods() = %#v, want sorted descriptors", methods)
	}
	for _, method := range methods {
		if method.RequestType != reflect.TypeOf(remainingCoverageRequest{}) || method.ResponseType != reflect.TypeOf(remainingCoverageResponse{}) {
			t.Fatalf("method %q types = %v/%v, want request/response types", method.Name, method.RequestType, method.ResponseType)
		}
	}
	if events := server.Events(); events != nil {
		t.Fatalf("Events() = %#v, want nil compatibility snapshot", events)
	}
}

func TestUnattachedEmitterSendReturnsActionableError(t *testing.T) {
	var emitter Emitter[remainingCoverageEvent]
	if err := emitter.Send(remainingCoverageEvent{Kind: "unattached"}); err == nil || err.Error() != "latch: emitter is not attached to a connection" {
		t.Fatalf("unattached Send error = %v, want attachment error", err)
	}
}

func TestRootErrorFormatsCodeAndMessage(t *testing.T) {
	err := NewError("invalid_request", "missing value")
	if got, want := err.Error(), "invalid_request: missing value"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestTracingHelpersUseActiveNoopOpenTelemetrySpan(t *testing.T) {
	tracer := trace.NewNoopTracerProvider().Tracer("latch-coverage")
	ctx, span := tracer.Start(context.Background(), "coverage")
	defer span.End()

	spanEvent(ctx, "request.started", attribute.String("method", "coverage"))
	spanError(ctx, "request.failed", errors.New("boom"), attribute.Bool("retryable", false))
	spanError(ctx, "request.succeeded", nil)

	// The no-op provider is intentional: this module depends on the OpenTelemetry
	// API but not the SDK/test exporter. The calls above still exercise both
	// helper branches without adding a test-only module dependency.
}

func TestPanicReportPreservesPanicValueAndMarksItInternal(t *testing.T) {
	report := panicReport("boom")
	if !strings.Contains(report.Error(), "panic: boom") {
		t.Fatalf("panic report = %q, want panic value", report.Error())
	}
	if !report.IsInternal() {
		t.Fatal("panic report is not marked internal")
	}
}
