/** TypeScript implementation of latch/wire's version-1 binary format. */

export const MAX_DEPTH = 128;
export const MAX_CONTAINER = 1 << 24;

export const ValueTag = {
  null: 0,
  false: 1,
  true: 2,
  int: 3,
  uint: 4,
  float32: 5,
  float64: 6,
  string: 7,
  bytes: 8,
  struct: 9,
  list: 10,
  map: 11,
  time: 12,
} as const;

export interface StructValue { [field: string]: WireValue }
export interface MapValue { [key: string]: WireValue }
export type WireValue = null | boolean | bigint | number | string | Uint8Array | WireValue[] | StructValue | MapValue;

export type FrameType =
  | "connect"
  | "connected"
  | "request"
  | "response"
  | "error"
  | "event"
  | "connection_error";

export interface Envelope {
  type: FrameType;
  version?: string;
  id?: string;
  method?: string;
  event?: string;
  payload?: Uint8Array;
  error?: string;
  errorCode?: string;
}

const frameCodes: Record<FrameType, number> = {
  connect: 1,
  connected: 2,
  request: 3,
  response: 4,
  error: 5,
  event: 6,
  connection_error: 7,
};
const frameNames: Record<number, FrameType> = {
  1: "connect",
  2: "connected",
  3: "request",
  4: "response",
  5: "error",
  6: "event",
  7: "connection_error",
};

export class BinaryCodecError extends Error {
  constructor(message = "wire: malformed binary value") {
    super(message);
    this.name = "BinaryCodecError";
  }
}

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder("utf-8", { fatal: true });
const MAX_INT64 = (1n << 63n) - 1n;
const MIN_INT64 = -(1n << 63n);
const MAX_UINT64 = (1n << 64n) - 1n;


function asBigInt(value: number | bigint): bigint {
  if (typeof value === "bigint") return value;
  if (!Number.isSafeInteger(value)) throw new RangeError("wire: integer must be a safe integer or bigint");
  return BigInt(value);
}

class Writer {
  private bytes: number[] = [];

  byte(value: number): void { this.bytes.push(value & 0xff); }

  uvarint(value: bigint): void {
    if (value < 0n || value > MAX_UINT64) throw new RangeError("wire: unsigned integer out of range");
    do {
      let b = Number(value & 0x7fn);
      value >>= 7n;
      if (value !== 0n) b |= 0x80;
      this.byte(b);
    } while (value !== 0n);
  }

  svarint(value: number | bigint): void {
    const n = asBigInt(value);
    if (n < MIN_INT64 || n > MAX_INT64) throw new RangeError("wire: signed integer out of range");
    this.uvarint((n << 1n) ^ (n >> 63n));
  }

  raw(value: Uint8Array): void { for (const b of value) this.byte(b); }

  blob(value: Uint8Array): void {
    this.uvarint(BigInt(value.length));
    this.raw(value);
  }

  result(): Uint8Array { return Uint8Array.from(this.bytes); }
}

class Reader {
  private pos = 0;
  private readonly data: Uint8Array;
  constructor(data: Uint8Array) { this.data = data; }

  byte(): number {
    if (this.pos >= this.data.length) throw new BinaryCodecError();
    return this.data[this.pos++];
  }

  peekByte(): number {
    if (this.pos >= this.data.length) throw new BinaryCodecError();
    return this.data[this.pos];
  }

  uvarint(): bigint {
    let value = 0n;
    for (let i = 0; i < 10; i++) {
      const b = this.byte();
      if (i === 9 && (b & 0xfe) !== 0) throw new BinaryCodecError();
      value |= BigInt(b & 0x7f) << BigInt(i * 7);
      if ((b & 0x80) === 0) {
        // A terminal zero payload on a multi-byte encoding is overlong.
        if (i > 0 && (b & 0x7f) === 0) throw new BinaryCodecError();
        return value;
      }
    }
    throw new BinaryCodecError();
  }

