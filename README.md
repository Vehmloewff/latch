# Latchwire

Latchwire turns a Go-defined WebSocket API into fully type-safe Go,
TypeScript, and Dart clients.

You write Go structs and Go handler functions. Latchwire reflects over your
registrations once, builds a normalized protocol description, generates
JSON Schema from it, validates every connection and request against that
schema at runtime, and generates a client for each target language whose
API is exactly as strongly typed as your Go server. There is no `.proto`
file, no OpenAPI document, no hand-maintained JSON Schema, and no
`go generate` — the Go server is the only handwritten protocol definition.

```
Go types + registered handlers
              │ reflection (once, at finalize)
              ▼
       Latchwire protocol IR
        ╱      │       ╲
  JSON Schema  │        ╲
       │       ▼         ▼
       │   TypeScript   Dart / Go client
       ▼
 runtime validation
 (every connect + request)
```

## The smallest complete example

```go
type ConnectParams struct {
    Token string `json:"token"`
}

type AddRequest struct {
    A int `json:"a"`
    B int `json:"b"`
}

type AddResponse struct {
    Result int `json:"result"`
}

type Tick struct {
    Value int `json:"value"`
}

func BuildAPI() *latchwire.Server[ConnectParams] {
    lw := latchwire.New[ConnectParams](latchwire.Options{
        ProtocolName:    "demo",
        ProtocolVersion: "1",
    })

    tick := latchwire.Event[Tick]("tick")
    lw.RegisterEvent(tick)

    lw.OnConnect(func(ctx context.Context, conn *latchwire.Conn[ConnectParams]) error {
        go func() {
            ticker := time.NewTicker(time.Second)
            defer ticker.Stop()
            for {
                select {
                case <-conn.Done():
                    return
                case t := <-ticker.C:
                    _ = tick.Send(conn, Tick{Value: t.Second()})
                }
            }
        }()
        return nil
    })

    err := lw.Register("math.add", func(
        ctx context.Context,
        conn *latchwire.Conn[ConnectParams],
        req AddRequest,
    ) (AddResponse, error) {
        return AddResponse{Result: req.A + req.B}, nil
    })
    if err != nil {
        log.Fatal(err)
    }

    return lw
}
```

Serve it:

