package latch

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/vehmloewff/report"
)

const instrumentationName = "github.com/vehmloewff/latch"

func spanEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

func spanError(ctx context.Context, name string, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if err != nil {
		span.RecordError(report.From(err).Wrap(name).Dump("operation", name),
			trace.WithAttributes(attrs...))
	}
	span.SetStatus(codes.Error, name)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

func panicReport(value any) report.Err {
	return report.New(fmt.Sprintf("panic: %v", value)).Internal()
}