  svarint(): bigint {
    const value = this.uvarint();
    return (value >> 1n) ^ -(value & 1n);
  }

  blob(): Uint8Array {
    const length = this.uvarint();
    if (length > BigInt(MAX_CONTAINER) || length > BigInt(this.data.length - this.pos)) throw new BinaryCodecError();
    const end = this.pos + Number(length);
    const result = this.data.slice(this.pos, end);
    this.pos = end;
    return result;
  }

  take(length: number): Uint8Array {
    if (length < 0 || this.pos + length > this.data.length) throw new BinaryCodecError();
    const result = this.data.slice(this.pos, this.pos + length);
    this.pos += length;
    return result;
  }

  done(): boolean { return this.pos === this.data.length; }
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value) && !(value instanceof Uint8Array) && !(value instanceof Date);
}

function decodeUTF8(value: Uint8Array): string {
  try {
    return textDecoder.decode(value);
  } catch {
    throw new BinaryCodecError("wire: invalid UTF-8");
  }
}

function setOwn<T>(object: Record<string, T>, key: string, value: T): void {
  Object.defineProperty(object, key, {
    configurable: true,
    enumerable: true,
    writable: true,
    value,
  });
}

function encodeValueInto(writer: Writer, value: unknown, depth: number): void {
  if (depth > MAX_DEPTH) throw new BinaryCodecError("wire: maximum nesting depth exceeded");
  if (value === null || value === undefined) { writer.byte(ValueTag.null); return; }
  if (value === false) { writer.byte(ValueTag.false); return; }
  if (value === true) { writer.byte(ValueTag.true); return; }
  if (typeof value === "bigint") {
    if (value < MIN_INT64 || value > MAX_INT64) { writer.byte(ValueTag.uint); writer.uvarint(value); }
    else { writer.byte(ValueTag.int); writer.svarint(value); }
    return;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new TypeError("wire: non-finite number must be encoded explicitly as float32/float64");
    writer.byte(ValueTag.float64);
    const buffer = new ArrayBuffer(8);
    new DataView(buffer).setFloat64(0, value, true);
    writer.raw(new Uint8Array(buffer));
    return;
  }
  if (typeof value === "string") { writer.byte(ValueTag.string); writer.blob(textEncoder.encode(value)); return; }
  if (value instanceof Uint8Array) { writer.byte(ValueTag.bytes); writer.blob(value); return; }
  if (value instanceof Date) { encodeTimeInto(writer, BigInt(value.getTime()) * 1_000_000n); return; }
  if (Array.isArray(value)) {
    writer.byte(ValueTag.list); writer.uvarint(BigInt(value.length));
    for (const item of value) encodeValueInto(writer, item, depth + 1);
    return;
  }
  if (isPlainObject(value)) {
    encodeMapInto(writer, value, depth);
    return;
  }
  throw new TypeError("wire: unsupported value");
}

function encodeTimeInto(writer: Writer, nanos: number | bigint): void {
  writer.byte(ValueTag.time);
  writer.svarint(nanos);
}

function encodeMapInto(writer: Writer, value: Record<string, unknown>, depth: number): void {
  writer.byte(ValueTag.map);
  const entries = Object.entries(value);
  writer.uvarint(BigInt(entries.length));
  for (const [key, item] of entries) {
    writer.byte(ValueTag.string);
    writer.blob(textEncoder.encode(key));
    encodeValueInto(writer, item, depth + 1);
  }
}

function encodeRawFloat(value: number, width: 4 | 8): Uint8Array {
  const buffer = new ArrayBuffer(width);
  const view = new DataView(buffer);
  if (width === 4) view.setFloat32(0, value, true); else view.setFloat64(0, value, true);
  return new Uint8Array(buffer);
}

export function encodeInt(value: number | bigint): Uint8Array {
  const writer = new Writer(); writer.byte(ValueTag.int); writer.svarint(value); return writer.result();
}

