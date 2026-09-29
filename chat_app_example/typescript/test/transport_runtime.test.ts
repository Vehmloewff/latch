import { strict as assert } from "node:assert";
import { test } from "node:test";
import {

  ConnectedLatchClient,
  LatchClient,
  LatchError,
  ConnectionState,
  type Event,
  __latchWireTypes,
  decodeEnvelope,
  encodeEnvelope,
  encodeTyped,
  type WebSocketLike,
} from "../client.ts";

class FakeWebSocket implements WebSocketLike {
  readonly url: string;
  readonly sent: Uint8Array[] = [];
  readonly closeCalls: Array<{ code?: number; reason?: string }> = [];
  readyState = 0;
  binaryType: string | undefined;
  onopen: ((ev: unknown) => void) | null = null;
  onclose: ((ev: unknown) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: ArrayBuffer | Uint8Array }) => void) | null = null;

  constructor(url: string) {
    this.url = url;
  }

  send(data: Uint8Array): void {
    this.sent.push(new Uint8Array(data));
  }

  close(code?: number, reason?: string): void {
    this.closeCalls.push({ code, reason });
    this.readyState = 3;
    this.onclose?.({});
  }

  open(): void {
    this.readyState = 1;
    this.onopen?.({});
  }

  receive(data: ArrayBuffer | Uint8Array): void {
    this.onmessage?.({ data });
  }

  remoteClose(): void {
    this.readyState = 3;
    this.onclose?.({});
  }
}

function connectWith(socket: FakeWebSocket, url = "ws://fake.test/chat", onEvent: (event: Event) => void = () => {}, onConnectionStateChange?: (state: ConnectionState) => void):
  Promise<ConnectedLatchClient> {
  return new LatchClient({
    url,
    onEvent,
    onConnectionStateChange,
    webSocketFactory: (actualUrl) => {
      assert.equal(actualUrl, socket.url);
      return socket;
    },
  }).connect();
}

function responseFrame(id: string, value: unknown, type: string): Uint8Array {
  return encodeEnvelope({
    type: "response",
    id,
    payload: encodeTyped(value, { kind: "named", name: type }, __latchWireTypes),
  });
}

function requestOn(socket: FakeWebSocket): ReturnType<typeof decodeEnvelope> {
  assert.equal(socket.sent.length, 1);
  return decodeEnvelope(socket.sent[0]);
}

test("connect appends the protocol version and replays an event buffered before setup", async () => {
  const socket = new FakeWebSocket("ws://fake.test/chat?existing=1&version=1");
  let resolveEvent!: (event: Event) => void;
  const event = new Promise<Event>((resolve) => { resolveEvent = resolve; });
  const connecting = connectWith(socket, "ws://fake.test/chat?existing=1", resolveEvent);
  assert.equal(socket.binaryType, "arraybuffer");

  socket.open();
  socket.receive(encodeEnvelope({
    type: "event",
    event: "chat.message",
    payload: encodeTyped(
      { kind: "presence", presence: { room: "general", userId: "bob", online: true } },
      { kind: "named", name: "Event" },
      __latchWireTypes,
    ),
  }));

  const client = await connecting;
  assert.deepEqual(await event, {
    kind: "presence",
    presence: { room: "general", userId: "bob", online: true },
  });
});

test("request and response frames correlate by ID and use generated typed APIs", async () => {
  const socket = new FakeWebSocket("ws://fake.test/chat?version=1");
  const connecting = connectWith(socket);
  socket.open();
  const client = await connecting;

  const response = client.chatHistory({ room: "general" });
  const request = requestOn(socket);
  assert.equal(request.type, "request");
  assert.equal(request.id, "1");
  assert.equal(request.method, "chat_history");

  socket.receive(responseFrame(request.id!, { messages: [] }, "HistoryResponse"));
  assert.deepEqual(await response, { messages: [] });
});

test("error frames reject the matching request with its wire error", async () => {
  const socket = new FakeWebSocket("ws://fake.test/chat?version=1");
  const connecting = connectWith(socket);
  socket.open();
  const client = await connecting;

  const response = client.chatListRooms({});
  const request = requestOn(socket);
  socket.receive(encodeEnvelope({
    type: "error",
    id: request.id,
    error: "room unavailable",
    errorCode: "room_unavailable",
  }));

  await assert.rejects(response, (error: unknown) => {
    assert.ok(error instanceof LatchError);
    assert.equal(error.code, "room_unavailable");
    assert.equal(error.message, "room unavailable");
    return true;
  });
});

