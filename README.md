# Latch

Latch turns a Go-defined WebSocket API into type-safe clients for Go,
TypeScript, and Dart.

Define your request, response, event, and connection-state types in Go. Register
handlers on a `Server`, serve it as an `http.Handler`, and generate clients from
the same server definition. Latch handles protocol reflection, runtime
validation, and client code generation without a separate schema file.

## Features

- Strongly typed RPC methods and server-to-client events
- Go-first API with compile-time handler validation
- Reflection-driven binary encoding and decoding with stable numeric field IDs
- Generated TypeScript, Dart, and Go clients
- Deterministic client output
- No `.proto`, OpenAPI document, or handwritten schema required

## Quick start

Define an API and register its methods:

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/vehmloewff/latch"
)

type State struct {
    UserID string
}

type AddRequest struct {
    A int `json:"a"`
    B int `json:"b"`
}

type AddResponse struct {
    Result int `json:"result"`
}

type Event struct {
    Kind string `json:"kind"`
}

func buildAPI() *latch.Server[State] {
    server := latch.New[State](latch.Options{
        ProtocolVersion: "1",
    })

    server.OnConnect(func(
        ctx context.Context,
        events latch.Emitter[Event],
        conn *latch.Conn,
    ) (State, error) {
        return State{}, nil
    })

    server.Register("math_add", func(
        ctx context.Context,
        state State,
        req AddRequest,
    ) (AddResponse, error) {
        return AddResponse{Result: req.A + req.B}, nil
    })

    return server
}

func main() {
    server := buildAPI()
    http.Handle("/ws", server)
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

A `Server` implements `http.Handler`, so it can be mounted directly on any Go
HTTP server.

## Generate clients

Generate each target from the same server definition. These methods finalize the
server schema once and pass it to the selected code generator:

```go
func main() {
    server := buildAPI()

    if err := server.GenerateTypeScript(latch.TypeScriptOptions{
        OutputDir: "./typescript",
    }); err != nil {
        log.Fatal(err)
    }

    if err := server.GenerateDart(latch.DartOptions{
        OutputDir: "./dart",
        Package:   "latch_client",
    }); err != nil {
        log.Fatal(err)
    }

    if err := server.GenerateGo(latch.GoOptions{
        OutputDir: "./generated/go",
        Package:   "latchclient",
    }); err != nil {
        log.Fatal(err)
    }
}
```

Use `GenerateSchema` when integrating the in-process normalized protocol IR with custom generators.

## Generated clients

The basic example's complete cross-language check is intentionally a standalone
command rather than part of `go test`:

```sh
go run ./cmd/cross-language-test
```

Language arguments are optional: `go`, `dart`, and `typescript`. With no
arguments all languages run; for example:

```sh
go run ./cmd/cross-language-test typescript dart
```

The command regenerates the clients, runs static checks for the selected
languages, starts the Go server when needed, then runs the selected integration
tests.

TypeScript:

```ts
import { LatchClient } from "./typescript";

const client = new LatchClient({ url: "ws://localhost:8080/ws" });
const conn = await client.connect();
const result = await conn.mathAdd({ a: 1, b: 2 });

result.result; // number
```

Dart:

```dart
final client = LatchClient(
  ClientOptions(Uri.parse('ws://localhost:8080/ws')),
);
final conn = await client.connect();
final result = await conn.mathAdd(AddRequest(a: 1, b: 2));

print(result.result);
```

Go:

```go
client := latchclient.New("ws://localhost:8080/ws")
conn, err := client.Connect(ctx)
if err != nil {
    log.Fatal(err)
}

result, err := conn.MathAdd(ctx, latchclient.AddRequest{A: 1, B: 2})
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Result)
```

Method names are registered in `snake_case` and generated in each client
language's conventional style. For example, `math_add` becomes `mathAdd` in
TypeScript and Dart, and `MathAdd` in Go. Request types, response types, and
event payloads are generated from the server definition, so client code never
needs to manually cast responses or maintain a second copy of the protocol.

## Supported types

Latch supports named exported structs, strings, booleans, signed and
unsigned integers up to 32 bits, `float32`/`float64`, slices, arrays,
`map[string]V`, pointers, `time.Time`, and named string enums declared with a
`jsonschema_enum:"a,b,c"` tag.

Anonymous structs, `interface{}`/`any`, channels, functions, complex numbers,
`int64`/`uint64`, non-string map keys, and types implementing
`json.Marshaler` are rejected during registration or finalization.

## Development

```sh
go build ./...
go test ./...
go run ./cmd/cross-language-test
```

The cross-language runner uses the generated clients with the available
TypeScript, Dart, and Go toolchains. The complete working example is in
[`examples/chat_app`](examples/chat_app).