export function encodeUint(value: number | bigint): Uint8Array {
  const writer = new Writer(); writer.byte(ValueTag.uint); writer.uvarint(asBigInt(value)); return writer.result();
}

export function encodeFloat32(value: number): Uint8Array {
  if (!Number.isFinite(value)) throw new TypeError("wire: float must be finite");
  const writer = new Writer(); writer.byte(ValueTag.float32); writer.raw(encodeRawFloat(value, 4)); return writer.result();
}

export function encodeFloat64(value: number): Uint8Array {
  if (!Number.isFinite(value)) throw new TypeError("wire: float must be finite");
  const writer = new Writer(); writer.byte(ValueTag.float64); writer.raw(encodeRawFloat(value, 8)); return writer.result();
}

/** Encodes a value. Plain objects are encoded as string-keyed maps. */
export function encodeValue(value: unknown): Uint8Array {
  const writer = new Writer();
  encodeValueInto(writer, value, 0);
  return writer.result();
}

/** Encodes a struct whose keys are positive numeric field IDs. */
export function encodeStruct(fields: Record<number | string, unknown>): Uint8Array {
  const writer = new Writer();
  encodeStructInto(writer, fields, 0);
  return writer.result();
}

function structFieldID(key: string): bigint {
  if (!/^[1-9][0-9]*$/.test(key)) throw new RangeError(`wire: invalid struct field number ${key}`);
  const id = BigInt(key);
  if (id > 0xffffffffn) throw new RangeError(`wire: invalid struct field number ${key}`);
  return id;
}

function encodeStructInto(writer: Writer, fields: Record<number | string, unknown>, depth: number): void {
  if (depth > MAX_DEPTH) throw new BinaryCodecError("wire: maximum nesting depth exceeded");
  const entries = Object.entries(fields).map(([key, value]) => {
    if (!/^[1-9][0-9]*$/.test(key) || BigInt(key) > 0xffffffffn) throw new RangeError(`wire: invalid struct field number ${key}`);
    return [BigInt(key), value] as const;
  });
  const seen = new Set<string>();
  for (const [id] of entries) { if (seen.has(id.toString())) throw new RangeError(`wire: duplicate field number ${id}`); seen.add(id.toString()); }
  writer.byte(ValueTag.struct); writer.uvarint(BigInt(entries.length));
  for (const [id, value] of entries) { writer.uvarint(id); encodeValueInto(writer, value, depth + 1); }
}

function decodeValueFrom(reader: Reader, depth: number): WireValue {
  if (depth > MAX_DEPTH) throw new BinaryCodecError("wire: maximum nesting depth exceeded");
  switch (reader.byte()) {
    case ValueTag.null: return null;
    case ValueTag.false: return false;
    case ValueTag.true: return true;
    case ValueTag.int: return reader.svarint();
    case ValueTag.uint: return reader.uvarint();
    case ValueTag.float32: {
      const bytes = reader.take(4); return new DataView(bytes.buffer, bytes.byteOffset, 4).getFloat32(0, true);
    }
    case ValueTag.float64: {
      const bytes = reader.take(8); return new DataView(bytes.buffer, bytes.byteOffset, 8).getFloat64(0, true);
    }
    case ValueTag.string: return decodeUTF8(reader.blob());
    case ValueTag.bytes: return reader.blob();
    case ValueTag.time: return reader.svarint();
    case ValueTag.list: {
      const count = reader.uvarint();
      if (count > BigInt(MAX_CONTAINER)) throw new BinaryCodecError();
      const result: WireValue[] = [];
      for (let i = 0; i < Number(count); i++) result.push(decodeValueFrom(reader, depth + 1));
      return result;
    }
    case ValueTag.map: {
      const count = reader.uvarint();
      if (count > BigInt(MAX_CONTAINER)) throw new BinaryCodecError();
      const result = Object.create(null) as MapValue;
      const seen = new Set<string>();
      for (let i = 0; i < Number(count); i++) {
        if (reader.byte() !== ValueTag.string) throw new BinaryCodecError();
        const key = decodeUTF8(reader.blob());
        if (seen.has(key)) throw new BinaryCodecError("wire: duplicate map key");
        seen.add(key);
        setOwn(result, key, decodeValueFrom(reader, depth + 1));
      }
      return result;
    }
    case ValueTag.struct: {
      const count = reader.uvarint();
      if (count > BigInt(MAX_CONTAINER)) throw new BinaryCodecError();
      const result = Object.create(null) as StructValue;
      const seen = new Set<string>();
      for (let i = 0; i < Number(count); i++) {
        const id = reader.uvarint();
        if (id === 0n || id > 0xffffffffn) throw new BinaryCodecError();
        const key = id.toString();
        if (seen.has(key)) throw new BinaryCodecError("wire: duplicate struct field");
        seen.add(key);
        setOwn(result, key, decodeValueFrom(reader, depth + 1));
      }
      return result;
    }
    default:
      throw new BinaryCodecError();
  }
}

