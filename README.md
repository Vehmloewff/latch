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
    A int `latch:"1"`
    B int `latch:"2"`
}

type AddResponse struct {
    Result int `latch:"1"`
}

type Event struct {
    Kind string `latch:"1"`
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
HTTP server. Connections must include a non-empty `version` query parameter;
the generated clients add `?version=1` automatically from the configured
`ProtocolVersion`. A raw WebSocket client should connect to
`ws://localhost:8080/ws?version=1`.

To run the server, save the snippet as `main.go` in a new Go module and run:

```sh
go mod init example.com/quickstart
go get github.com/vehmloewff/latch
go run .
```

Keep client generation in a separate Go program (or your build tooling), since
it finalizes the same server definition and writes the generated source files.

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
`jsonschema_enum:"a,b,c"` tag. Struct fields use `latch:"N"` for their stable
wire ID and `latch:"N,omitempty"` for optional fields. Generated TypeScript and
Dart field names are derived from the Go field names using lower camel case.

Anonymous structs, `interface{}`/`any`, channels, functions, complex numbers,
`int64`/`uint64`, non-string map keys, and types implementing
`json.Marshaler` are rejected during registration or finalization.

## Development

```sh
go build ./...
go test ./...

# Binary runtime tests
(cd codegen/typescript/binary_runtime && npm ci && npm test)
(cd codegen/dart/binary_runtime && dart pub get && dart test)

# Cross-language example integration tests
go run ./integration_test
```

The binary runtime tests exercise the standalone TypeScript and Dart codecs.
The cross-language runner is separate from `go test`: it regenerates the chat
app example's clients, runs the selected language checks, starts its Go server,
and runs the integration programs. Language arguments are optional: `go`,
`dart`, and `typescript`; with no arguments, all three run. The complete
working example is in [`chat_app_example`](chat_app_example).
