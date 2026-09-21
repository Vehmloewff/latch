// Package testutil provides a minimal handwritten WebSocket client for
// exercising the Latchwire wire protocol directly in tests, without
// depending on any generated client.
package testutil

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/vehmloewff/latch/wire"
)

const defaultTimeout = 5 * time.Second

// Client is a raw Latchwire WebSocket client for tests.
type Client struct {
	t  *testing.T
	ws *websocket.Conn
}

// Dial connects to url (a "ws://..." URL) and registers cleanup to close
// the connection when the test ends.
func Dial(t *testing.T, url string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("testutil: dial %s: %v", url, err)
	}
	t.Cleanup(func() { _ = ws.Close(websocket.StatusNormalClosure, "") })
	return &Client{t: t, ws: ws}
}

// Send marshals and writes env as a single WebSocket text message.
func (c *Client) Send(env wire.Envelope) {
	c.t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		c.t.Fatalf("testutil: marshal envelope: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, raw); err != nil {
		c.t.Fatalf("testutil: write: %v", err)
	}
}

// SendRaw writes raw bytes directly, bypassing envelope construction, for
// tests of malformed-input handling.
func (c *Client) SendRaw(raw []byte) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, raw); err != nil {
		c.t.Fatalf("testutil: write raw: %v", err)
	}
}

// Recv reads and decodes the next frame, failing the test on error or
// timeout.
func (c *Client) Recv() wire.Envelope {
	c.t.Helper()
	env, err := c.TryRecv(defaultTimeout)
	if err != nil {
		c.t.Fatalf("testutil: recv: %v", err)
	}
	return env
}

// TryRecv reads and decodes the next frame with an explicit timeout,
// returning an error instead of failing the test (for negative-path tests
// that expect a closed connection or no message).
func (c *Client) TryRecv(timeout time.Duration) (wire.Envelope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, raw, err := c.ws.Read(ctx)
	if err != nil {
		return wire.Envelope{}, err
	}
	var env wire.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return wire.Envelope{}, err
	}
	return env, nil
}

// Connect sends a "connect" frame with the given protocol/version/payload
// and returns the server's first response frame (either "connected" or
// "connection_error").
func (c *Client) Connect(protocol, version string, payload any) wire.Envelope {
	c.t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		c.t.Fatalf("testutil: marshal connect payload: %v", err)
	}
	c.Send(wire.Envelope{Type: wire.FrameConnect, Version: version, Payload: raw})
	return c.Recv()
}

// Request sends a "request" frame for method with the given id and payload.
func (c *Client) Request(id, method string, payload any) {
	c.t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		c.t.Fatalf("testutil: marshal request payload: %v", err)
	}
	c.Send(wire.Envelope{Type: wire.FrameRequest, ID: id, Method: method, Payload: raw})
}

// Close closes the underlying WebSocket connection immediately.
func (c *Client) Close() {
	_ = c.ws.CloseNow()
}
