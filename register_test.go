package latch_test

import (
	"context"
	"testing"

	"github.com/vehmloewff/latch"
)

type registerState struct{}
type registerRequest struct{}
type registerResponse struct{}

func validRegisterHandler(context.Context, registerState, registerRequest) (registerResponse, error) {
	return registerResponse{}, nil
}

func TestRegisterRejectsInvalidHandler(t *testing.T) {
	tests := []struct {
		name    string
		handler any
	}{
		{"wrong argument count", func(context.Context) (registerResponse, error) { return registerResponse{}, nil }},
		{"missing context", func(string, registerState, registerRequest) (registerResponse, error) { return registerResponse{}, nil }},
		{"wrong response", func(context.Context, registerState, registerRequest) (string, error) { return "", nil }},
		{"anonymous request", func(context.Context, registerState, struct{}) (registerResponse, error) {
			return registerResponse{}, nil
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := latch.New[registerState](latch.Options{})
			defer func() {
				if recover() == nil {
					t.Fatal("Register did not panic")
				}
			}()
			server.Register("valid_name", tt.handler)
		})
	}
}

func TestRegisterRequiresSnakeCase(t *testing.T) {
	for _, name := range []string{"", "camelCase", "PascalCase", "with-hyphen", "with..dots", "_leading", "trailing_", "double__underscore"} {
		t.Run(name, func(t *testing.T) {
			server := latch.New[registerState](latch.Options{})
			defer func() {
				if recover() == nil {
					t.Fatal("Register did not panic")
				}
			}()
			server.Register(name, validRegisterHandler)
		})
	}
}

func TestRegisterRejectsDuplicateAndLateRegistration(t *testing.T) {
	server := latch.New[registerState](latch.Options{})
	server.Register("valid_name", validRegisterHandler)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("duplicate Register did not panic")
			}
		}()
		server.Register("valid_name", validRegisterHandler)
	}()

	server.OnConnect(func(context.Context, latch.Emitter[registerEvent], *latch.Conn) (registerState, error) {
		return registerState{}, nil
	})
	if _, err := server.Schema(); err != nil {
		t.Fatalf("Schema: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("late Register did not panic")
		}
	}()
	server.Register("another_name", validRegisterHandler)
}

type registerEvent struct{}
