// Package wire defines Latch's binary WebSocket protocol. Every runtime frame
// is a binary Envelope encoded with MarshalBinary.
package wire

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

// Error is the structured application error carried by the binary protocol.
type Error struct {
	Code    string
	Message string
}

// Envelope is the single discriminated-union message shape used for every
// binary frame. Fields irrelevant to a given Type are encoded as empty values.
type Envelope struct {
	Type      FrameType
	Version   string
	ID        string
	Method    string
	Event     string
	Payload   []byte
	Error     string
	ErrorCode string
}
