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

  uvarint(): bigint {
    let value = 0n;
    for (let i = 0; i < 10; i++) {
      const b = this.byte();
      if (i === 9 && (b & 0xfe) !== 0) throw new BinaryCodecError();
      value |= BigInt(b & 0x7f) << BigInt(i * 7);
      if ((b & 0x80) === 0) return value;
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
    case ValueTag.string: return textDecoder.decode(reader.blob());
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
      const result: MapValue = {};
      for (let i = 0; i < Number(count); i++) {
        if (reader.byte() !== ValueTag.string) throw new BinaryCodecError();
        const key = textDecoder.decode(reader.blob());
        result[key] = decodeValueFrom(reader, depth + 1);
      }
      return result;
    }
    case ValueTag.struct: {
      const count = reader.uvarint();
      if (count > BigInt(MAX_CONTAINER)) throw new BinaryCodecError();
      const result: StructValue = {};
      for (let i = 0; i < Number(count); i++) {
        const id = reader.uvarint();
        if (id === 0n || id > 0xffffffffn) throw new BinaryCodecError();
        result[id.toString()] = decodeValueFrom(reader, depth + 1);
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
  | { kind: "null" | "bool" | "int" | "uint" | "float32" | "float64" | "string" | "bytes" | "time" }
  | { kind: "enum" }
  | { kind: "nullable" | "list"; elem: WireType }
  | { kind: "map"; value: WireType }
  | { kind: "named"; name: string }
  | { kind: "struct"; fields: Record<string, { name: string; type: WireType; optional?: boolean }> };

export type WireTypeRegistry = Record<string, WireType>;

function encodeTypedInto(writer: Writer, value: unknown, type: WireType, registry: WireTypeRegistry, depth: number): void {
  if (depth > MAX_DEPTH) throw new BinaryCodecError("wire: maximum nesting depth exceeded");
  if (type.kind === "named") {
    const named = registry[type.name];
    if (!named) throw new BinaryCodecError(`wire: unknown named type ${type.name}`);
    encodeTypedInto(writer, value, named, registry, depth);
    return;
  }
  if (type.kind === "nullable") {
    if (value === null || value === undefined) { writer.byte(ValueTag.null); return; }
    encodeTypedInto(writer, value, type.elem, registry, depth + 1);
    return;
  }
  switch (type.kind) {
    case "null": writer.byte(ValueTag.null); return;
    case "bool": if (typeof value !== "boolean") throw new TypeError("wire: expected boolean"); writer.byte(value ? ValueTag.true : ValueTag.false); return;
    case "int": writer.byte(ValueTag.int); writer.svarint(asBigInt(value as number | bigint)); return;
    case "uint": writer.byte(ValueTag.uint); writer.uvarint(asBigInt(value as number | bigint)); return;
    case "float32": writer.byte(ValueTag.float32); writer.raw(encodeRawFloat(Number(value), 4)); return;
    case "float64": writer.byte(ValueTag.float64); writer.raw(encodeRawFloat(Number(value), 8)); return;
    case "string": writer.byte(ValueTag.string); writer.blob(textEncoder.encode(String(value))); return;
    case "bytes": if (!(value instanceof Uint8Array)) throw new TypeError("wire: expected bytes"); writer.byte(ValueTag.bytes); writer.blob(value); return;
    case "time": writer.byte(ValueTag.time); writer.svarint(value as number | bigint); return;
    case "enum": writer.byte(ValueTag.string); writer.blob(textEncoder.encode(String(value))); return;
    case "list":
      if (!Array.isArray(value)) throw new TypeError("wire: expected list");
      writer.byte(ValueTag.list); writer.uvarint(BigInt(value.length));
      for (const item of value) encodeTypedInto(writer, item, type.elem, registry, depth + 1);
      return;
    case "map":
      if (!isPlainObject(value)) throw new TypeError("wire: expected map");
      encodeMapTypedInto(writer, value, type.value, registry, depth);
      return;
    case "struct": {
      if (!isPlainObject(value)) throw new TypeError("wire: expected struct");
      const fields = Object.entries(type.fields).filter(([, field]) => !field.optional || (value as Record<string, unknown>)[field.name] !== undefined);
      writer.byte(ValueTag.struct); writer.uvarint(BigInt(fields.length));
      for (const [id, field] of fields) {
        writer.uvarint(BigInt(id));
        encodeTypedInto(writer, (value as Record<string, unknown>)[field.name], field.type, registry, depth + 1);
      }
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

function decodeTypedValue(value: WireValue, type: WireType, registry: WireTypeRegistry): unknown {
  if (type.kind === "named") {
    const named = registry[type.name];
    if (!named) throw new BinaryCodecError(`wire: unknown named type ${type.name}`);
    return decodeTypedValue(value, named, registry);
  }
  if (value === null) return null;
  if (type.kind === "nullable") return decodeTypedValue(value, type.elem, registry);
  switch (type.kind) {
    case "int": case "uint": return typeof value === "bigint" ? Number(value) : value;
    case "float32": case "float64": case "string": case "bool": case "bytes": case "enum": case "time": return value;
    case "null": return null;
    case "list": if (!Array.isArray(value)) throw new BinaryCodecError(); return value.map(item => decodeTypedValue(item, type.elem, registry));
    case "map": {
      if (!isPlainObject(value)) throw new BinaryCodecError();
      const result: Record<string, unknown> = {};
      for (const [key, item] of Object.entries(value)) result[key] = decodeTypedValue(item, type.value, registry);
      return result;
    }
    case "struct": {
      if (!isPlainObject(value)) throw new BinaryCodecError();
      const result: Record<string, unknown> = {};
      for (const [id, field] of Object.entries(type.fields)) {
        if (Object.prototype.hasOwnProperty.call(value, id)) result[field.name] = decodeTypedValue(value[id], field.type, registry);
      }
      return result;
    }
  }
}

export function decodeTyped(data: Uint8Array, type: WireType, registry: WireTypeRegistry): unknown {
  return decodeTypedValue(decodeValue(data), type, registry);
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
  const readString = (): string => textDecoder.decode(reader.blob());
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
