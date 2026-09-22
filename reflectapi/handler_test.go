package reflectapi

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type handlerState struct {
	Prefix string
}

type handlerRequest struct {
	Value int
}

type handlerResponse struct {
	Value int
}

func TestHandlerAdapterValidatesAndInvokesStateHandler(t *testing.T) {
	ctxKey := struct{}{}
	wantContext := context.WithValue(context.Background(), ctxKey, "present")
	wantState := handlerState{Prefix: "state"}
	adapter, err := ValidateStateHandler(func(ctx context.Context, state handlerState, req handlerRequest) (handlerResponse, error) {
		if ctx.Value(ctxKey) != "present" {
			return handlerResponse{}, errors.New("context value was not preserved")
		}
		return handlerResponse{Value: req.Value + len(state.Prefix)}, nil
	}, reflect.TypeOf(handlerState{}))
	if err != nil {
		t.Fatalf("ValidateStateHandler: %v", err)
	}
	if adapter.RequestType != reflect.TypeOf(handlerRequest{}) {
		t.Fatalf("RequestType = %v", adapter.RequestType)
	}
	if adapter.ResponseType != reflect.TypeOf(handlerResponse{}) {
		t.Fatalf("ResponseType = %v", adapter.ResponseType)
	}

	reqPtr, ok := adapter.NewRequest().(*handlerRequest)
	if !ok {
		t.Fatalf("NewRequest() type = %T, want *handlerRequest", adapter.NewRequest())
	}
	reqPtr.Value = 7
	response, err := adapter.Call(wantContext, reflect.ValueOf(wantState), reqPtr)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got, ok := response.(handlerResponse); !ok || got.Value != 12 {
		t.Fatalf("response = %#v, want handlerResponse{Value: 12}", response)
	}
}

func TestHandlerAdapterReturnsHandlerErrorsAndAliasMatches(t *testing.T) {
	wantErr := errors.New("application failure")
	handler := func(context.Context, handlerState, handlerRequest) (handlerResponse, error) {
		return handlerResponse{}, wantErr
	}
	adapter, err := ValidateHandler(handler, reflect.TypeOf(handlerState{}))
	if err != nil {
		t.Fatalf("ValidateHandler: %v", err)
	}
	response, gotErr := adapter.Call(context.Background(), reflect.ValueOf(handlerState{}), &handlerRequest{})
	if response != (handlerResponse{}) {
		t.Fatalf("response = %#v, want zero response", response)
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("Call error = %v, want %v", gotErr, wantErr)
	}
}

func TestValidateStateHandlerRejectsInvalidShapes(t *testing.T) {
	stateType := reflect.TypeOf(handlerState{})
	valid := func(context.Context, handlerState, handlerRequest) (handlerResponse, error) {
		return handlerResponse{}, nil
	}
	var nilHandler any

	tests := []struct {
		name    string
		handler any
		want    string
	}{
		{"nil", nilHandler, "must not be nil"},
		{"non-function", 42, "must be a function"},
		{"variadic", func(context.Context, handlerState, ...handlerRequest) (handlerResponse, error) {
			return handlerResponse{}, nil
		}, "variadic"},
		{"wrong input count", func(context.Context, handlerState) (handlerResponse, error) {
			return handlerResponse{}, nil
		}, "exactly 3 arguments"},
		{"wrong output count", func(context.Context, handlerState, handlerRequest) handlerResponse {
			return handlerResponse{}
		}, "exactly 2 values"},
		{"wrong context", func(string, handlerState, handlerRequest) (handlerResponse, error) {
			return handlerResponse{}, nil
		}, "first argument"},
		{"wrong state", func(context.Context, string, handlerRequest) (handlerResponse, error) {
			return handlerResponse{}, nil
		}, "second argument"},
		{"anonymous request", func(context.Context, handlerState, struct{}) (handlerResponse, error) {
			return handlerResponse{}, nil
		}, "anonymous struct"},
		{"anonymous response", func(context.Context, handlerState, handlerRequest) (struct{}, error) {
			return struct{}{}, nil
		}, "anonymous struct"},
		{"wrong error result", func(context.Context, handlerState, handlerRequest) (handlerResponse, string) {
			return handlerResponse{}, ""
		}, "must be error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateStateHandler(tt.handler, stateType)
			if err == nil {
				t.Fatal("ValidateStateHandler succeeded for invalid handler")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}

	adapter, err := ValidateStateHandler(valid, stateType)
	if err != nil || adapter == nil {
		t.Fatalf("valid handler rejected: adapter=%v err=%v", adapter, err)
	}
}
