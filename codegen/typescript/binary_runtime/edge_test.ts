import {
  BinaryCodecError,
  MAX_CONTAINER,
  MAX_DEPTH,
  ValueTag,
  decodeEnvelope,
  decodeTyped,
  decodeValue,
  encodeEnvelope,
  encodeFloat64,
  encodeInt,
  encodeStruct,
  encodeTimeNanoseconds,
  encodeTyped,
  encodeUint,
  encodeValue,
  type Envelope,
  type FrameType,
  type WireType,
  type WireTypeRegistry,
} from "./codec.js";

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}
function same(actual: unknown, expected: unknown, message: string): void {
  assert(Object.is(actual, expected), `${message}: got ${String(actual)}, want ${String(expected)}`);
}
function bytes(...values: number[]): Uint8Array { return new Uint8Array(values); }
function join(...parts: number[][]): Uint8Array { return new Uint8Array(parts.flat()); }
function uvarint(value: bigint): number[] {
  const out: number[] = [];
  do {
    let b = Number(value & 0x7fn);
    value >>= 7n;
    if (value !== 0n) b |= 0x80;
    out.push(b);
  } while (value !== 0n);
  return out;
}
function thrown(fn: () => unknown): unknown {
  try { fn(); } catch (error) { return error; }
  return undefined;
}
function expectThrow(fn: () => unknown, label: string): void {
  assert(thrown(fn) !== undefined, `${label}: invalid input was accepted`);
}
function expectCodecError(fn: () => unknown, label: string): void {
  const error = thrown(fn);
  assert(error !== undefined, `${label}: malformed input was accepted`);
  assert(error instanceof BinaryCodecError, `${label}: wrong error ${String(error)}`);
}
function expectBytes(actual: Uint8Array, expected: number[], label: string): void {
  assert(actual.length === expected.length, `${label}: wrong length`);
  expected.forEach((value, index) => assert(actual[index] === value, `${label}: byte ${index}`));
}

const failures: string[] = [];
function test(name: string, fn: () => void): void {
  try { fn(); } catch (error) { failures.push(`${name}: ${error instanceof Error ? error.message : String(error)}`); }
}

test("integer widths and safe-integer boundaries", () => {
  const minInt64 = -(1n << 63n);
  const maxInt64 = (1n << 63n) - 1n;
  const maxUint64 = (1n << 64n) - 1n;
  same(decodeValue(encodeInt(minInt64)), minInt64, "min int64");
  same(decodeValue(encodeInt(maxInt64)), maxInt64, "max int64");
  same(decodeValue(encodeUint(maxUint64)), maxUint64, "max uint64");
  same(decodeValue(encodeInt(Number.MAX_SAFE_INTEGER)), BigInt(Number.MAX_SAFE_INTEGER), "safe int");
  same(decodeValue(encodeUint(Number.MAX_SAFE_INTEGER)), BigInt(Number.MAX_SAFE_INTEGER), "safe uint");
  expectThrow(() => encodeInt(minInt64 - 1n), "int underflow");
  expectThrow(() => encodeInt(maxInt64 + 1n), "int overflow");
  expectThrow(() => encodeUint(-1n), "uint underflow");
  expectThrow(() => encodeUint(maxUint64 + 1n), "uint overflow");
  expectThrow(() => encodeInt(Number.MAX_SAFE_INTEGER + 1), "unsafe int number");
  expectThrow(() => encodeUint(Number.MAX_SAFE_INTEGER + 1), "unsafe uint number");
});

test("malformed, overflow, and noncanonical varints", () => {
  expectCodecError(() => decodeValue(bytes(ValueTag.uint, 0x80)), "unterminated varint");
  expectCodecError(() => decodeValue(bytes(ValueTag.uint, 0x80, 0)), "overlong zero");
  expectCodecError(() => decodeValue(bytes(ValueTag.uint, 0x81, 0)), "overlong one");
  expectCodecError(() => decodeValue(bytes(ValueTag.int, 0x82, 0)), "overlong signed one");
  expectCodecError(() => decodeValue(join([ValueTag.uint], [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 2])), "u64 overflow");
  expectCodecError(() => decodeValue(join([ValueTag.uint], [0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0])), "noncanonical ten-byte varint");
});

test("invalid UTF-8 is rejected as a codec error", () => {
  expectCodecError(() => decodeValue(bytes(ValueTag.string, 1, 0xff)), "string UTF-8");
  expectCodecError(() => decodeValue(join([ValueTag.map, 1, ValueTag.string, 1, 0xff], [ValueTag.null])), "map-key UTF-8");
  const malformed = encodeEnvelope({ type: "connect", version: "v1" });
  const invalid = malformed.slice(); invalid[3] = 0xff;
  expectCodecError(() => decodeEnvelope(invalid), "envelope UTF-8");
});

