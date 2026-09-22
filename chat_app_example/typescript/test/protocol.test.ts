import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
  __latchWireTypes,
  decodeTyped,
  encodeTyped,
  type Event,
} from "../client.ts";

test("binary protocol round-trips a chat message event", () => {
  const original: Event = {
    kind: "message",
    message: {
      message: {
        id: 7,
        room: "general",
        senderId: "alice",
        text: "hello",
        sentAt: 123000n,
      },
    },
  };
  const encoded = encodeTyped(original, { kind: "named", name: "Event" }, __latchWireTypes);
  const decoded = decodeTyped(encoded, { kind: "named", name: "Event" }, __latchWireTypes) as Event;
  assert.deepEqual(decoded, original);
});

test("binary protocol preserves presence events and numeric field values", () => {
  const original: Event = {
    kind: "presence",
    presence: { room: "general", userId: "bob", online: true },
  };
  const encoded = encodeTyped(original, { kind: "named", name: "Event" }, __latchWireTypes);
  const decoded = decodeTyped(encoded, { kind: "named", name: "Event" }, __latchWireTypes) as Event;
  assert.deepEqual(decoded, original);
});
