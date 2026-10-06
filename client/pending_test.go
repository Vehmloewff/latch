package client

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRegisterPendingDuringShutdownIsRejected(t *testing.T) {
	conn := &Conn{
		pending: make(map[string]chan pendingResult),
		closed:  make(chan struct{}),
	}
	// Hold registration and the shutdown drain at the same lock, then let
	// shutdown publish closed before either can acquire it.
	conn.pendingMu.Lock()
	registered := make(chan error, 1)
	go func() {
		registered <- conn.registerPending("late", make(chan pendingResult, 1))
	}()
	terminated := make(chan struct{})
	go func() {
		conn.terminate()
		close(terminated)
	}()
	select {
	case <-conn.Closed():
	case <-time.After(2 * time.Second):
		conn.pendingMu.Unlock()
		t.Fatal("shutdown did not publish closed")
	}
	conn.pendingMu.Unlock()

	select {
	case err := <-registered:
		var clientErr *Error
		if !errors.As(err, &clientErr) || clientErr.Code != "connection_closed" {
			t.Fatalf("registration during shutdown = %v, want connection_closed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("registration remained blocked during shutdown")
	}
	select {
	case <-terminated:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if len(conn.pending) != 0 {
		t.Fatalf("shutdown left %d orphaned calls", len(conn.pending))
	}
}

func TestShutdownResolvesEveryRegisteredPendingCall(t *testing.T) {
	conn := &Conn{
		pending: make(map[string]chan pendingResult),
		closed:  make(chan struct{}),
	}
	results := make([]chan pendingResult, 16)
	for i := range results {
		results[i] = make(chan pendingResult, 1)
		if err := conn.registerPending(fmt.Sprint(i), results[i]); err != nil {
			t.Fatal(err)
		}
	}
	conn.terminate()
	for i, result := range results {
		select {
		case res := <-result:
			if res.err == nil || res.err.Code != "connection_closed" {
				t.Fatalf("pending call %d result = %+v, want connection_closed", i, res)
			}
		default:
			t.Fatalf("pending call %d was not resolved by shutdown", i)
		}
	}
}