```go
func main() {
    lw := BuildAPI()
    http.Handle("/ws", lw)
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

Generate clients from the exact same registration — no separate schema, no
`go generate`, just a plain Go program:

```go
func main() {
    lw := BuildAPI()
    err := lw.Generate(latchwire.GenerateOptions{
        TypeScript: &latchwire.TypeScriptOptions{OutputDir: "./generated/typescript"},
        Dart:       &latchwire.DartOptions{OutputDir: "./generated/dart", Package: "demo_client"},
        Go:         &latchwire.GoOptions{OutputDir: "./generated/go", Package: "democlient"},
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

### The generated TypeScript client

```ts
import { DemoClient } from "./generated/typescript";

const client = new DemoClient({ url: "ws://localhost:8080/ws" });
const conn = await client.connect({ token: "abc" });

const result = await conn.math.add({ a: 1, b: 2 });
result.result; // number, statically known

conn.events.tick.subscribe((event) => {
  event.value; // number, statically known
});
```

### The generated Dart client

```dart
final client = DemoClient(ClientOptions(Uri.parse('ws://localhost:8080/ws')));
final conn = await client.connect(ConnectParams(token: 'abc'));

final result = await conn.math.add(AddRequest(a: 1, b: 2));
print(result.result);

conn.events.tick.listen((event) {
  print(event.value);
});
```

### The generated Go client

```go
client := democlient.New("ws://localhost:8080/ws")
conn, err := client.Connect(ctx, democlient.ConnectParams{Token: "abc"})
if err != nil {
    log.Fatal(err)
}

result, err := conn.Math.Add(ctx, democlient.AddRequest{A: 1, B: 2})
fmt.Println(result.Result)

for tick := range conn.Events.Tick() {
    fmt.Println(tick.Value)
}
```

If a client developer ever has to manually type an event name, cast a
response, inspect `any`/`dynamic`, deserialize a raw map, or hand-maintain a
schema to keep it in sync with the server, that's a bug in Latchwire, not a
missing feature in the generated code.

## How it fits together

1. **Define Go types** for your connect payload, every method's
   request/response, and every event's payload. Each must be a named,
   exported struct (see [Supported types](#supported-go-type-system)).
2. **Create a server**: `latchwire.New[ConnectParams](latchwire.Options{...})`.
3. **Register methods**: `lw.Register("user.get", handler)`, where `handler`
   has the one canonical signature
   `func(context.Context, *latchwire.Conn[C], Request) (Response, error)`.
   The signature is validated immediately, not on the first request.
4. **Declare and register events**: `evt := latchwire.Event[T]("name")`,
   then `lw.RegisterEvent(evt)`.
5. **Add `OnConnect`** (optional) for authentication, subscriptions, or any
   other per-connection setup. `conn.Params()` is already schema-validated
   and decoded by the time your handler runs.
6. **Serve it**: a `*latchwire.Server[C]` is an `http.Handler`.
7. **Generate clients**: `lw.Generate(latchwire.GenerateOptions{...})` — a
   plain function call from a plain Go program (see
   `examples/basic/gen`), never `go generate`.
8. **Call the generated APIs** from TypeScript, Dart, or Go, exactly as
   shown above.

## Wire protocol

JSON text frames over a WebSocket, one discriminated envelope shape for
every direction: `connect` → `connected`, `request` → `response`/`error`,
and server-initiated `event`. A connection must send `connect` before
anything else; the server validates that payload against generated JSON
Schema, decodes it, runs `OnConnect`, and only then sends `connected`,
followed by any events your `OnConnect` sent (they're buffered until then —
`connected` always arrives first). See `wire/envelope.go` for the
exact frame shapes.

## Supported Go type system

Structs (named, exported fields only), `string`, `bool`, signed/unsigned
integers up to 32 bits, `float32`/`float64`, slices, arrays, `map[string]V`,
pointers (nullable fields), `time.Time` (RFC3339 on the wire), and named
string types with an explicit `jsonschema_enum:"a,b,c"` tag on at least one
usage. Anonymous structs, `interface{}`/`any`, channels, funcs, complex
numbers, `int64`/`uint64`, non-string map keys, and types implementing
`json.Marshaler` are rejected at registration/finalization time with a
descriptive error — Latchwire never silently generates an incorrect client
type. See [`docs/design-notes.md`](docs/design-notes.md) for the exact
reasoning behind each of these choices, including the required/optional/
nullable semantics (`json:"name"` vs `,omitempty` vs pointer vs both).

## Repository layout

```
server.go, conn.go, method.go, event.go,
errors.go, protocol.go, generate.go    Public API (small and intentional)
client/                                Public Go client runtime
reflectapi/                            The one reflection → IR stage
protocol/                              The normalized IR
jsonschema/                            IR → JSON Schema, + runtime validation
codegen/{typescript,dart,golang}/      Per-language generators
names/                                 Shared naming/namespacing helpers
wire/                                  Wire envelope types
testutil/                              Handwritten test WebSocket client
cmd/latchwire/                         CLI: generate from an exported manifest
examples/basic/                        A complete worked example, all 3 languages
tests/integration/                     Cross-language integration suite
docs/design-notes.md                   Every non-obvious decision, written down
```

Every package here is public — none of this lives under `internal/`. Both
the codegen generators and their `protocol.Protocol` IR input need to be
importable so a project can build its own small CLI or tooling around them
(the same way `cmd/latchwire` does), and keeping the rest of the pipeline
(`reflectapi`, `jsonschema`, `names`, `wire`) alongside them avoids an
arbitrary, hard-to-predict split between what's "core" and what's
"internal."

## Generating clients: two ways

**Programmatic (primary, recommended)** — call `Server.Generate` from a
plain Go program that builds the same server your application serves (see
`examples/basic/gen/main.go`). This is the only way that actually reflects
over your types; nothing about Latchwire parses Go source.

**CLI, from an exported manifest (secondary)** — `Server.WriteManifest`
writes a complete, versioned JSON description of your protocol (including
its full IR, not just derived JSON Schema). The `latchwire` CLI can then
generate clients from that file alone, without your server's source
available:

```sh
latchwire generate --manifest latchwire.json \
  --typescript ./gen/ts --dart ./gen/dart --go ./gen/go
```

This is useful for a CI step or generating against a manifest published by
a running service, but it's still downstream of the same reflection your
own program performs — the CLI itself never reflects over arbitrary Go
source.

## Testing this repository

```sh
go build ./...
go test ./...                    # unit + wire-protocol tests, race-clean
go test ./tests/integration/...  # cross-language suite (skips a language
                                  # gracefully if its toolchain isn't set up —
                                  # see below)
```

The cross-language suite drives the *same* generated example server through
real `tsc`/Node, `dart analyze`/`dart test`, and plain `go build`/`go test`
toolchains — not mocks. To exercise all three:

```sh
cd examples/basic/generated/typescript && npm install
cd examples/basic/generated/dart && dart pub get
```

(Go needs nothing extra — it's already part of this module.)

## What's out of scope for v1

Binary encodings, gRPC/protobuf, HTTP fallback, durable event replay or
delivery acknowledgements, streaming RPC, client-initiated events, session
resumption, and automatic reconnection are all explicitly out of scope —
see `docs/design-notes.md` for the reasoning and for what's designed to be
addable later without a breaking change to the wire protocol.