/** Decodes exactly one value and rejects trailing bytes. */
export function decodeValue(data: Uint8Array): WireValue {
  const reader = new Reader(data);
  const value = decodeValueFrom(reader, 0);
  if (!reader.done()) throw new BinaryCodecError();
  return value;
}

export type WireType =
  | { kind: "null" | "bool" | "float32" | "float64" | "string" }
  | { kind: "int" | "uint"; bits?: 8 | 16 | 32 | 64; min?: number | bigint; max?: number | bigint }
  | { kind: "bytes"; length?: number }
  | { kind: "time"; unit?: "nanoseconds" }
  | { kind: "enum" }
  | { kind: "nullable"; elem: WireType }
  | { kind: "list"; elem: WireType }
  | { kind: "array"; length: number; elem: WireType }
  | { kind: "map"; value: WireType }
  | { kind: "named"; name: string }
  | { kind: "struct"; fields: Record<string, { name: string; type: WireType; optional?: boolean }> };

export type WireTypeRegistry = Record<string, WireType>;


function checkedBits(type: Extract<WireType, { kind: "int" | "uint" }>): 8 | 16 | 32 | 64 {
  const bits = type.bits ?? 64;
  if (bits !== 8 && bits !== 16 && bits !== 32 && bits !== 64) throw new TypeError("wire: invalid integer width");
  return bits;
}

function integerBounds(type: Extract<WireType, { kind: "int" | "uint" }>): [bigint, bigint] {
  const bits = checkedBits(type);
  const min = type.kind === "int" ? -(1n << BigInt(bits - 1)) : 0n;
  const max = type.kind === "int" ? (1n << BigInt(bits - 1)) - 1n : (1n << BigInt(bits)) - 1n;
  const suppliedMin = type.min === undefined ? min : asBigInt(type.min);
  const suppliedMax = type.max === undefined ? max : asBigInt(type.max);
  if (suppliedMin < min || suppliedMax > max || suppliedMin > suppliedMax) throw new TypeError("wire: invalid integer range");
  return [suppliedMin, suppliedMax];
}

function checkedInteger(value: unknown, type: Extract<WireType, { kind: "int" | "uint" }>): bigint {
  if (typeof value !== "number" && typeof value !== "bigint") throw new TypeError("wire: expected integer");
  const result = asBigInt(value);
  const [min, max] = integerBounds(type);
  if (result < min || result > max) throw new RangeError("wire: integer out of range");
  return result;
}

function decodedInteger(value: bigint, type: Extract<WireType, { kind: "int" | "uint" }>): number | bigint {
  const [min, max] = integerBounds(type);
  if (value < min || value > max) throw new BinaryCodecError("wire: integer out of range");
  if (type.bits === 64) return value;
  return Number(value);
}

