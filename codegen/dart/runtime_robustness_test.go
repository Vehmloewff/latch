package dart

import (
	"strings"
	"testing"
)

func generatedDartClient(t *testing.T) string {
	t.Helper()
	files, err := Generate(buildFixtureProtocol(t), Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return string(files["lib/client.dart"])
}

func dartSourceSection(t *testing.T, source, start, end string) string {
	t.Helper()
	startAt := strings.Index(source, start)
	if startAt < 0 {
		t.Fatalf("generated Dart source missing section start %q", start)
	}
	section := source[startAt+len(start):]
	if end == "" {
		return section
	}
	endAt := strings.Index(section, end)
	if endAt < 0 {
		t.Fatalf("generated Dart source missing section end %q after %q", end, start)
	}
	return section[:endAt]
}

func requireDartSource(t *testing.T, source, want, behavior string) {
	t.Helper()
	if !strings.Contains(source, want) {
		t.Errorf("generated Dart runtime does not implement %s; missing %q", behavior, want)
	}
}

func TestGeneratedDartRuntimeCleansUpTransportFailures(t *testing.T) {
	source := generatedDartClient(t)
	call := dartSourceSection(t, source, "Future<TResp> call<TResp>(", "void dispatchEvent")

	// Encoding the request payload and writing it to the socket are both
	// synchronous failure points. Neither may leave an entry in _pending.
	requireDartSource(t, call, "BinaryCodec.encode(payload)", "request payload encoding")
	requireDartSource(t, call, "_channel.sink.add(frame);", "request WebSocket send")
	requireDartSource(t, call, "try {", "request transport failure cleanup")
	requireDartSource(t, call, "_pending.remove(id);", "request transport failure cleanup")
	requireDartSource(t, call, "completeError(error", "request transport failure cleanup")
}

func TestGeneratedDartRuntimeHandlesMalformedPostConnectFrames(t *testing.T) {
	source := generatedDartClient(t)
	message := dartSourceSection(t, source, "void _handleMessage(dynamic data) {", "void _handleClose() {")
	decodeCatch := dartSourceSection(t, message, "    } catch (_) {", "    switch (env.type) {")

	// Malformed binary after setup is a terminal protocol failure. Silently
	// returning here strands every request that was waiting for a response.
	requireDartSource(t, decodeCatch, "_failAllPending(", "malformed post-connect frame handling")
	requireDartSource(t, decodeCatch, "_handleClose();", "malformed post-connect frame handling")
}

func TestGeneratedDartRuntimeHandlesEventDecodeFailures(t *testing.T) {
	source := generatedDartClient(t)
	message := dartSourceSection(t, source, "void _handleMessage(dynamic data) {", "void _handleClose() {")
	event := dartSourceSection(t, message, "      case FrameCode.event:", "      case FrameCode.connectionError:")

	// An invalid event payload must be isolated to the event frame. The stream
	// subscription must remain usable for later responses and events.
	requireDartSource(t, event, "try {", "event decode failure handling")
	requireDartSource(t, event, "dispatchEvent(", "event decode failure handling")
	requireDartSource(t, event, "catch (_) {}", "event decode failure handling")
}

func TestGeneratedDartRuntimeUsesEmptyResponsePayloadAsEmptyValue(t *testing.T) {
	source := generatedDartClient(t)
	message := dartSourceSection(t, source, "void _handleMessage(dynamic data) {", "void _handleClose() {")
	response := dartSourceSection(t, message, "      case FrameCode.response:", "      case FrameCode.error:")

	// The TypeScript runtime treats a zero-length response as an absent value;
	// Dart must do the same instead of asking BinaryCodec to decode no bytes.
	requireDartSource(t, response, "env.payload.isEmpty", "empty response payload handling")
	requireDartSource(t, response, "BinaryCodec.decode(env.payload)", "empty response payload handling")
}

func TestGeneratedDartRuntimeTerminatesOnConnectionErrors(t *testing.T) {
	source := generatedDartClient(t)
	message := dartSourceSection(t, source, "void _handleMessage(dynamic data) {", "void _handleClose() {")
	connectionError := dartSourceSection(t, message, "      case FrameCode.connectionError:", "      default:")

	requireDartSource(t, connectionError, "_failAllPending(", "connection-error handling")
	requireDartSource(t, connectionError, "_handleClose();", "connection-error handling")
	requireDartSource(t, connectionError, "_channel.sink.close(", "connection-error handling")
}

func TestGeneratedDartRuntimeDoesNotResolveBeforeConnectionRejectionCanBeObserved(t *testing.T) {
	source := generatedDartClient(t)
	connect := dartSourceSection(t, source, "Future<HandshakeResult> connectSocket(", "class _PendingRequest")

	// Setup must watch the first frames after the channel becomes ready. A
	// connection_error racing with readiness must reject connect(), not resolve
	// a Connected client that is already closed on its first turn.
	requireDartSource(t, connect, "FrameCode.connectionError", "connection rejection during open")
	requireDartSource(t, connect, "connect_rejected", "connection rejection during open")
	requireDartSource(t, connect, "LatchError", "connection rejection during open")
}
