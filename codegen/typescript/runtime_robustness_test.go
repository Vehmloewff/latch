package typescript

import (
	"strings"
	"testing"
)

func generatedTypeScriptClient(t *testing.T) string {
	t.Helper()
	files, err := Generate(buildFixtureProtocol(t), Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return string(files["client.ts"])
}

func sourceSection(t *testing.T, source, start, end string) string {
	t.Helper()
	startAt := strings.Index(source, start)
	if startAt < 0 {
		t.Fatalf("generated source missing section start %q", start)
	}
	section := source[startAt+len(start):]
	if end == "" {
		return section
	}
	endAt := strings.Index(section, end)
	if endAt < 0 {
		t.Fatalf("generated source missing section end %q after %q", end, start)
	}
	return section[:endAt]
}

func requireSource(t *testing.T, source, want, behavior string) {
	t.Helper()
	if !strings.Contains(source, want) {
		t.Errorf("generated TypeScript runtime does not implement %s; missing %q", behavior, want)
	}
}

func TestGeneratedTypeScriptRuntimeCleansUpTransportFailures(t *testing.T) {
	source := generatedTypeScriptClient(t)
	call := sourceSection(t, source, "protected call<TResp>(", "protected abstract dispatchEvent")

	// The request must not remain in the correlation map when either payload
	// encoding or WebSocket send throws synchronously.
	requireSource(t, call, "encodePayload(payload)", "request payload encoding")
	requireSource(t, call, "this.ws.send(frame)", "request WebSocket send")
	requireSource(t, call, "try {", "request transport failure cleanup")
	requireSource(t, call, "this.pending.delete(id);", "request transport failure cleanup")
	requireSource(t, call, "reject(error);", "request transport failure cleanup")
}

func TestGeneratedTypeScriptRuntimeHandlesMalformedPostConnectFrames(t *testing.T) {
	source := generatedTypeScriptClient(t)
	message := sourceSection(t, source, "private handleMessage(data: Uint8Array): void {", "private handleClose(): void {")
	decodeCatch := sourceSection(t, message, "    } catch {", "    const payload = env.payload;")

	// A malformed frame after setup is a terminal protocol failure, not an
	// ignorable message that can leave RPC promises pending forever.
	requireSource(t, decodeCatch, "this.failAllPending(", "malformed post-connect frame handling")
	requireSource(t, decodeCatch, "this.handleClose();", "malformed post-connect frame handling")
}

func TestGeneratedTypeScriptRuntimeContainsSafeResponseAndEventDispatch(t *testing.T) {
	source := generatedTypeScriptClient(t)
	message := sourceSection(t, source, "private handleMessage(data: Uint8Array): void {", "private handleClose(): void {")
	response := sourceSection(t, message, `case "response": {`, `case "error": {`)
	event := sourceSection(t, message, `case "event":`, `case "connection_error":`)

	// Empty response payloads represent an empty/void response and must not be
	// passed through a decoder that expects a binary value.
	requireSource(t, response, "payload && payload.length > 0 ? p.decode(payload) : undefined", "empty response payload handling")

	// A bad event payload must be isolated to that event. It must not escape the
	// WebSocket message callback and disrupt response processing.
	requireSource(t, event, "try {", "event decode failure handling")
	requireSource(t, event, "this.dispatchEvent(", "event decode failure handling")
	requireSource(t, event, "catch", "event decode failure handling")
}

func TestGeneratedTypeScriptRuntimeTerminatesOnConnectionErrors(t *testing.T) {
	source := generatedTypeScriptClient(t)
	message := sourceSection(t, source, "private handleMessage(data: Uint8Array): void {", "private handleClose(): void {")
	connectionError := sourceSection(t, message, `case "connection_error":`, "default:")

	requireSource(t, connectionError, "this.failAllPending(", "connection-error handling")
	requireSource(t, connectionError, "this.handleClose();", "connection-error handling")
	requireSource(t, connectionError, "ws.close(", "connection-error handling")
}

func TestGeneratedTypeScriptRuntimeDoesNotResolveBeforeConnectionRejectionCanBeObserved(t *testing.T) {
	source := generatedTypeScriptClient(t)
	connect := sourceSection(t, source, "export function connectSocket(", "")

	openAt := strings.Index(connect, "ws.onopen = () =>")
	if openAt < 0 {
		t.Fatalf("generated source missing WebSocket open handler")
	}
	open := connect[openAt:]
	resolveAt := strings.Index(open, "resolve({ ws, bufferedMessages });")
	if resolveAt < 0 {
		t.Fatalf("generated source missing handshake resolution")
	}
	if !strings.Contains(open[:resolveAt], "setTimeout(") {
		t.Errorf("generated TypeScript runtime resolves connect() immediately on open; it must yield before resolving so a raced connection_error can reject setup")
	}

	// The connection-error frame must be inspected before ordinary post-open
	// buffering, otherwise a rejection that arrives with the open transition is
	// converted into a successfully resolved Connected client.
	messageAt := strings.Index(open, "ws.onmessage = (ev) => {")
	if messageAt < 0 {
		t.Fatalf("generated source missing setup message handler")
	}
	message := open[messageAt:]
	errorAt := strings.Index(message, `if (env.type === "connection_error")`)
	bufferAt := strings.Index(message, "if (settled) {")
	if errorAt < 0 || bufferAt < 0 || errorAt > bufferAt {
		t.Errorf("generated TypeScript runtime buffers setup connection_error frames before inspecting them")
	}
}
