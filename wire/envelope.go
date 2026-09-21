// Package wire defines Latch's on-the-wire JSON envelope. Every
// WebSocket text message, in both directions, is exactly one Envelope.
package wire

import "encoding/json"

// FrameType discriminates the kind of Envelope.
type FrameType string

const (
	// FrameConnect and FrameConnected are retained for decoding older
	// protocol traffic. Current clients do not send or expect either frame.
	FrameConnect         FrameType = "connect"
	FrameConnected       FrameType = "connected"
	FrameRequest         FrameType = "request"
	FrameResponse        FrameType = "response"
	FrameError           FrameType = "error"
	FrameEvent           FrameType = "event"
	FrameConnectionError FrameType = "connection_error"
)

// Error is the legacy structured error shape retained for compatibility with
// older protocol users. New frames use the safe Error message string below.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Envelope is the single discriminated-union message shape used for every
// frame type. Fields irrelevant to a given Type are simply omitted.
type Envelope struct {
	Type FrameType `json:"type"`

	// Version is retained for protocol metadata in server-generated frames.
	Version string `json:"version,omitempty"`
	// request / response / error
	ID     string `json:"id,omitempty"`
	Method string `json:"method,omitempty"`

	// Event is ignored by current runtimes. It is retained so older clients
	// can decode envelopes while migrating to the single event stream.
	Event string `json:"event,omitempty"`

	// request, response, or event payload
	Payload json.RawMessage `json:"payload,omitempty"`

	// error / connection_error. ErrorCode preserves the safe application
	// error code while Error carries its human-readable message.
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}