test("depth, container limits, truncation, and trailing bytes", () => {
  let nested: unknown = null;
  for (let i = 0; i <= MAX_DEPTH; i++) nested = [nested];
  expectThrow(() => encodeValue(nested), "maximum encode depth");
  let nestedBytes: number[] = [ValueTag.null];
  for (let i = 0; i <= MAX_DEPTH; i++) nestedBytes = [ValueTag.list, 1, ...nestedBytes];
  expectCodecError(() => decodeValue(new Uint8Array(nestedBytes)), "maximum decode depth");
  const tooMany = uvarint(BigInt(MAX_CONTAINER) + 1n);
  expectCodecError(() => decodeValue(join([ValueTag.list], tooMany)), "list limit");
  expectCodecError(() => decodeValue(join([ValueTag.map], tooMany)), "map limit");
  expectCodecError(() => decodeValue(join([ValueTag.struct], tooMany)), "struct limit");
  expectCodecError(() => decodeValue(bytes(ValueTag.list, 1)), "truncated list");
  expectCodecError(() => decodeValue(bytes(ValueTag.string, 1)), "truncated blob");
  expectCodecError(() => decodeValue(bytes(ValueTag.null, ValueTag.null)), "trailing value");
});

test("struct IDs are positive, unique, and bounded", () => {
  expectCodecError(() => decodeValue(bytes(ValueTag.struct, 2, 1, ValueTag.null, 1, ValueTag.null)), "duplicate ID");
  expectCodecError(() => decodeValue(bytes(ValueTag.struct, 1, 0, ValueTag.null)), "zero ID");
  expectCodecError(() => decodeValue(join([ValueTag.struct, 1], uvarint(0x1_0000_0000n), [ValueTag.null])), "large ID");
  expectThrow(() => encodeStruct({ 0: null }), "encoded zero ID");
  expectThrow(() => encodeStruct({ "01": null }), "encoded noncanonical ID");
  expectThrow(() => encodeStruct({ "4294967296": null }), "encoded large ID");
});

test("__proto__ and constructor map keys survive round trips", () => {
  const input = Object.create(null) as Record<string, unknown>;
  Object.defineProperty(input, "__proto__", { enumerable: true, value: "proto" });
  Object.defineProperty(input, "constructor", { enumerable: true, value: "ctor" });
  const decoded = decodeValue(encodeValue(input)) as Record<string, unknown>;
  assert(Object.prototype.hasOwnProperty.call(decoded, "__proto__"), "lost __proto__");
  assert(Object.prototype.hasOwnProperty.call(decoded, "constructor"), "lost constructor");
  same(decoded["__proto__"], "proto", "__proto__ value");
  same(decoded.constructor, "ctor", "constructor value");
});

test("timestamp boundaries are exact", () => {
  const min = -(1n << 63n); const max = (1n << 63n) - 1n;
  same(decodeValue(encodeTimeNanoseconds(min)), min, "minimum timestamp");
  same(decodeValue(encodeTimeNanoseconds(max)), max, "maximum timestamp");
  expectThrow(() => encodeTimeNanoseconds(min - 1n), "timestamp underflow");
  expectThrow(() => encodeTimeNanoseconds(max + 1n), "timestamp overflow");
  expectThrow(() => encodeTimeNanoseconds(Number.MAX_SAFE_INTEGER + 1), "unsafe timestamp");
});

const registry: WireTypeRegistry = {};
const structType: WireType = {
  kind: "struct",
  fields: {
    "1": { name: "name", type: { kind: "string" } },
    "2": { name: "count", type: { kind: "int" } },
    "3": { name: "optional", type: { kind: "nullable", elem: { kind: "string" } }, optional: true },
  },
};
const mapType: WireType = { kind: "map", value: { kind: "string" } };
const typed = (value: unknown, type: WireType): Uint8Array => encodeTyped(value, type, registry);

test("typed encode validation is strict", () => {
  expectThrow(() => typed(null, { kind: "string" }), "null string");
  expectThrow(() => typed(undefined, { kind: "string" }), "undefined string");
  expectThrow(() => typed(null, { kind: "int" }), "null int");
  expectThrow(() => typed("1", { kind: "int" }), "string int");
  expectThrow(() => typed(1, { kind: "bool" }), "number bool");
  expectThrow(() => typed("1", { kind: "float32" }), "string float");
  expectThrow(() => typed(Number.NaN, { kind: "float64" }), "NaN float");
  expectThrow(() => typed("bytes", { kind: "bytes" }), "string bytes");
  expectThrow(() => typed([1], { kind: "bytes" }), "array bytes");
  expectThrow(() => typed(undefined, { kind: "nullable", elem: { kind: "string" } }), "undefined nullable");
  expectThrow(() => typed(Number.MAX_SAFE_INTEGER + 1, { kind: "int" }), "unsafe typed int");
  expectThrow(() => typed(-1, { kind: "uint" }), "negative typed uint");
  expectThrow(() => typed({}, structType), "missing required field");
  expectThrow(() => typed({ name: "n" }, structType), "missing second required field");
  expectThrow(() => typed({ name: "n", count: 1, optional: 2 }, structType), "wrong optional field");
  expectThrow(() => typed({ x: 1 }, { kind: "struct", fields: { "0": { name: "x", type: { kind: "int" } } } }), "zero typed field ID");
});

