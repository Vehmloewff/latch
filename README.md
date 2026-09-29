# Latch

Latch turns a Go-defined WebSocket API into type-safe clients for Go,
TypeScript, Dart, and Swift.

Define your request, response, event, and connection-state types in Go. Register
handlers on a `Server`, serve it as an `http.Handler`, and generate clients from
the same server definition. Latch handles protocol reflection, runtime
validation, and client code generation without a separate schema file.

## Features

- Strongly typed RPC methods and server-to-client events
- Go-first API with compile-time handler validation
- Reflection-driven binary encoding and decoding with stable numeric field IDs
- Generated TypeScript, Dart, Go, and Swift clients
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

    if err := server.GenerateKotlin(latch.KotlinOptions{
        OutputDir: "./kotlin",
    }); err != nil {
        log.Fatal(err)
    }

    if err := server.GenerateSwift(latch.SwiftOptions{
        OutputDir: "./swift",
    }); err != nil {
        log.Fatal(err)
    }

    if err := server.GenerateCSharp(latch.CSharpOptions{
        OutputDir: "./csharp",
    }); err != nil {
        log.Fatal(err)
    }
}
```

The Kotlin/JVM target generates a standalone `LatchClient.kt` using the JDK WebSocket client (Java 11+) and Kotlin standard library. Connect with `LatchClient(url, onEvent = { event -> /* handle event */ }).connect()`, call typed methods on the returned connection, and call `close()` when done. Pass `onConnectionStateChange` to observe connection lifecycle changes. Run `go test ./codegen/kotlin -v` with `kotlinc` and Java installed for binary and socket tests; `go run ./integration_test kotlin` also generates the example client.

The C# target generates a standalone `LatchClient.cs` for .NET 8+ with no NuGet dependencies. Connect with `await new LatchClient(url, onEvent: e => Console.WriteLine(e)).ConnectAsync()`, call typed `*Async` methods on the returned connection, and dispose it with `await using`. The constructor also accepts optional `onConnectionStateChange` and `onEventError` callbacks. Run `go test ./codegen/csharp -v` with the .NET SDK installed to compile and exercise binary and WebSocket tests.

Use `GenerateSchema` when integrating the in-process normalized protocol IR with custom generators.

## Generated clients

TypeScript:

```ts
import { LatchClient } from "./typescript";

const client = new LatchClient({
  url: "ws://localhost:8080/ws",
  onEvent: event => console.log(event),
  onConnectionStateChange: state => console.log(state), // optional
});
const conn = await client.connect();
const result = await conn.mathAdd({ a: 1, b: 2 });

result.result; // number
```

Dart:

```dart
final client = LatchClient(
  ClientOptions(Uri.parse('ws://localhost:8080/ws')),
  onEvent: (event) => print(event),
  onConnectionStateChange: (state) => print(state), // optional
);
final conn = await client.connect();
final result = await conn.mathAdd(AddRequest(a: 1, b: 2));

print(result.result);
```

Swift (generated into the `LatchClient` Swift package, using Swift concurrency and Foundation's WebSocket API):

```swift
import Foundation
import LatchClient

let client = LatchClient(url: URL(string: "ws://localhost:8080/ws")!, onEvent: { event in print(event) })
let connection = try await client.connect()
let result = try await connection.chatSendMessage(SendMessageRequest(room: "general", senderId: "user", text: "hello"))
print(result.message.text)

```

Go:

```go
client := latchclient.New("ws://localhost:8080/ws", func(event latchclient.Event) {
    fmt.Println(event)
})
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

Generated clients automatically retry failed connections about every two seconds. The connection-state callback reports `connecting`, `offline`, and `connected` as the connection changes. A client that has connected once remains usable after a disconnect: new RPC calls made while offline wait in an unbounded queue and run when the connection returns. Requests already sent on a failed connection are **not** replayed, since replaying an action could apply it twice; those calls fail and the caller can decide whether to retry. Explicitly closing or disposing the client stops reconnecting and fails queued calls. An initial `connect()` also keeps retrying until it succeeds (or is cancelled where the language API supports cancellation).

## Supported types

Latch supports named exported structs, strings, booleans, signed and
unsigned integers up to 32 bits, `float32`/`float64`, slices, arrays,
`map[string]V`, pointers, `time.Time`, and named string enums declared with a
`jsonschema_enum:"a,b,c"` tag. Every exported field in a protocol struct must
have a `latch:"N"` tag with a positive, stable wire ID; use
`latch:"N,omitempty"` for optional fields. Missing field numbers panic during
wire encoding or decoding. Generated TypeScript and Dart field names are derived from the Go field names using lower camel case. Swift clients use Foundation and Swift concurrency, and expose RPC methods as `async throws` functions with events delivered to the constructor callback. All generated clients expose `connecting`, `connected`, and `offline` connection states through an optional constructor callback.

Anonymous structs, `interface{}`/`any`, channels, functions, complex numbers,
`int64`/`uint64`, non-string map keys, and types implementing
`json.Marshaler` are rejected during registration or finalization.

## Development

```sh
go build ./...
go test ./...
go run ./integration_test swift # starts the server and runs every Swift test

# Full integration runner: installs TypeScript/Dart dependencies, runs both
# standalone binary-runtime suites, regenerates clients, and runs the example
go run ./integration_test
```

The cross-language runner is separate from `go test`: it installs the selected
language dependencies, runs the standalone TypeScript and Dart binary-runtime
tests, regenerates the chat app example's clients, runs the selected language
checks, starts its Go server, and runs the integration programs. Run
`go run ./integration_test swift` to run SwiftLint and `swift test`, including
its WebSocket integration test against the Go server. Running `swift test` from
the package directory without `SERVER_URL` fails the server-dependent test rather
than silently skipping it. Runner arguments are optional: `go`, `dart`,
`typescript`, `kotlin`, and `swift`; with no arguments, all five run. The complete working example is in
[`chat_app_example`](chat_app_example).
