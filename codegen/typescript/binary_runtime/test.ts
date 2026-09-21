import {
  BinaryCodecError,
  decodeEnvelope,
  decodeValue,
  encodeEnvelope,
  encodeFloat32,
  encodeFloat64,
  encodeInt,
  encodeStruct,
  encodeTyped,
  decodeTyped,
  encodeTimeNanoseconds,
  encodeUint,
  encodeValue,
  type Envelope,
} from "./codec.js";

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}
function equalBytes(actual: Uint8Array, expected: number[], message: string): void {
  assert(actual.length === expected.length && actual.every((v, i) => v === expected[i]), `${message}: ${Array.from(actual)}`);
}
function expectMalformed(fn: () => unknown): void {
  let failed = false;
  try { fn(); } catch (error) { failed = error instanceof BinaryCodecError; }
  assert(failed, "malformed input was accepted");
}

// These are the canonical tag/varint encodings used by wire/binary.go.
equalBytes(encodeInt(-1n), [3, 1], "zigzag -1");
equalBytes(encodeInt(64n), [3, 128, 1], "zigzag 64");
equalBytes(encodeUint(300n), [4, 172, 2], "unsigned 300");
equalBytes(encodeFloat32(1), [5, 0, 0, 128, 63], "float32");
equalBytes(encodeFloat64(1), [6, 0, 0, 0, 0, 0, 0, 240, 63], "float64");
equalBytes(encodeValue("hello"), [7, 5, 104, 101, 108, 108, 111], "string");
equalBytes(encodeValue(new Uint8Array([0, 255])), [8, 2, 0, 255], "bytes");

const decoded = decodeValue(encodeStruct({
  1: "hello 👋",
  2: -19n,
  3: [null, true, 1.5],
  4: { label: "value" },
  5: new Uint8Array([1, 2, 255]),
}));
assert(typeof decoded === "object" && decoded !== null, "struct did not decode");
const decodedStruct = decoded as Record<string, any>;
assert(decodedStruct["1"] === "hello 👋" && decodedStruct["2"] === -19n, "numeric struct fields changed");
assert((decodedStruct["5"] as Uint8Array)[2] === 255, "bytes changed");

// A decoder that does not know field 99 can still skip its complete nested value.
const withUnknown = encodeStruct({ 1: 7n, 99: { nested: [1n, 2n, 3n] } });
const unknownResult = decodeValue(withUnknown) as Record<string, unknown>;
assert(unknownResult["1"] === 7n && unknownResult["99"] !== undefined, "unknown struct field was not self-delimiting");

const typedRegistry = {
  Child: { kind: "struct", fields: { 1: { name: "name", type: { kind: "string" } } } },
  Parent: { kind: "struct", fields: {
    1: { name: "child", type: { kind: "named", name: "Child" } },
    2: { name: "items", type: { kind: "list", elem: { kind: "named", name: "Child" } } },
    3: { name: "labels", type: { kind: "map", value: { kind: "int" } } },
    4: { name: "optional", type: { kind: "nullable", elem: { kind: "string" } }, optional: true },
  } },
} as const;
const typedInput = { child: { name: "one" }, items: [{ name: "two" }], labels: { count: 3 }, };
const typedOutput = decodeTyped(encodeTyped(typedInput, { kind: "named", name: "Parent" }, typedRegistry), { kind: "named", name: "Parent" }, typedRegistry) as any;
assert(typedOutput.child.name === "one" && typedOutput.items[0].name === "two" && typedOutput.labels.count === 3, "typed nested struct round trip failed");

const time = encodeTimeNanoseconds(-1234567890123n);
assert(decodeValue(time) === -1234567890123n, "timestamp precision changed");

const envelope: Envelope = {
  type: "response",
  version: "v1",
  id: "42",
  method: "ignored",
  event: "",
  payload: new Uint8Array([1, 2, 3]),
  error: "",
  errorCode: "",
};
const envelopeBytes = encodeEnvelope(envelope);
assert(envelopeBytes[0] === 1 && envelopeBytes[1] === 4, "envelope version/frame code mismatch");
const roundTripEnvelope = decodeEnvelope(envelopeBytes);
assert(roundTripEnvelope.type === "response" && roundTripEnvelope.id === "42", "envelope fields changed");
assert(roundTripEnvelope.payload?.join(",") === "1,2,3", "envelope payload changed");

expectMalformed(() => decodeValue(new Uint8Array([7, 0xff])));
expectMalformed(() => decodeValue(new Uint8Array([6, 1, 2])));
expectMalformed(() => decodeEnvelope(new Uint8Array([2])));
expectMalformed(() => decodeEnvelope(new Uint8Array([1, 9])));
expectMalformed(() => decodeEnvelope(new Uint8Array([1, 4])));

const sharedVectors: Record<string, number[]> = {
  int_minus_one: [3, 1], int_64: [3, 128, 1], uint_300: [4, 172, 2],
  float32_one: [5, 0, 0, 128, 63], float64_one: [6, 0, 0, 0, 0, 0, 0, 240, 63],
  string_hello: [7, 5, 104, 101, 108, 108, 111], bytes_binary: [8, 3, 0, 255, 127],
  struct_nested: [9, 4, 1, 7, 5, 104, 101, 108, 108, 111, 2, 3, 37, 3, 10, 3, 0, 2, 6, 0, 0, 0, 0, 0, 0, 248, 63, 4, 8, 3, 1, 2, 255],
  envelope_response: [1, 4, 0, 2, 52, 50, 0, 0, 3, 1, 2, 3, 0, 1, 120],
};
for (const [name, bytes] of Object.entries(sharedVectors)) {
  if (name === "envelope_response") {
    const env: Envelope = { type: "response", id: "42", payload: new Uint8Array([1, 2, 3]), errorCode: "x" };
    equalBytes(encodeEnvelope(env), bytes, name);
    const decoded = decodeEnvelope(new Uint8Array(bytes));
    assert(decoded.id === "42" && decoded.errorCode === "x", `${name} decode`);
  } else {
    equalBytes(name === "struct_nested" ? encodeStruct({ 1: "hello", 2: -19n, 3: [null, true, 1.5], 4: new Uint8Array([1, 2, 255]) }) :
      name === "int_minus_one" ? encodeInt(-1) : name === "int_64" ? encodeInt(64) : name === "uint_300" ? encodeUint(300) :
      name === "float32_one" ? encodeFloat32(1) : name === "float64_one" ? encodeFloat64(1) : name === "string_hello" ? encodeValue("hello") : encodeValue(new Uint8Array([0, 255, 127])), bytes, name);
  }
}

console.log("binary runtime tests passed");