function expectTag(reader: Reader, expected: number): void {
  if (reader.byte() !== expected) throw new BinaryCodecError("wire: unexpected value kind");
}

function encodeTypedInto(writer: Writer, value: unknown, type: WireType, registry: WireTypeRegistry, depth: number): void {
  if (depth > MAX_DEPTH) throw new BinaryCodecError("wire: maximum nesting depth exceeded");
  if (type.kind === "named") {
    const named = registry[type.name];
    if (!named) throw new BinaryCodecError(`wire: unknown named type ${type.name}`);
    encodeTypedInto(writer, value, named, registry, depth);
    return;
  }
  if (type.kind === "nullable") {
    if (value === null) { writer.byte(ValueTag.null); return; }
    if (value === undefined) throw new TypeError("wire: expected null or value");
    encodeTypedInto(writer, value, type.elem, registry, depth + 1);
    return;
  }
  switch (type.kind) {
    case "null": if (value !== null) throw new TypeError("wire: expected null"); writer.byte(ValueTag.null); return;
    case "bool": if (typeof value !== "boolean") throw new TypeError("wire: expected boolean"); writer.byte(value ? ValueTag.true : ValueTag.false); return;
    case "int": writer.byte(ValueTag.int); writer.svarint(checkedInteger(value, type)); return;
    case "uint": writer.byte(ValueTag.uint); writer.uvarint(checkedInteger(value, type)); return;
    case "float32": if (typeof value !== "number" || !Number.isFinite(value)) throw new TypeError("wire: expected finite number"); writer.byte(ValueTag.float32); writer.raw(encodeRawFloat(value, 4)); return;
    case "float64": if (typeof value !== "number" || !Number.isFinite(value)) throw new TypeError("wire: expected finite number"); writer.byte(ValueTag.float64); writer.raw(encodeRawFloat(value, 8)); return;
    case "string": if (typeof value !== "string") throw new TypeError("wire: expected string"); writer.byte(ValueTag.string); writer.blob(textEncoder.encode(value)); return;
    case "bytes":
      if (!(value instanceof Uint8Array)) throw new TypeError("wire: expected bytes");
      if (type.length !== undefined && value.length !== type.length) throw new RangeError("wire: bytes length mismatch");
      writer.byte(ValueTag.bytes); writer.blob(value); return;
    case "time": writer.byte(ValueTag.time); writer.svarint(checkedInteger(value, { kind: "int", bits: 64 })); return;
    case "enum": if (typeof value !== "string") throw new TypeError("wire: expected enum string"); writer.byte(ValueTag.string); writer.blob(textEncoder.encode(value)); return;
    case "list":
      if (!Array.isArray(value)) throw new TypeError("wire: expected list");
      writer.byte(ValueTag.list); writer.uvarint(BigInt(value.length));
      for (const item of value) encodeTypedInto(writer, item, type.elem, registry, depth + 1);
      return;
    case "array":
      if (!Array.isArray(value)) throw new TypeError("wire: expected array");
      if (value.length !== type.length) throw new RangeError("wire: array length mismatch");
      writer.byte(ValueTag.list); writer.uvarint(BigInt(value.length));
      for (const item of value) encodeTypedInto(writer, item, type.elem, registry, depth + 1);
      return;
    case "map":
      if (!isPlainObject(value)) throw new TypeError("wire: expected map");
      encodeMapTypedInto(writer, value, type.value, registry, depth); return;
    case "struct": {
      if (!isPlainObject(value)) throw new TypeError("wire: expected struct");
      const fields = Object.entries(type.fields);
      for (const [id] of fields) structFieldID(id);
      const encoded: Array<[bigint, { name: string; type: WireType; optional?: boolean }, unknown]> = [];
      for (const [id, field] of fields) {
        const hasField = Object.prototype.hasOwnProperty.call(value, field.name);
        const fieldValue = value[field.name];
        if (!hasField || (field.optional && fieldValue === undefined)) {
          if (!field.optional) throw new TypeError(`wire: missing required field ${field.name}`);
          continue;
        }
        encoded.push([BigInt(id), field, fieldValue]);
      }
      writer.byte(ValueTag.struct); writer.uvarint(BigInt(encoded.length));
      for (const [id, field, fieldValue] of encoded) { writer.uvarint(id); encodeTypedInto(writer, fieldValue, field.type, registry, depth + 1); }
      return;
    }
  }
}

