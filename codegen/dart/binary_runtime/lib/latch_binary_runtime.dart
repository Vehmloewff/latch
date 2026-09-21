import 'dart:convert';
import 'dart:typed_data';

const int maxDepth = 128;
const int maxContainer = 1 << 24;

class BinaryMalformedError extends FormatException {
  BinaryMalformedError([String message = 'malformed binary value'])
      : super(message);
}

/// An unsigned 64-bit wire integer. Dart's VM supports values larger than
/// 2^63; values outside that range are rejected by the wire encoder.
class UIntValue {
  final BigInt value;
  UIntValue(Object value)
      : value = value is BigInt ? value : BigInt.from(value as int);

  @override
  bool operator ==(Object other) => other is UIntValue && other.value == value;
  @override
  int get hashCode => value.hashCode;
  @override
  String toString() => 'UIntValue($value)';
}

/// Forces a Dart number to use the wire float32 tag.
class Float32Value {
  final double value;
  const Float32Value(this.value);

  @override
  bool operator ==(Object other) =>
      other is Float32Value && other.value == value;
  @override
  int get hashCode => value.hashCode;
}

/// A wire struct, whose fields are addressed by numeric IDs rather than names.
class StructValue {
  final Map<int, Object?> fields;
  StructValue(Map<int, Object?> fields) : fields = Map.unmodifiable(fields);

  @override
  bool operator ==(Object other) =>
      other is StructValue && _mapsEqual(fields, other.fields);
  @override
  int get hashCode =>
      fields.entries.fold(0, (h, e) => h ^ e.key.hashCode ^ e.value.hashCode);
}

bool _mapsEqual(Map<Object?, Object?> a, Map<Object?, Object?> b) {
  if (a.length != b.length) return false;
  for (final entry in a.entries) {
    if (!b.containsKey(entry.key) || b[entry.key] != entry.value) return false;
  }
  return true;
}

abstract final class ValueTag {
  static const int nullValue = 0;
  static const int falseValue = 1;
  static const int trueValue = 2;
  static const int intValue = 3;
  static const int uintValue = 4;
  static const int float32 = 5;
  static const int float64 = 6;
  static const int string = 7;
  static const int bytes = 8;
  static const int struct = 9;
  static const int list = 10;
  static const int map = 11;
  static const int time = 12;
}

abstract final class FrameCode {
  static const int connect = 1;
  static const int connected = 2;
  static const int request = 3;
  static const int response = 4;
  static const int error = 5;
  static const int event = 6;
  static const int connectionError = 7;
}

class BinaryEnvelope {
  final int type;
  final String version;
  final String id;
  final String method;
  final String event;
  final Uint8List payload;
  final String error;
  final String errorCode;

  BinaryEnvelope({
    required this.type,
    this.version = '',
    this.id = '',
    this.method = '',
    this.event = '',
    Uint8List? payload,
    this.error = '',
    this.errorCode = '',
  }) : payload = payload ?? Uint8List(0);

  Uint8List encode() {
    if (type < FrameCode.connect || type > FrameCode.connectionError) {
      throw BinaryMalformedError();
    }
    final out = _Writer()
      ..byte(1)
      ..byte(type);
    out.blob(utf8.encode(version));
    out.blob(utf8.encode(id));
    out.blob(utf8.encode(method));
    out.blob(utf8.encode(event));
    out.blob(payload);
    out.blob(utf8.encode(error));
    out.blob(utf8.encode(errorCode));
    return Uint8List.fromList(out.bytes);
  }

  static BinaryEnvelope decode(List<int> data) {
    final reader = _Reader(data);
    if (reader.byte() != 1) throw BinaryMalformedError();
    final type = reader.byte();
    if (type < FrameCode.connect || type > FrameCode.connectionError) {
      throw BinaryMalformedError();
    }
    String text() => utf8.decode(reader.blob(), allowMalformed: false);
    final result = BinaryEnvelope(
      type: type,
      version: text(),
      id: text(),
      method: text(),
      event: text(),
      payload: Uint8List.fromList(reader.blob()),
      error: text(),
      errorCode: text(),
    );
    reader.done();
    return result;
  }
}

abstract final class BinaryCodec {
  static Uint8List encode(Object? value) {
    final writer = _Writer();
    _encodeValue(writer, value, 0);
    return Uint8List.fromList(writer.bytes);
  }

  static Object? decode(List<int> data) {
    final reader = _Reader(data);
    final value = _decodeValue(reader, 0);
    reader.done();
    return value;
  }

  static void _encodeValue(_Writer out, Object? value, int depth) {
    _checkDepth(depth);
    if (value == null) return out.byte(ValueTag.nullValue);
    if (value is bool)
      return out.byte(value ? ValueTag.trueValue : ValueTag.falseValue);
    if (value is UIntValue) {
      _checkUint(value.value);
      out.byte(ValueTag.uintValue);
      return out.uvarint(value.value);
    }
    if (value is Float32Value) {
      out.byte(ValueTag.float32);
      out.float32(value.value);
      return;
    }
    if (value is int) {
      _checkInt(value);
      out.byte(ValueTag.intValue);
      return out.svarint(value);
    }
    if (value is double) {
      out.byte(ValueTag.float64);
      out.float64(value);
      return;
    }
    if (value is String) {
      out.byte(ValueTag.string);
      return out.blob(utf8.encode(value));
    }
    if (value is DateTime) {
      out.byte(ValueTag.time);
      return out.svarint(value.toUtc().microsecondsSinceEpoch * 1000);
    }
    if (value is Uint8List) {
      out.byte(ValueTag.bytes);
      return out.blob(value);
    }
    if (value is StructValue) {
      out.byte(ValueTag.struct);
      out.uvarint(value.fields.length);
      for (final entry in value.fields.entries) {
        if (entry.key <= 0 || entry.key > 0xffffffff)
          throw ArgumentError('struct field IDs must be uint32 and non-zero');
        out.uvarint(entry.key);
        _encodeValue(out, entry.value, depth + 1);
      }
      return;
    }
    if (value is List) {
      out.byte(ValueTag.list);
      out.uvarint(value.length);
      for (final item in value) _encodeValue(out, item, depth + 1);
      return;
    }
    if (value is Map) {
      out.byte(ValueTag.map);
      out.uvarint(value.length);
      for (final entry in value.entries) {
        if (entry.key is! String)
          throw ArgumentError('map keys must be strings');
        out.byte(ValueTag.string);
        out.blob(utf8.encode(entry.key as String));
        _encodeValue(out, entry.value, depth + 1);
      }
      return;
    }
    throw ArgumentError('unsupported binary value: ${value.runtimeType}');
  }