test("malformed frames fail pending requests and close the socket", async () => {
  const socket = new FakeWebSocket("ws://fake.test/chat?version=1");
  const connecting = connectWith(socket);
  socket.open();
  const client = await connecting;

  const response = client.chatHistory({ room: "general" });
  socket.receive(new Uint8Array([1, 4]));

  await assert.rejects(response, (error: unknown) => {
    assert.ok(error instanceof LatchError);
    assert.equal(error.code, "malformed_frame");
    return true;
  });
  assert.equal(client.closed, true);
  assert.deepEqual(socket.closeCalls, [{ code: 1002, reason: "malformed binary frame" }]);
  await assert.rejects(client.chatHistory({ room: "general" }), (error: unknown) => {
    assert.ok(error instanceof LatchError);
    return error.code === "connection_closed";
  });
});

test("event dispatch is asynchronous and close fails pending requests", async () => {
  const socket = new FakeWebSocket("ws://fake.test/chat?version=1");
  let resolveEvent!: (event: Event) => void;
  const event = new Promise<Event>((resolve) => { resolveEvent = resolve; });
  const connecting = connectWith(socket, undefined, (value) => resolveEvent(value));
  socket.open();
  const client = await connecting;
  socket.receive(encodeEnvelope({
    type: "event",
    event: "chat.message",
    payload: encodeTyped(
      { kind: "presence", presence: { room: "general", userId: "alice", online: false } },
      { kind: "named", name: "Event" },
      __latchWireTypes,
    ),
  }));
  assert.deepEqual(await event, {
    kind: "presence",
    presence: { room: "general", userId: "alice", online: false },
  });

  const response = client.chatJoinRoom({ room: "general", userId: "alice" });
  client.close();
  await assert.rejects(response, (error: unknown) => {
    assert.ok(error instanceof LatchError);
    return error.code === "connection_closed";
  });
  assert.equal(client.closed, true);
  assert.deepEqual(socket.closeCalls, [{ code: 1000, reason: "" }]);
});

test("state transitions on connect, remote close, and reconnect", async () => {
  const states: ConnectionState[] = [];
  const first = new FakeWebSocket("ws://fake.test/chat?version=1");
  const second = new FakeWebSocket("ws://fake.test/chat?version=1");
  let next = first;
  const client = new LatchClient({
    url: "ws://fake.test/chat",
    onEvent: () => {},
    onConnectionStateChange: (state) => states.push(state),
    webSocketFactory: () => next,
  });
  const connecting = client.connect();
  assert.deepEqual(states, [ConnectionState.Connecting]);
  await assert.rejects(client.connect(), /already connecting or connected/);
  first.open();
  await connecting;
  assert.deepEqual(states, [ConnectionState.Connecting, ConnectionState.Connected]);
  await assert.rejects(client.connect(), /already connecting or connected/);
  first.remoteClose();
  first.remoteClose();
  assert.deepEqual(states, [ConnectionState.Connecting, ConnectionState.Connected, ConnectionState.Offline]);
  next = second;
  const reconnecting = client.connect();
  second.open();
  const connected = await reconnecting;
  connected.close();
  assert.deepEqual(states, [ConnectionState.Connecting, ConnectionState.Connected, ConnectionState.Offline,
    ConnectionState.Connecting, ConnectionState.Connected, ConnectionState.Offline]);
});

test("state returns offline on setup rejection", async () => {
  const states: ConnectionState[] = [];
  const socket = new FakeWebSocket("ws://fake.test/chat?version=1");
  const client = new LatchClient({ url: "ws://fake.test/chat", onEvent: () => {},
    onConnectionStateChange: (state) => states.push(state), webSocketFactory: () => socket });
  const connecting = client.connect();
  socket.open();
  socket.receive(encodeEnvelope({ type: "connection_error", error: "denied" }));
  await assert.rejects(connecting, LatchError);
  assert.deepEqual(states, [ConnectionState.Connecting, ConnectionState.Offline]);
});

test("connect rejects a connection_error that races WebSocket open", async () => {
  const socket = new FakeWebSocket("ws://fake.test/chat?version=1");
  const connecting = connectWith(socket);
  socket.open();
  socket.receive(encodeEnvelope({
    type: "connection_error",
    error: "bad token",
    errorCode: "unauthorized",
  }));

  await assert.rejects(connecting, (error: unknown) => {
    assert.ok(error instanceof LatchError);
    assert.equal(error.code, "unauthorized");
    assert.equal(error.message, "bad token");
    return true;
  });
  assert.deepEqual(socket.closeCalls, [{ code: 1000, reason: "connection rejected" }]);
});

test("connect rejects a malformed frame before setup", async () => {
  const socket = new FakeWebSocket("ws://fake.test/chat?version=1");
  const connecting = connectWith(socket);
  socket.open();
  socket.receive(new Uint8Array([1, 4]));

  await assert.rejects(connecting, (error: unknown) => {
    assert.ok(error instanceof Error);
    assert.equal(error.message, "Latch: malformed binary response during setup");
    return true;
  });
  assert.deepEqual(socket.closeCalls, [{ code: 1002, reason: "malformed binary event before setup" }]);
});

