package latchwire_test

import (
	"context"
	"testing"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/report"
)

type OtherConnectParams struct {
	APIKey string `json:"apiKey"`
}

func validHandler(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, report.Err) {
	return AddResponse{}, nil
}

func TestRegisterRejectsWrongArgCount(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	err := srv.Register("bad", func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) (AddResponse, error) {
		return AddResponse{}, nil
	})
	if err == nil {
		t.Fatalf("expected error for wrong argument count")
	}
}

func TestRegisterRejectsMissingContext(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	err := srv.Register("bad", func(a string, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, error) {
		return AddResponse{}, nil
	})
	if err == nil {
		t.Fatalf("expected error for missing context.Context")
	}
}

func TestRegisterRejectsConnTypeFromDifferentServer(t *testing.T) {
	srv := latchwire.New[OtherConnectParams](latchwire.Options{})
	err := srv.Register("bad", validHandler) // validHandler takes *Conn[ConnectParams], not *Conn[OtherConnectParams]
	if err == nil {
		t.Fatalf("expected error for connection type mismatch")
	}
}

func TestRegisterRejectsNonErrorSecondReturn(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	err := srv.Register("bad", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, string) {
		return AddResponse{}, ""
	})
	if err == nil {
		t.Fatalf("expected error for non-error second return value")
	}
}

func TestRegisterRejectsWrongReturnCount(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	err := srv.Register("bad", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) AddResponse {
		return AddResponse{}
	})
	if err == nil {
		t.Fatalf("expected error for wrong return count")
	}
}

func TestRegisterRejectsAnonymousStructRequest(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	err := srv.Register("bad", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req struct {
		ID string `json:"id"`
	}) (AddResponse, error) {
		return AddResponse{}, nil
	})
	if err == nil {
		t.Fatalf("expected error for anonymous struct request type")
	}
}

func TestRegisterRejectsPrimitiveRequestType(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	err := srv.Register("bad", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req string) (AddResponse, error) {
		return AddResponse{}, nil
	})
	if err == nil {
		t.Fatalf("expected error for non-struct request type")
	}
}

func TestRegisterRejectsDuplicateMethodName(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	if err := srv.Register("dup", validHandler); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := srv.Register("dup", validHandler); err == nil {
		t.Fatalf("expected error for duplicate method name")
	}
}

func TestRegisterRejectsEmptyMethodName(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	if err := srv.Register("", validHandler); err == nil {
		t.Fatalf("expected error for empty method name")
	}
}

func TestRegisterRejectsReservedMethodName(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	if err := srv.Register("connect", validHandler); err == nil {
		t.Fatalf("expected error for reserved method name %q", "connect")
	}
}

func TestRegisterEventRejectsDuplicateAndReservedAndEmpty(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})

	tick1 := latchwire.Event[Tick]("tick")
	if err := srv.RegisterEvent(tick1); err != nil {
		t.Fatalf("first RegisterEvent: %v", err)
	}
	tick2 := latchwire.Event[Tick]("tick")
	if err := srv.RegisterEvent(tick2); err == nil {
		t.Fatalf("expected error for duplicate event name")
	}

	empty := latchwire.Event[Tick]("")
	if err := srv.RegisterEvent(empty); err == nil {
		t.Fatalf("expected error for empty event name")
	}

	reserved := latchwire.Event[Tick]("event")
	if err := srv.RegisterEvent(reserved); err == nil {
		t.Fatalf("expected error for reserved event name")
	}
}

func TestOnConnectRejectsSecondRegistration(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	if err := srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) report.Err { return nil }); err != nil {
		t.Fatalf("first OnConnect: %v", err)
	}
	if err := srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) report.Err { return nil }); err == nil {
		t.Fatalf("expected error for second OnConnect registration")
	}
}

func TestRegistrationRejectedAfterFinalization(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	if _, err := srv.Manifest(); err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if err := srv.Register("late", validHandler); err == nil {
		t.Fatalf("expected error registering a method after finalization")
	}
	if err := srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) report.Err { return nil }); err == nil {
		t.Fatalf("expected error registering OnConnect after finalization")
	}
}

func TestMethodNamespaceCollisionRejected(t *testing.T) {
	srv := latchwire.New[ConnectParams](latchwire.Options{})
	if err := srv.Register("user", validHandler); err != nil {
		t.Fatalf("Register user: %v", err)
	}
	if err := srv.Register("user.get", validHandler); err != nil {
		t.Fatalf("Register user.get: %v", err)
	}
	if _, err := srv.Manifest(); err == nil {
		t.Fatalf("expected finalize to fail due to method namespace collision between %q and %q", "user", "user.get")
	}
}
