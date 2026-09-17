package latchwire

import "fmt"

// Well-known wire error codes. Applications are free to use any other code
// string for their own application errors returned via NewError.
const (
	// ErrCodeMethodNotFound is returned when a request names a method that
	// was never registered.
	ErrCodeMethodNotFound = "method_not_found"

	// ErrCodeInvalidRequest is returned when a request's payload fails JSON
	// Schema validation or cannot be decoded.
	ErrCodeInvalidRequest = "invalid_request"

	// ErrCodeInternal is returned for any unexpected Go error or panic
	// surfaced by a handler. The underlying error is never sent to the
	// client; see Options.Logger and Options.Debug.
	ErrCodeInternal = "internal_error"

	// ErrCodeInvalidConnectPayload is returned in a connection_error frame
	// when the connect payload fails schema validation or decoding.
	ErrCodeInvalidConnectPayload = "invalid_connect_payload"

	// ErrCodeProtocolViolation is returned in a connection_error frame when
	// the client violates the handshake protocol (e.g. sends a request
	// before connecting, or connects twice).
	ErrCodeProtocolViolation = "protocol_violation"

	// ErrCodeConnectRejected is returned when OnConnect returns a non-nil
	// error that is not already a *Error.
	ErrCodeConnectRejected = "connect_rejected"

	// ErrCodeDuplicateRequestID is returned when a client reuses a request
	// ID that is already in flight on the same connection.
	ErrCodeDuplicateRequestID = "duplicate_request_id"
)

// Error is Latchwire's structured application error. Handlers return it to
// produce a specific wire error code and message; any other error type
// returned by a handler is converted into an opaque ErrCodeInternal error
// before it reaches the client.
type Error struct {
	Code    string
	Message string
}

// NewError constructs an application Error with the given wire code and
// human-readable message.
func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
