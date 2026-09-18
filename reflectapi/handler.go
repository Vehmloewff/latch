package reflectapi

import (
	"context"
	"fmt"
	"reflect"

	"github.com/vehmloewff/report"
)

var ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
var errType = reflect.TypeOf((*report.Err)(nil)).Elem()

// HandlerAdapter wraps a validated Latchwire method handler so callers can
// invoke it generically without knowing the concrete request/response types
// at compile time.
type HandlerAdapter struct {
	RequestType  reflect.Type
	ResponseType reflect.Type

	fn reflect.Value
}

// ValidateHandler checks that handler has the single canonical Latchwire
// method signature:
//
//	func(context.Context, <wantConnType>, Request) (Response, report.Err)
//
// where Request and Response are named, exported struct types. It returns a
// descriptive error for every rejected shape instead of deferring the check
// to the first network request.
func ValidateHandler(handler any, wantConnType reflect.Type) (*HandlerAdapter, error) {
	if handler == nil {
		return nil, fmt.Errorf("handler must not be nil")
	}

	hv := reflect.ValueOf(handler)
	ht := hv.Type()

	if ht.Kind() != reflect.Func {
		return nil, fmt.Errorf("handler must be a function, got %s", ht.Kind())
	}
	if ht.IsVariadic() {
		return nil, fmt.Errorf("handler must not be variadic")
	}
	if ht.NumIn() != 3 {
		return nil, fmt.Errorf(
			"handler must accept exactly 3 arguments (context.Context, *latchwire.Conn[...], Request), got %d",
			ht.NumIn(),
		)
	}
	if ht.NumOut() != 2 {
		return nil, fmt.Errorf(
			"handler must return exactly 2 values (Response, report.Err), got %d",
			ht.NumOut(),
		)
	}

	if ht.In(0) != ctxType {
		return nil, fmt.Errorf("handler's first argument must be context.Context, got %s", ht.In(0))
	}

	if ht.In(1) != wantConnType {
		return nil, fmt.Errorf(
			"handler's second argument must be %s (the connection type for this server), got %s; "+
				"this usually means the handler was written for a different Server's connect-parameter type",
			wantConnType, ht.In(1),
		)
	}

	reqType := ht.In(2)
	if err := requireNamedStruct(reqType); err != nil {
		return nil, fmt.Errorf("handler request type: %w", err)
	}

	respType := ht.Out(0)
	if err := requireNamedStruct(respType); err != nil {
		return nil, fmt.Errorf("handler response type: %w", err)
	}

	errOutType := ht.Out(1)
	if !errOutType.Implements(errType) {
		return nil, fmt.Errorf("handler's second return value must be report.Err, got %s", errOutType)
	}
	if errOutType != errType {
		return nil, fmt.Errorf("handler's second return value must be exactly report.Err, got %s", errOutType)
	}

	return &HandlerAdapter{
		RequestType:  reqType,
		ResponseType: respType,
		fn:           hv,
	}, nil
}

func requireNamedStruct(t reflect.Type) error {
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("must be a struct type, got %s (kind %s)", t, t.Kind())
	}
	if t.Name() == "" {
		return fmt.Errorf("anonymous struct types are not supported; define a named type")
	}
	if t.PkgPath() == "" {
		return fmt.Errorf("type %s has no package path", t.Name())
	}
	return nil
}

// NewRequest allocates a zero-valued pointer to the handler's request type,
// suitable for json.Unmarshal.
func (h *HandlerAdapter) NewRequest() any {
	return reflect.New(h.RequestType).Interface()
}

// Call invokes the wrapped handler. connVal must be a reflect.Value holding
// the concrete *latchwire.Conn[C] instance matching wantConnType passed to
// ValidateHandler. reqPtr must be a pointer to h.RequestType, typically the
// value returned by NewRequest after being unmarshaled into.
func (h *HandlerAdapter) Call(ctx context.Context, connVal reflect.Value, reqPtr any) (resp any, err report.Err) {
	reqVal := reflect.ValueOf(reqPtr).Elem()
	outs := h.fn.Call([]reflect.Value{reflect.ValueOf(ctx), connVal, reqVal})
	resp = outs[0].Interface()
	if errIface := outs[1].Interface(); errIface != nil {
		err = errIface.(report.Err)
	}
	return resp, err
}