  static Object? _decodeValue(_Reader reader, int depth) {
    _checkDepth(depth);
    switch (reader.byte()) {
      case ValueTag.nullValue:
        return null;
      case ValueTag.falseValue:
        return false;
      case ValueTag.trueValue:
        return true;
      case ValueTag.intValue:
        return reader.svarint();
      case ValueTag.uintValue:
        return UIntValue(reader.uvarint());
      case ValueTag.float32:
        return Float32Value(reader.float32());
      case ValueTag.float64:
        return reader.float64();
      case ValueTag.string:
        return utf8.decode(reader.blob(), allowMalformed: false);
      case ValueTag.bytes:
        return Uint8List.fromList(reader.blob());
      case ValueTag.time:
        return DateTime.fromMicrosecondsSinceEpoch(
          reader.svarint() ~/ 1000,
          isUtc: true,
        );
      case ValueTag.list:
        final count = reader.count();
        return List<Object?>.generate(
          count,
          (_) => _decodeValue(reader, depth + 1),
        );
      case ValueTag.map:
        final count = reader.count();
        final result = <String, Object?>{};
        for (var i = 0; i < count; i++) {
          if (reader.byte() != ValueTag.string) throw BinaryMalformedError();
          final key = utf8.decode(reader.blob(), allowMalformed: false);
          result[key] = _decodeValue(reader, depth + 1);
        }
        return result;
      case ValueTag.struct:
        final count = reader.count();
        final result = <int, Object?>{};
        for (var i = 0; i < count; i++)
          result[reader.uvarint().toInt()] = _decodeValue(reader, depth + 1);
        return StructValue(result);
      default:
        throw BinaryMalformedError();
    }
  }

  static void _checkDepth(int depth) {
    if (depth > maxDepth)
      throw BinaryMalformedError('maximum nesting depth exceeded');
  }

  static void _checkUint(BigInt value) {
    if (value < BigInt.zero || value > BigInt.parse('18446744073709551615')) {
      throw ArgumentError('unsigned integer out of range');
    }
  }

  static void _checkInt(int value) {
    if (value < -0x8000000000000000 || value > 0x7fffffffffffffff)
      throw ArgumentError('signed integer out of range');
  }
}

class _Writer {
  final bytes = <int>[];
  void byte(int value) => bytes.add(value & 0xff);
  void uvarint(Object value) {
    var remaining = value is BigInt ? value : BigInt.from(value as int);
    while (remaining >= BigInt.from(0x80)) {
      byte((remaining & BigInt.from(0x7f)).toInt() | 0x80);
      remaining >>= 7;
    }
    byte(remaining.toInt());
  }

  void svarint(int value) => uvarint(
        value < 0 ? ((-value * 2) - 1) : value * 2,
      );
  void blob(List<int> value) {
    uvarint(value.length);
    bytes.addAll(value);
  }

  void float32(double value) {
    final data = ByteData(4)..setFloat32(0, value, Endian.little);
    bytes.addAll(data.buffer.asUint8List());
  }

  void float64(double value) {
    final data = ByteData(8)..setFloat64(0, value, Endian.little);
    bytes.addAll(data.buffer.asUint8List());
  }
}

class _Reader {
  final List<int> data;
  int position = 0;
  _Reader(this.data);
  int byte() {
    if (position >= data.length) throw BinaryMalformedError();
    return data[position++];
  }

  BigInt uvarint() {
    var result = BigInt.zero;
    for (var shift = 0; shift < 64; shift += 7) {
      final value = byte();
      if (shift == 63 && value > 1) throw BinaryMalformedError();
      result |= BigInt.from(value & 0x7f) << shift;
      if (value < 0x80) return result;
    }
    throw BinaryMalformedError();
  }

  int svarint() {
    final value = uvarint();
    final signed = (value >> 1) ^ (-(value & BigInt.one));
    return signed.toInt();
  }

  List<int> blob() {
    final length = uvarint();
    if (length > BigInt.from(maxContainer) ||
        length > BigInt.from(data.length - position)) {
      throw BinaryMalformedError();
    }
    final size = length.toInt();
    final result = data.sublist(position, position + size);
    position += size;
    return result;
  }

  int count() {
    final result = uvarint();
    if (result > BigInt.from(maxContainer)) throw BinaryMalformedError();
    return result.toInt();
  }

  double float32() {
    final bytes = take(4);
    return ByteData.sublistView(Uint8List.fromList(bytes))
        .getFloat32(0, Endian.little);
  }

  double float64() {
    final bytes = take(8);
    return ByteData.sublistView(Uint8List.fromList(bytes))
        .getFloat64(0, Endian.little);
  }

  List<int> take(int length) {
    if (length > data.length - position) throw BinaryMalformedError();
    final result = data.sublist(position, position + length);
    position += length;
    return result;
  }

  void done() {
    if (position != data.length) throw BinaryMalformedError();
  }
}
