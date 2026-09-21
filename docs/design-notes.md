# Latch v1 design notes

This document records the explicit decisions the spec asked to be made and
documented rather than left ambiguous (see "Questions to resolve during
implementation"). It is updated as each phase lands.

## 0. Current connection and event model

The server is generic over its per-connection application state `S`; the
connection remains non-generic. The one server-to-client event payload is
carried by a generic `Emitter[E]` argument to `OnConnect`.
Applications that have several event variants model them as one tagged `E`
value and send it with `emitter.Send(value)`. There are no named event
registrations and no event-name dispatch field on the wire.

The WebSocket upgrade is the connection handshake. The client sends no
initial setup frame. `OnConnect` runs once after the upgrade and receives a
connection whose `Request()` method returns a copy of the HTTP upgrade
request. `OnConnect` returns `S`, which is passed to every method and to
`OnDisconnect`; state is stored by the server, not on `Conn`. RPC methods are
registered by identifier only; dotted names are rejected so every generated
client can expose methods directly.

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
  normal behavior (a plain JSON number) — Latch does not support a
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
and Latch does not parse Go source or comments. Enums are declared with
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

Latch does not support arbitrary types implementing `json.Marshaler` or
`encoding.TextMarshaler` — their wire shape cannot be safely inferred by
reflection and Latch has no per-type schema-override mechanism in v1.
`time.Time` is the sole explicit exception: it is special-cased to the
JSON Schema `{"type":"string","format":"date-time"}` and to `string` (with
an RFC3339 documentation note) in every generated language, rather than a
JavaScript `Date` or similar — this avoids changing JSON round-trip
semantics silently.

## 4. Outbound queue overflow

`Options.OutboundQueueSize` bounds a per-connection channel. On overflow,
`Conn.Send` (and every other outbound frame) returns an error **and**
the connection is closed (`ClosePolicyViolation`). Latch never silently
drops a typed frame to relieve backpressure.

## 5. Error code naming convention

Wire error codes are `snake_case` strings. Latch reserves:

- `method_not_found`
- `invalid_request`
- `internal_error`
- `protocol_violation`
- `connect_rejected`
- `duplicate_request_id`

Applications are free to use any other code string via `latch.NewError`.

## 6. OnConnect is optional

A `Server[S]` may have zero or one `OnConnect` handler. With one, it runs
after the HTTP upgrade, can inspect `conn.Request()`, send typed `E` events,
and returns the per-connection `S` value. That state is passed to every method
and to the optional `OnDisconnect` callback. Registering a second lifecycle
callback of either kind is a registration-time error.

## 7. Origin checking default

With neither `Options.OriginPatterns` nor `Options.CheckOrigin` set,
Latch relies on `github.com/coder/websocket`'s default behavior, which
only allows same-origin connections (the request's `Origin` header must
match the request host). `OriginPatterns` extends this the same way
`websocket.AcceptOptions.OriginPatterns` does; `CheckOrigin` bypasses it
entirely with a caller-supplied predicate over the raw request.

## 8. Runtime/generated code split

Section 25 of the spec describes a two-package split per language (a small
`@latch/runtime`-style package plus a thin generated package per API).
For v1, Latch generates **one self-contained output directory per
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

Events sent from `OnConnect` can arrive before the generated client's
constructor has finished, because there is no client handshake response. The
runtime therefore buffers frames received before the application subscribes:
before
`BaseConnection`'s constructor (and therefore the generated subclass's own
`readonly events = {...}` field initializers) has even run. The runtime
detects this case (`HandshakeResult.bufferedMessages`) and replays those
frames after construction, and the replay is scheduled with `setTimeout(fn,
0)` — a macrotask — rather than `queueMicrotask`. A microtask would still
run *before* the application's own code immediately following `await
client.connect(...)` (e.g. a synchronous `conn.events.subscribe(...)`
call), because that code is itself just another microtask continuation of
the same `await` and both were queued from within the same synchronous
call stack. Only a macrotask reliably runs after the caller has had a
chance to subscribe. Dart and the Go client will need the equivalent
ordering guarantee once they exist (see the note added to their sections
below when implemented) — this is a wire-protocol-shaped hazard
(the server starts OnConnect immediately after the upgrade), not a
TypeScript-only concern.

## 10. Dart optional vs nullable

Dart's type system has no built-in "present-with-null vs absent" three-state
distinction the way a hand-written serializer could fake in a dynamically
typed language, and adding a wrapper type (`Option<T>`-style) for every
optional field would make the generated API markedly less idiomatic. Per
the spec's own allowance ("if implementing exact absent-vs-null distinction
makes the Dart API excessively awkward, document the compromise"), Latch
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
value in an `unknown` case), Latch throws a typed
`LatchDecodeException` from a generated enum's `fromJson` when the wire
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
environment). Latch's generated `pubspec.yaml` depends on
`package:web_socket_channel` (the standard, actively maintained
cross-platform WebSocket package used by the wider Dart/Flutter ecosystem)
rather than reimplementing per-platform transport. `WebSocketChannel.stream`
is single-subscription (unlike a browser WebSocket's freely-reassignable
`onmessage`), so `connectSocket` creates the one
`StreamSubscription` for the connection and hands it to `BaseConnection`,
which takes over by replacing the
subscription's callbacks (`onData`/`onDone`/`onError`) instead of calling
`.listen()` a second time — which throws at runtime ("Stream has already
been listened to"). This was caught by the generated Dart client's own
integration test, not anticipated up front.

## 13. Connection-time event flood vs. the outbound queue

`OnConnect` can send typed events immediately. Those sends use the same
bounded outbound queue as later sends; on overflow `Conn.Send` returns an
error and closes the connection rather than silently dropping a frame.

## 14. Go client: channels, not callbacks, for events

Per spec section 58 Q7, generated Go events use one channel
(`conn.Events()` returns a `<-chan Event`). The channel is buffered and
delivery is best-effort: once it is full, further events are dropped rather
than blocking the connection's read goroutine. It closes when the connection
closes.

## 15. Go client: no handshake replay-buffer needed

Go's client dials without sending a setup frame and starts its reader only
after generated code registers the one event channel. This prevents an event
sent from `OnConnect` from racing the generated registration.

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
`EventStream`, `LatchError`) and the generated `types.ts`/`client.ts`
are separate files in one output directory. `index.ts` re-exports the
public surface. Method namespaces nest per dotted segment
(`client.billing.invoice.get(...)`); event names are a single flat
camelCase namespace (`conn.events.billingInvoiceUpdated`), matching the
spec's own examples — methods nest, events don't.