test("typed decode validates wire kinds, nullability, and required fields", () => {
  expectThrow(() => decodeTyped(encodeValue("x"), { kind: "int" }, registry), "string as int");
  expectThrow(() => decodeTyped(encodeUint(1), { kind: "int" }, registry), "uint as int");
  expectThrow(() => decodeTyped(encodeInt(1), { kind: "uint" }, registry), "int as uint");
  expectThrow(() => decodeTyped(encodeValue(1), { kind: "bool" }, registry), "number as bool");
  expectThrow(() => decodeTyped(encodeValue("x"), { kind: "bytes" }, registry), "string as bytes");
  expectThrow(() => decodeTyped(encodeValue(null), { kind: "string" }, registry), "null as string");
  expectThrow(() => decodeTyped(encodeStruct({}), structType, registry), "missing required decoded field");
  expectThrow(() => decodeTyped(bytes(ValueTag.struct, 2, 1, ValueTag.string, 1, 1, 1, ValueTag.null), structType, registry), "duplicate decoded field");
  const output = decodeTyped(typed({ name: "n", count: 3 }, structType), structType, registry) as Record<string, unknown>;
  same(output.name, "n", "typed name"); same(output.count, 3, "typed count");
});

test("bytes, lists, maps, and fixed-array representation stay type-safe", () => {
  const input = Object.create(null) as Record<string, unknown>;
  Object.defineProperty(input, "__proto__", { enumerable: true, value: "safe" });
  Object.defineProperty(input, "constructor", { enumerable: true, value: "safe2" });
  const output = decodeTyped(typed(input, mapType), mapType, registry) as Record<string, unknown>;
  assert(Object.prototype.hasOwnProperty.call(output, "__proto__"), "typed lost __proto__");
  assert(Object.prototype.hasOwnProperty.call(output, "constructor"), "typed lost constructor");
  const listType: WireType = { kind: "list", elem: { kind: "int" } };
  expectThrow(() => typed([1, "bad"], listType), "wrong list element");
  expectThrow(() => decodeTyped(encodeValue(new Uint8Array([1])), listType, registry), "bytes as list");
  expectThrow(() => decodeTyped(encodeStruct({ 1: "x" }), mapType, registry), "struct as map");
});

test("decodes the canonical shared fixture values", () => {
  same(decodeValue(bytes(3, 1)), -1n, "signed -1 fixture");
  same(decodeValue(bytes(3, 128, 1)), 64n, "signed 64 fixture");
  same(decodeValue(bytes(4, 172, 2)), 300n, "unsigned 300 fixture");
  same(decodeValue(bytes(5, 0, 0, 128, 63)), 1, "float32 fixture");
  same(decodeValue(bytes(6, 0, 0, 0, 0, 0, 0, 240, 63)), 1, "float64 fixture");
  same(decodeValue(bytes(7, 5, 104, 101, 108, 108, 111)), "hello", "string fixture");
  const binary = decodeValue(bytes(8, 3, 0, 255, 127)) as Uint8Array;
  expectBytes(binary, [0, 255, 127], "bytes fixture");
  const nested = decodeValue(bytes(9, 1, 1, 7, 1, 120)) as Record<string, unknown>;
  same(nested["1"], "x", "struct fixture");
});

test("all envelope frame types round trip", () => {
  const types: FrameType[] = ["connect", "connected", "request", "response", "error", "event", "connection_error"];
  for (const type of types) {
    const decoded = decodeEnvelope(encodeEnvelope({ type, version: "v1", id: "1", payload: bytes(1, 2, 255) }));
    same(decoded.type, type, `${type} type`); same(decoded.id, "1", `${type} ID`);
    expectBytes(decoded.payload ?? new Uint8Array(), [1, 2, 255], `${type} payload`);
  }
});

test("malformed envelopes reject version, frame, truncation, trailing, and invalid UTF-8", () => {
  const valid = encodeEnvelope({ type: "connect", version: "v1", id: "1" });
  expectCodecError(() => decodeEnvelope(bytes(2)), "version");
  expectCodecError(() => decodeEnvelope(bytes(1, 8)), "frame code");
  expectCodecError(() => decodeEnvelope(valid.slice(0, -1)), "truncated envelope");
  expectCodecError(() => decodeEnvelope(join(Array.from(valid), [0])), "trailing envelope");
  const invalid = valid.slice(); invalid[3] = 0xff;
  expectCodecError(() => decodeEnvelope(invalid), "invalid envelope UTF-8");
});

if (failures.length > 0) throw new Error(`edge tests failed (${failures.length}):\n${failures.join("\n")}`);
console.log("binary runtime edge tests passed");
