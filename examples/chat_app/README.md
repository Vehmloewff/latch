# Latch chat app example

This directory is a self-contained chat application protocol. It has no frontend:
the Go server provides rooms and message history, while generated Go,
TypeScript, and Dart clients demonstrate how a chat UI or CLI would use it.

The protocol source is `api/api.go`. Generated clients are written to
`golang/`, `typescript/`, and `dart/lib/`.

## Layout

```text
chat_app/
├── api/                         Protocol source of truth
├── cmd/
│   ├── generate_clients/        Client generator
│   └── server/                  WebSocket server
├── golang/
│   ├── client.go                Generated Go client
│   ├── client_test.go           Go protocol tests
│   └── example/main.go          Chat usage story
├── typescript/
│   ├── client.ts                Generated TypeScript client
│   ├── test/protocol.test.ts    TypeScript protocol tests
│   └── main.ts                  Chat usage story
└── dart/
    ├── lib/client.dart          Generated Dart client
    ├── test/protocol_test.dart  Dart protocol tests
    └── main.dart                Chat usage story
```

## Install dependencies

From this directory:

```sh
cd typescript
npm install
cd ../dart
dart pub get
cd ..
```

## Generate clients

```sh
go run ./cmd/generate_clients
```

Run this again whenever `api/api.go` changes. It regenerates the clients but
does not overwrite the handwritten examples or test files.

## Run the server

The server reads `PORT`; it defaults to `8080`.

```sh
go run ./cmd/server
```

The WebSocket endpoint is `ws://localhost:8080/ws`. To use another port:

```sh
PORT=9090 go run ./cmd/server
```

## Chat protocol

A client connects, lists rooms, and joins a room with a user ID. For example,
the calls made by a chat application would be:

```text
chatListRooms
chatJoinRoom(room: "general", userId: "alice")
chatSendMessage(room: "general", senderId: "alice", text: "Hello!")
chatHistory(room: "general")
```

The server emits presence events when users join and message events to every
connection that joined the message's room. The examples walk through two independent connections, Alice and Bob: Alice
sends a message and both connections receive it. Each `main` file contains
narrative comments immediately above the calls they explain, so it can be read
as both a user story and a runnable client walkthrough. Executable assertions
are kept in the protocol tests.

## Run the Go client and tests

The executable walkthrough needs a running server. Start it first, then from this directory:

```sh
SERVER_URL=ws://localhost:8080/ws go run ./golang/example
```

The protocol tests are separate and test binary encoding without requiring a running server:

```sh
go test ./golang
```

The example prints each step of Bob and Alice's chat session.

## Run the TypeScript client and tests

```sh
cd typescript
npm run check
SERVER_URL=ws://localhost:8080/ws npm run example
npm test
```

The tests use Node's built-in test runner and the generated client uses binary
WebSocket frames.

## Run the Dart client and tests

```sh
cd dart
dart analyze lib main.dart test
dart run main.dart
dart test
```

`SERVER_URL` is optional when the server is running on the default port.
