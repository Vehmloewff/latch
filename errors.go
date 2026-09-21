package latch

import "fmt"

const (
	ErrCodeMethodNotFound        = "method_not_found"
	ErrCodeInvalidRequest        = "invalid_request"
	ErrCodeInternal              = "internal_error"
	ErrCodeInvalidConnectPayload = "invalid_connect_payload"
	ErrCodeProtocolViolation     = "protocol_violation"
	ErrCodeConnectRejected       = "connect_rejected"
	ErrCodeDuplicateRequestID    = "duplicate_request_id"
)

// Error is retained for compatibility with the redesigned protocol code.
type Error struct {
	Code    string
	Message string
}

func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
