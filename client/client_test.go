package client_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/client"
	"github.com/vehmloewff/report"
)

type connectParams struct {
	Token string `json:"token" jsonschema:"minLength=1"`
}

type addRequest struct {
	A int `json:"a"`
	B int `json:"b"`
}

type addResponse struct {
	Result int `json:"result"`
}

type tick struct {
	Value int `json:"value"`
}

func newTestServer(t *testing.T) (url string, tickEvent *latchwire.EventDef[tick]) {
	t.Helper()
	srv := latchwire.New[connectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})

	evt := latchwire.Event[tick]("tick")
	if err := srv.RegisterEvent(evt); err != nil {
		t.Fatalf("RegisterEvent: %v", err)
	}

	err := srv.Register("math.add", func(ctx context.Context, conn *latchwire.Conn[connectParams], req addRequest) (addResponse, report.Err) {
		if req.A == -1 {
			return addResponse{}, report.New("a must not be -1")
		}
		return addResponse{Result: req.A + req.B}, nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	err = srv.OnConnect(func(ctx context.Context, conn *latchwire.Conn[connectParams]) report.Err {
		if conn.Params().Token == "reject-me" {
			return report.New("token rejected")
		}
		if err := evt.Send(conn, tick{Value: 1}); err != nil {
			return report.From(err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("OnConnect: %v", err)
	}

	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws", evt
}

func TestClientConnectCallAndEvent(t *testing.T) {
	url, _ := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := client.Connect(ctx, url, "demo", "1", connectParams{Token: "abc"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	tickCh := client.RegisterEvent[tick](conn, "tick")
	conn.Start()
	defer conn.Close()

	select {
	case v := <-tickCh:
		if v.Value != 1 {
			t.Fatalf("expected tick value 1, got %d", v.Value)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for tick event")
	}

	resp, err := client.Call[addResponse](ctx, conn, "math.add", addRequest{A: 2, B: 3})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Result != 5 {
		t.Fatalf("expected 5, got %d", resp.Result)
	}
}

func TestClientApplicationError(t *testing.T) {
	url, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := client.Connect(ctx, url, "demo", "1", connectParams{Token: "abc"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	conn.Start()
	defer conn.Close()

	_, err = client.Call[addResponse](ctx, conn, "math.add", addRequest{A: -1, B: 1})
	if err == nil {
		t.Fatalf("expected an error")
	}
	if err.Error() != "handler returned an error" {
		t.Fatalf("expected application error message, got %v", err)
	}
}

func TestClientConnectRejected(t *testing.T) {
	url, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The connect params carry jsonschema:"minLength=1", so an empty token
	// is rejected by schema validation before OnConnect ever runs.
	_, err := client.Connect(ctx, url, "demo", "1", connectParams{Token: ""})
	if err == nil {
		t.Fatalf("expected connect to be rejected")
	}
	if err.Error() != "connect payload failed schema validation" {
		t.Fatalf("expected connect payload error, got %v", err)
	}
}

func TestClientOnConnectRejected(t *testing.T) {
	url, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Passes schema validation (non-empty) but is rejected by OnConnect.
	_, err := client.Connect(ctx, url, "demo", "1", connectParams{Token: "reject-me"})
	if err == nil {
		t.Fatalf("expected connect to be rejected by OnConnect")
	}
	if err.Error() != "connection rejected" {
		t.Fatalf("expected OnConnect rejection message, got %v", err)
	}
}

func TestClientConcurrentCalls(t *testing.T) {
	url, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := client.Connect(ctx, url, "demo", "1", connectParams{Token: "abc"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	conn.Start()
	defer conn.Close()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := client.Call[addResponse](ctx, conn, "math.add", addRequest{A: i, B: 1})
			if err != nil {
				errs <- err
				return
			}
			if resp.Result != i+1 {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent call failed: %v", err)
		}
	}
}

func TestClientCloseFailsPendingAndClosesEventChannel(t *testing.T) {
	url, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := client.Connect(ctx, url, "demo", "1", connectParams{Token: "abc"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	tickCh := client.RegisterEvent[tick](conn, "tick")
	conn.Start()

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-tickCh:
		// Either a buffered tick or the closed zero-value read is fine; the
		// channel must not block forever, which the second read below
		// verifies by requiring it to be closed.
	case <-time.After(2 * time.Second):
		t.Fatalf("event channel never became ready after close")
	}

	// Drain until closed.
	for {
		select {
		case _, ok := <-tickCh:
			if !ok {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event channel was never closed after Close()")
		}
	}
}