function encodeMapTypedInto(writer: Writer, value: Record<string, unknown>, type: WireType, registry: WireTypeRegistry, depth: number): void {
  const entries = Object.entries(value);
  writer.byte(ValueTag.map); writer.uvarint(BigInt(entries.length));
  for (const [key, item] of entries) {
    writer.byte(ValueTag.string); writer.blob(textEncoder.encode(key));
    encodeTypedInto(writer, item, type, registry, depth + 1);
  }
}

export function encodeTyped(value: unknown, type: WireType, registry: WireTypeRegistry): Uint8Array {
  const writer = new Writer();
  encodeTypedInto(writer, value, type, registry, 0);
  return writer.result();
}

function decodeTypedFrom(reader: Reader, type: WireType, registry: WireTypeRegistry, depth: number): unknown {
  if (depth > MAX_DEPTH) throw new BinaryCodecError("wire: maximum nesting depth exceeded");
  if (type.kind === "named") {
    const named = registry[type.name];
    if (!named) throw new BinaryCodecError(`wire: unknown named type ${type.name}`);
    return decodeTypedFrom(reader, named, registry, depth);
  }
  if (type.kind === "nullable") {
    if (reader.peekByte() === ValueTag.null) { reader.byte(); return null; }
    return decodeTypedFrom(reader, type.elem, registry, depth + 1);
  }
  switch (type.kind) {
    case "null": expectTag(reader, ValueTag.null); return null;
    case "bool": { const tag = reader.byte(); if (tag === ValueTag.false) return false; if (tag === ValueTag.true) return true; throw new BinaryCodecError("wire: unexpected value kind"); }
    case "int": expectTag(reader, ValueTag.int); return decodedInteger(reader.svarint(), type);
    case "uint": expectTag(reader, ValueTag.uint); return decodedInteger(reader.uvarint(), type);
    case "float32": { expectTag(reader, ValueTag.float32); const b = reader.take(4); const n = new DataView(b.buffer, b.byteOffset, 4).getFloat32(0, true); if (!Number.isFinite(n)) throw new BinaryCodecError(); return n; }
    case "float64": { expectTag(reader, ValueTag.float64); const b = reader.take(8); const n = new DataView(b.buffer, b.byteOffset, 8).getFloat64(0, true); if (!Number.isFinite(n)) throw new BinaryCodecError(); return n; }
    case "string": expectTag(reader, ValueTag.string); return decodeUTF8(reader.blob());
    case "bytes": { expectTag(reader, ValueTag.bytes); const value = reader.blob(); if (type.length !== undefined && value.length !== type.length) throw new BinaryCodecError("wire: bytes length mismatch"); return value; }
    case "time": expectTag(reader, ValueTag.time); return reader.svarint();
    case "enum": expectTag(reader, ValueTag.string); return decodeUTF8(reader.blob());
    case "list": {
      expectTag(reader, ValueTag.list); const count = reader.uvarint(); if (count > BigInt(MAX_CONTAINER)) throw new BinaryCodecError();
      const result: unknown[] = []; for (let i = 0; i < Number(count); i++) result.push(decodeTypedFrom(reader, type.elem, registry, depth + 1)); return result;
    }
    case "array": {
      expectTag(reader, ValueTag.list); const count = reader.uvarint(); if (count !== BigInt(type.length)) throw new BinaryCodecError("wire: array length mismatch");
      const result: unknown[] = []; for (let i = 0; i < type.length; i++) result.push(decodeTypedFrom(reader, type.elem, registry, depth + 1)); return result;
    }
    case "map": {
      expectTag(reader, ValueTag.map); const count = reader.uvarint(); if (count > BigInt(MAX_CONTAINER)) throw new BinaryCodecError();
      const result = Object.create(null) as Record<string, unknown>; const seen = new Set<string>();
      for (let i = 0; i < Number(count); i++) { expectTag(reader, ValueTag.string); const key = decodeUTF8(reader.blob()); if (seen.has(key)) throw new BinaryCodecError("wire: duplicate map key"); seen.add(key); setOwn(result, key, decodeTypedFrom(reader, type.value, registry, depth + 1)); }
      return result;
    }
    case "struct": {
      expectTag(reader, ValueTag.struct); const count = reader.uvarint(); if (count > BigInt(MAX_CONTAINER)) throw new BinaryCodecError();
      const fields = new Map<string, { name: string; type: WireType; optional?: boolean }>();
      for (const [id, field] of Object.entries(type.fields)) { const key = structFieldID(id).toString(); if (fields.has(key)) throw new BinaryCodecError("wire: duplicate schema field"); fields.set(key, field); }
      const result = Object.create(null) as Record<string, unknown>; const seen = new Set<string>();
      for (let i = 0; i < Number(count); i++) { const id = reader.uvarint(); if (id === 0n || id > 0xffffffffn) throw new BinaryCodecError(); const key = id.toString(); if (seen.has(key)) throw new BinaryCodecError("wire: duplicate struct field"); seen.add(key); const field = fields.get(key); if (field) setOwn(result, field.name, decodeTypedFrom(reader, field.type, registry, depth + 1)); else decodeValueFrom(reader, depth + 1); }
      for (const [id, field] of fields) if (!field.optional && !seen.has(id)) throw new BinaryCodecError(`wire: missing required field ${field.name}`);
      return result;
    }
  }
}

