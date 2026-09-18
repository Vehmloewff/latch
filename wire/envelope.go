// Package wire defines Latchwire's on-the-wire JSON envelope. Every
// WebSocket text message, in both directions, is exactly one Envelope.
package wire

import "encoding/json"

// FrameType discriminates the kind of Envelope.
type FrameType string

const (
	FrameConnect         FrameType = "connect"
	FrameConnected       FrameType = "connected"
	FrameRequest         FrameType = "request"
	FrameResponse        FrameType = "response"
	FrameError           FrameType = "error"
	FrameEvent           FrameType = "event"
	FrameConnectionError FrameType = "connection_error"
)

// Envelope is the single discriminated-union message shape used for every
// frame type. Fields irrelevant to a given Type are simply omitted.
type Envelope struct {
	Type FrameType `json:"type"`

	// connect / connected
	Protocol string `json:"protocol,omitempty"`
	Version  string `json:"version,omitempty"`

	// request / response / error
	ID     string `json:"id,omitempty"`
	Method string `json:"method,omitempty"`

	// event
	Event string `json:"event,omitempty"`

	// request payload, response payload, connect payload, or event payload
	Payload json.RawMessage `json:"payload,omitempty"`

	// error / connection_error. Errors intentionally carry only a safe,
	// human-readable report message.
	Error string `json:"error,omitempty"`
}
