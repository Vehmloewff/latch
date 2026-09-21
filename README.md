# Latchwire

Latchwire turns a Go-defined WebSocket API into type-safe clients for Go,
TypeScript, and Dart.

Define your request, response, event, and connection-state types in Go. Register
handlers on a `Server`, serve it as an `http.Handler`, and generate clients from
the same server definition. Latchwire handles protocol reflection, runtime
validation, and client code generation without a separate schema file.

## Features

- Strongly typed RPC methods and server-to-client events
- Go-first API with compile-time handler validation
- Runtime JSON Schema validation for requests and events
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

func buildAPI() *latchwire.Server[State] {
    server := latchwire.New[State](latchwire.Options{
        ProtocolVersion: "1",
    })

    server.OnConnect(func(
        ctx context.Context,
        events latchwire.Emitter[Event],
        conn *latchwire.Conn,
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

    if err := server.GenerateTypeScript(latchwire.TypeScriptOptions{
        OutputDir: "./generated/typescript",
    }); err != nil {
        log.Fatal(err)
    }

    if err := server.GenerateDart(latchwire.DartOptions{
        OutputDir: "./generated/dart",
        Package:   "latchwire_client",
    }); err != nil {
        log.Fatal(err)
    }

    if err := server.GenerateGo(latchwire.GoOptions{
        OutputDir: "./generated/go",
        Package:   "latchwireclient",
    }); err != nil {
        log.Fatal(err)
    }
}
```

Use `GenerateSchema` when integrating the normalized schema with custom tooling.

## Generated clients

TypeScript:

```ts
import { LatchwireClient } from "./generated/typescript";

const client = new LatchwireClient({ url: "ws://localhost:8080/ws" });
const conn = await client.connect();
const result = await conn.mathAdd({ a: 1, b: 2 });

result.result; // number
```

Dart:

```dart
final client = LatchwireClient(
  ClientOptions(Uri.parse('ws://localhost:8080/ws')),
);
final conn = await client.connect();
final result = await conn.mathAdd(AddRequest(a: 1, b: 2));

print(result.result);
```

Go:

```go
client := latchwireclient.New("ws://localhost:8080/ws")
conn, err := client.Connect(ctx)
if err != nil {
    log.Fatal(err)
}

result, err := conn.MathAdd(ctx, latchwireclient.AddRequest{A: 1, B: 2})
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

Latchwire supports named exported structs, strings, booleans, signed and
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
go test ./tests/integration/...
```

The cross-language integration suite uses the generated clients with the
available TypeScript, Dart, and Go toolchains. The complete working example is
in [`examples/basic`](examples/basic).