export function decodeTyped(data: Uint8Array, type: WireType, registry: WireTypeRegistry): unknown {
  const reader = new Reader(data);
  const value = decodeTypedFrom(reader, type, registry, 0);
  if (!reader.done()) throw new BinaryCodecError();
  return value;
}

/** Encodes a timestamp as signed nanoseconds since the Unix epoch. */
export function encodeTimeNanoseconds(nanos: number | bigint): Uint8Array {
  const writer = new Writer();
  encodeTimeInto(writer, nanos);
  return writer.result();
}

export function encodeEnvelope(envelope: Envelope): Uint8Array {
  const code = frameCodes[envelope.type];
  if (!code) throw new BinaryCodecError("wire: unknown frame type");
  const writer = new Writer();
  writer.byte(1);
  writer.byte(code);
  writer.blob(textEncoder.encode(envelope.version ?? ""));
  writer.blob(textEncoder.encode(envelope.id ?? ""));
  writer.blob(textEncoder.encode(envelope.method ?? ""));
  writer.blob(textEncoder.encode(envelope.event ?? ""));
  writer.blob(envelope.payload ?? new Uint8Array());
  writer.blob(textEncoder.encode(envelope.error ?? ""));
  writer.blob(textEncoder.encode(envelope.errorCode ?? ""));
  return writer.result();
}

export function decodeEnvelope(data: Uint8Array): Envelope {
  const reader = new Reader(data);
  if (reader.byte() !== 1) throw new BinaryCodecError();
  const type = frameNames[reader.byte()];
  if (!type) throw new BinaryCodecError();
  const readString = (): string => decodeUTF8(reader.blob());
  const result: Envelope = {
    type,
    version: readString(),
    id: readString(),
    method: readString(),
    event: readString(),
    payload: reader.blob(),
    error: readString(),
    errorCode: readString(),
  };
  if (!reader.done()) throw new BinaryCodecError();
  return result;
}
