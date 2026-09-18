# Latchwire v1 design notes

This document records the explicit decisions the spec asked to be made and
documented rather than left ambiguous (see "Questions to resolve during
implementation"). It is updated as each phase lands.

## 1. Go integer -> TypeScript mapping

`int`, `int8`, `int16`, `int32`, `uint`, `uint8`, `uint16`, `uint32`,
`float32`, `float64` all map to TypeScript `number`.

`int64` and `uint64` are **rejected at the reflection stage** (see
`reflectapi/registry.go`), for every generated language, not just
TypeScript. The reasons:

- A `int64`/`uint64` value can exceed `Number.MAX_SAFE_INTEGER`, so mapping
  it to TS `number` is unsafe.
- Mapping it to TS `string` instead would require the Go server to encode
  it as a JSON string on the wire, which diverges from `encoding/json`'s
  normal behavior (a plain JSON number) — Latchwire does not support a
  custom per-field wire-encoding policy in v1.
- Because Go is the single source of truth reflected once into one IR
  shared by all three generators, a type must be either valid or invalid
  for *all* generated languages at once — it would be inconsistent to allow
  `int64` because Dart and the Go client can represent it exactly, while
  quietly generating unsound TypeScript for the same field.

Applications needing 64-bit integers should use `int32`, a `string`-typed
field (e.g. for opaque IDs), or split the value.

## 2. Enum declaration convention

Go has no source-level way to declare "the values of this named type,"
and Latchwire does not parse Go source or comments. Enums are declared with
an explicit struct-tag on (at least one) field that uses them:

```go
type Status string

type Widget struct {
    Status Status `json:"status" jsonschema_enum:"pending,active,disabled"`
}
```

A named string type used **without** a `jsonschema_enum` tag anywhere is
treated as a plain `string` on the wire and in every generated language —
this matches Go's own semantics, since `type Status string` places no
runtime restriction on its values either.

If the same Go type is tagged with different value lists in different
places, that is a registration-time error. If a type is used once without
the tag and again with it, whichever field the reflector resolves *first*
wins; keep the tag consistent across every field that uses the type.

Numeric enums are out of scope for v1 (the spec explicitly limits enums to
named string types).

## 3. Custom JSON marshalers

Latchwire does not support arbitrary types implementing `json.Marshaler` or
`encoding.TextMarshaler` — their wire shape cannot be safely inferred by
reflection and Latchwire has no per-type schema-override mechanism in v1.
`time.Time` is the sole explicit exception: it is special-cased to the
JSON Schema `{"type":"string","format":"date-time"}` and to `string` (with
an RFC3339 documentation note) in every generated language, rather than a
JavaScript `Date` or similar — this avoids changing JSON round-trip
semantics silently.

## 4. Outbound queue overflow

`Options.OutboundQueueSize` bounds a per-connection channel. On overflow,
`EventDef.Send` (and every other outbound frame) returns an error **and**
the connection is closed (`ClosePolicyViolation`). Latchwire never silently
drops a typed frame to relieve backpressure.

## 5. Report errors

Error and `connection_error` frames carry a single safe message string. They
never expose protocol-specific codes or application error structs.

Go handlers and `OnConnect` callbacks return `report.Err`. Wrap reports with
operation context and attach relevant diagnostic data with `Dump`; use
`Internal` for implementation details that must not reach the client. Latchwire
uses `UserMessage` for the wire string and records the full report in the
OpenTelemetry span.

## 6. OnConnect is optional

A `Server[C]` may have zero or one `OnConnect` handler. With none, every
connect payload that passes JSON Schema validation is accepted
unconditionally. Registering a second `OnConnect` is a registration-time
error.

## 7. Origin checking default

With neither `Options.OriginPatterns` nor `Options.CheckOrigin` set,
Latchwire relies on `github.com/coder/websocket`'s default behavior, which
only allows same-origin connections (the request's `Origin` header must
match the request host). `OriginPatterns` extends this the same way
`websocket.AcceptOptions.OriginPatterns` does; `CheckOrigin` bypasses it
entirely with a caller-supplied predicate over the raw request.

## 8. Runtime/generated code split

Section 25 of the spec describes a two-package split per language (a small
`@latchwire/runtime`-style package plus a thin generated package per API).
For v1, Latchwire generates **one self-contained output directory per
language** that still separates runtime-shaped code (transport, request
correlation, error types) into its own file(s) from protocol-specific
generated code (DTOs, method/event trees, the client class) — but does not
yet publish the runtime half as an independently versioned package. This
keeps `go get`/`npm install`/`pub get` unnecessary for consuming generated
output while preserving the intended architecture; splitting into separate
versioned packages is a natural, non-breaking follow-up.

## 9. TypeScript event callback scheduling

`EventStream.subscribe` listener callbacks are always invoked via
`queueMicrotask`, never synchronously from inside the WebSocket message
handler. This keeps a slow or throwing listener from ever blocking the
socket's read loop, and keeps listener execution order the same relative to
other promise-based work in the app.

There is one deliberate exception, and it was found by the end-to-end
integration test rather than anticipated up front: events sent from
`OnConnect` on the Go server are buffered server-side and flushed
immediately after `"connected"`, which means they can arrive in the very
same underlying WebSocket read as the handshake response — before
`BaseConnection`'s constructor (and therefore the generated subclass's own
`readonly events = {...}` field initializers) has even run. The runtime
detects this case (`HandshakeResult.bufferedMessages`) and replays those
frames after construction, but the replay is scheduled with `setTimeout(fn,
0)` — a macrotask — rather than `queueMicrotask`. A microtask would still
run *before* the application's own code immediately following `await
client.connect(...)` (e.g. a synchronous `conn.events.x.subscribe(...)`
call), because that code is itself just another microtask continuation of
the same `await` and both were queued from within the same synchronous
call stack. Only a macrotask reliably runs after the caller has had a
chance to subscribe. Dart and the Go client will need the equivalent
ordering guarantee once they exist (see the note added to their sections
below when implemented) — this is a wire-protocol-shaped hazard
(the server explicitly documents that "connected" always precedes buffered
events), not a TypeScript-only concern.

## 10. Dart optional vs nullable

Dart's type system has no built-in "present-with-null vs absent" three-state
distinction the way a hand-written serializer could fake in a dynamically
typed language, and adding a wrapper type (`Option<T>`-style) for every
optional field would make the generated API markedly less idiomatic. Per
the spec's own allowance ("if implementing exact absent-vs-null distinction
makes the Dart API excessively awkward, document the compromise"), Latchwire
collapses **optional** (Go `,omitempty`) and **nullable** (Go pointer) into
the same representation: a nullable Dart field (`T?`), where `null` means
"absent OR explicitly null" — the two are indistinguishable from generated
Dart code. Concretely:

- required, non-null (`Name string`) -> `final String name;`, required
  constructor parameter, key always present in `toJson()`.
- optional, non-null (`Name string ,omitempty`) -> `final String? name;`,
  optional constructor parameter; `toJson()` omits the key when `null`.
- required, nullable (`Name *string`) -> `final String? name;`, required
  constructor parameter; `toJson()` always includes the key, value possibly
  `null`.
- optional, nullable (`Name *string ,omitempty`) -> `final String? name;`,
  optional constructor parameter; `toJson()` omits the key when `null` (so
  Dart code can never deliberately send an explicit `null` for a field that
  is also optional — it can only omit it, which the JSON Schema always
  accepts since the field is optional either way).

This is implemented once, precisely, in `codegen/dart/types.go`
(`fieldDecodeExpr` / `fieldEncodeStatement`), not left to chance per field.

## 11. Dart unknown enum values

Per spec section 23's two sanctioned options (throw, or preserve the raw
value in an `unknown` case), Latchwire throws a typed
`LatchwireDecodeException` from a generated enum's `fromJson` when the wire
value doesn't match any known case. The alternative — an `unknown` case
carrying the raw string — isn't achievable with real Dart (enhanced) enums,
whose instances are a fixed, const set; only a class-based union could carry
arbitrary per-instance data, which would sacrifice exhaustive `switch`
checking on the enum, a bigger idiomatic loss than throwing on an unexpected
value. Never a bare/opaque cast: the exception is always a typed, catchable
`Exception` naming the enum and the bad value.

## 12. Dart transport dependency

Dart has no single WebSocket API that works unmodified across the VM,
Flutter, and web (unlike a global `WebSocket` in every JS-hosted
environment). Latchwire's generated `pubspec.yaml` depends on
`package:web_socket_channel` (the standard, actively maintained
cross-platform WebSocket package used by the wider Dart/Flutter ecosystem)
rather than reimplementing per-platform transport. `WebSocketChannel.stream`
is single-subscription (unlike a browser WebSocket's freely-reassignable
`onmessage`), so the handshake in `connectSocket` keeps its one
`StreamSubscription` alive across the handshake/live-connection boundary and
hands it to `BaseConnection`, which takes over by replacing the
subscription's callbacks (`onData`/`onDone`/`onError`) instead of calling
`.listen()` a second time — which throws at runtime ("Stream has already
been listened to"). This was caught by the generated Dart client's own
integration test, not anticipated up front.

## 13. Connect-time event flood vs. the outbound queue

`OnConnect` may spawn a goroutine that calls `EventDef.Send` before
`OnConnect` itself returns (the documented buffering behavior — see §5 of
the spec, "Event sends during OnConnect"). That goroutine and the
handshake's own `flushAfterConnect` both serialize through `Conn.sendMu`,
but which one observes `connectedSent == true` first is a genuine race:
either the flood keeps buffering into `pendingEvents` (unbounded, since
buffering never fails) until `flushAfterConnect` runs and then overflows
the bounded outbound queue *while replaying the buffer*, or
`flushAfterConnect` wins first and the flood overflows the queue directly
afterward. Both are correct outcomes — the outbound-queue-overflow policy
(§7 of the spec: close rather than silently drop) is upheld either way —
but they differ in whether the client ever receives the `"connected"`
frame before the connection terminates. A test that floods sends from
`OnConnect` with a tiny `OutboundQueueSize` (as
`TestOutboundQueueOverflowClosesConnection` does, deliberately, to force
overflow quickly) must therefore only assert the guarantee that holds
regardless of which side of the race wins: the connection terminates
rather than hanging or dropping frames silently forever. It must not assume
`"connected"` is always readable first, because under this specific
adversarial pattern it sometimes legitimately isn't.

## 14. Go client: channels, not callbacks, for events

Per spec section 58 Q7, generated Go events use channels
(`conn.Events.MessageReceived()` returns a `<-chan MessageReceived`), matching
the spec's own illustrative example (`for message := range
conn.Events.MessageReceived() {...}`) and idiomatic Go. Each event's channel
is buffered (capacity 64) and delivery is best-effort: if the consumer
isn't keeping up, once the buffer is full, further events for that stream
are silently dropped rather than blocking the connection's one read
goroutine — the same "events are best-effort" policy already documented for
the server and the other two languages. Channels are closed when the
connection closes, so a `for range` loop terminates cleanly rather than
blocking forever. This avoids spawning one extra goroutine per event
stream, matching the spec's "avoid one goroutine per event unless
necessary."

## 15. Go client: no handshake replay-buffer needed

TypeScript and Dart both need a "replay buffered messages" mechanism (see
§9, §12) because their transports deliver frames via callbacks/streams that
are already active — the same underlying batch of bytes can contain both
the handshake response and a subsequent event, delivered before the
caller's own code can install a permanent handler. Go's client avoids this
entirely by construction: `client.Connect` performs the handshake with a
single synchronous `ws.Read` loop and returns *before* starting any
background reader. Generated code registers every event handler (via
`client.RegisterEvent`) and only then calls `conn.Start()`, which is what
launches the goroutine that calls `ws.Read` again. Any frame the server
sent immediately after `"connected"` simply hasn't been read off the
socket yet at that point — there is nothing to buffer or replay, because
nothing has raced ahead of anything else. This is a direct consequence of
Go's pull-based, one-frame-per-`Read()`-call transport API, as opposed to
JavaScript/Dart's push-based callback/stream model.

## 16. Dart: zero-field structs need a plain constructor

A struct with no fields (a request type with no meaningful payload, e.g.
`type ListRoomsRequest struct{}`) is completely ordinary in Go, TypeScript,
and Go-client output, but Dart's named-parameter constructor syntax
(`Foo({...})`) does not permit an *empty* `{}` group — it's a syntax error
("Expected an identifier"), not merely an unusual style. The generator
special-cases zero-field structs to emit a plain no-argument constructor
(`Foo();`) instead. Caught by `dart analyze` on the generated example after
adding a third, field-less example method — not anticipated up front.

## 17. TypeScript runtime/generated split

Per §8, `runtime.ts` (transport, handshake, request correlation,
`EventStream`, `LatchwireError`) and the generated `types.ts`/`client.ts`
are separate files in one output directory. `index.ts` re-exports the
public surface. Method namespaces nest per dotted segment
(`client.billing.invoice.get(...)`); event names are a single flat
camelCase namespace (`conn.events.billingInvoiceUpdated`), matching the
spec's own examples — methods nest, events don't.
