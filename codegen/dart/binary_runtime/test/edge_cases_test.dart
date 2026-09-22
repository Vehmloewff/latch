import 'dart:typed_data';

import 'package:latch_binary_runtime/latch_binary_runtime.dart';
import 'package:test/test.dart';

final BigInt _int64Min = -(BigInt.one << 63);
final BigInt _int64Max = (BigInt.one << 63) - BigInt.one;
final BigInt _uint64Max = (BigInt.one << 64) - BigInt.one;
final BigInt _maxContainer = BigInt.from(1 << 24);

List<int> _uvarint(BigInt value) {
  if (value < BigInt.zero) throw ArgumentError.value(value);
  final result = <int>[];
  do {
    var byte = (value & BigInt.from(0x7f)).toInt();
    value >>= 7;
    if (value != BigInt.zero) byte |= 0x80;
    result.add(byte);
  } while (value != BigInt.zero);
  return result;
}

List<int> _zigzag(BigInt value) {
  final encoded = value.isNegative
      ? (-value * BigInt.two) - BigInt.one
      : value * BigInt.two;
  return _uvarint(encoded);
}

List<int> _valueWithVarint(int tag, List<int> varint) => [tag, ...varint];

List<int> _blob(List<int> bytes) =>
    [..._uvarint(BigInt.from(bytes.length)), ...bytes];

List<int> _envelopeBytes({
  int type = FrameCode.response,
  List<int>? version,
  List<int>? id,
  List<int>? method,
  List<int>? event,
  List<int>? payload,
  List<int>? error,
  List<int>? errorCode,
}) {
  final fields = <List<int>>[
    version ?? const [],
    id ?? const [],
    method ?? const [],
    event ?? const [],
    payload ?? const [],
    error ?? const [],
    errorCode ?? const [],
  ];
  return [
    1,
    type,
    for (final field in fields) ..._blob(field),
  ];
}

List<int> _nestedLists(int depth) {
  var value = <int>[ValueTag.nullValue];
  for (var i = 0; i < depth; i++) {
    value = [ValueTag.list, 1, ...value];
  }
  return value;
}

void _expectMalformed(Object? Function() action) {
  expect(action, throwsA(isA<BinaryMalformedError>()));
}

void _expectArgumentError(Object? Function() action) {
  expect(action, throwsA(isA<ArgumentError>()));
}

void main() {
  group('timestamps', () {
    test('preserves the signed nanosecond wire range that maps to microseconds',
        () {
      final largestMicrosecond = (_int64Max ~/ BigInt.from(1000)).toInt();
      final smallestMicrosecond = ((-_int64Max) ~/ BigInt.from(1000)).toInt();
      final values = [
        DateTime.fromMicrosecondsSinceEpoch(largestMicrosecond, isUtc: true),
        DateTime.fromMicrosecondsSinceEpoch(smallestMicrosecond, isUtc: true),
      ];

      for (final value in values) {
        final decoded =
            BinaryCodec.decode(BinaryCodec.encode(value)) as DateTime;
        expect(decoded, value);
        expect(decoded.isUtc, isTrue);
      }
    });

    test('rejects sub-microsecond timestamps instead of truncating them', () {
      _expectMalformed(
        () => BinaryCodec.decode(
            _valueWithVarint(ValueTag.time, _zigzag(BigInt.one))),
      );
      _expectMalformed(
        () => BinaryCodec.decode(
          _valueWithVarint(ValueTag.time, _zigzag(BigInt.from(1001))),
        ),
      );
    });

    test('rejects timestamps outside the signed nanosecond range', () {
      final tooLargeDate = DateTime.fromMicrosecondsSinceEpoch(
        (_int64Max ~/ BigInt.from(1000) + BigInt.one).toInt(),
        isUtc: true,
      );
      _expectArgumentError(() => BinaryCodec.encode(tooLargeDate));

      _expectMalformed(
        () => BinaryCodec.decode(
          _valueWithVarint(ValueTag.time, _zigzag(_int64Max)),
        ),
      );
      _expectMalformed(
        () => BinaryCodec.decode(
          _valueWithVarint(ValueTag.time, _zigzag(_int64Min)),
        ),
      );
    });
  });

  group('signed and unsigned integers', () {
    test('round trips signed and unsigned 64-bit boundaries', () {
      for (final value in [_int64Min, _int64Max]) {
        final encoded = _valueWithVarint(ValueTag.intValue, _zigzag(value));
        expect(BinaryCodec.decode(encoded), value.toInt());
      }

      expect(
        BinaryCodec.decode(
          _valueWithVarint(ValueTag.uintValue, _uvarint(BigInt.zero)),
        ),
        UIntValue(BigInt.zero),
      );
      expect(
        BinaryCodec.decode(
          _valueWithVarint(ValueTag.uintValue, _uvarint(_uint64Max)),
        ),
        UIntValue(_uint64Max),
      );
      expect(BinaryCodec.encode(_int64Min.toInt()),
          [ValueTag.intValue, ..._zigzag(_int64Min)]);
      expect(BinaryCodec.encode(_int64Max.toInt()),
          [ValueTag.intValue, ..._zigzag(_int64Max)]);
      expect(BinaryCodec.encode(UIntValue(_uint64Max)),
          [ValueTag.uintValue, ..._uvarint(_uint64Max)]);
    });

    test('rejects unsigned encoder overflow', () {
      _expectArgumentError(
          () => BinaryCodec.encode(UIntValue(BigInt.from(-1))));
      _expectArgumentError(
          () => BinaryCodec.encode(UIntValue(_uint64Max + BigInt.one)));
    });

    test(
        'rejects overflowing and noncanonical varints in every length/count position',
        () {
      final noncanonical = <List<int>>[
        [ValueTag.intValue, 0x80, 0x00],
        [ValueTag.uintValue, 0x80, 0x00],
        [ValueTag.time, 0x80, 0x00],
        [ValueTag.string, 0x80, 0x00],
        [ValueTag.bytes, 0x80, 0x00],
        [ValueTag.list, 0x80, 0x00],
        [ValueTag.map, 0x80, 0x00],
        [ValueTag.struct, 0x80, 0x00],
      ];
      for (final bytes in noncanonical) {
        _expectMalformed(() => BinaryCodec.decode(bytes));
      }

      final overflowing = [
        ...List<int>.filled(9, 0xff),
        0x02,
      ];
      _expectMalformed(
        () => BinaryCodec.decode([ValueTag.uintValue, ...overflowing]),
      );
      _expectMalformed(
        () => BinaryCodec.decode([ValueTag.intValue, ...overflowing]),
      );
      _expectMalformed(
        () => BinaryCodec.decode([ValueTag.string, ...overflowing]),
      );
      _expectMalformed(
        () => BinaryCodec.decode([ValueTag.list, ...overflowing]),
      );
      _expectMalformed(
        () => BinaryCodec.decode([
          ValueTag.uintValue,
          ...List<int>.filled(10, 0x80),
        ]),
      );
    });
  });

  group('malformed values', () {
    test('reports invalid UTF-8 consistently as BinaryMalformedError', () {
      _expectMalformed(() => BinaryCodec.decode([ValueTag.string, 1, 0xff]));
      _expectMalformed(
        () => BinaryCodec.decode([
          ValueTag.map,
          1,
          ValueTag.string,
          1,
          0xff,
          ValueTag.nullValue,
        ]),
      );

      for (final field in [0, 1, 2, 3, 5, 6]) {
        final fields = List<List<int>>.generate(7, (_) => <int>[]);
        fields[field] = [0xff];
        _expectMalformed(
          () => BinaryEnvelope.decode(_envelopeBytes(
            version: fields[0],
            id: fields[1],
            method: fields[2],
            event: fields[3],
            payload: fields[4],
            error: fields[5],
            errorCode: fields[6],
          )),
        );
      }
    });

    test('rejects invalid and duplicate struct field IDs', () {
      _expectMalformed(
        () => BinaryCodec.decode([
          ValueTag.struct,
          1,
          0,
          ValueTag.nullValue,
        ]),
      );
      _expectMalformed(
        () => BinaryCodec.decode([
          ValueTag.struct,
          1,
          ..._uvarint(BigInt.from(0x100000000)),
          ValueTag.nullValue,
        ]),
      );
      _expectMalformed(
        () => BinaryCodec.decode([
          ValueTag.struct,
          2,
          1,
          ValueTag.nullValue,
          1,
          ValueTag.trueValue,
        ]),
      );

      _expectArgumentError(() => BinaryCodec.encode(StructValue({0: null})));
      _expectArgumentError(
        () => BinaryCodec.encode(
          StructValue({0x100000000: null}),
        ),
      );
    });

    test('rejects duplicate map keys instead of silently overwriting', () {
      _expectMalformed(
        () => BinaryCodec.decode([
          ValueTag.map,
          2,
          ValueTag.string,
          3,
          115,
          97,
          109,
          ValueTag.intValue,
          1,
          ValueTag.string,
          3,
          115,
          97,
          109,
          ValueTag.intValue,
          2,
        ]),
      );
    });

    test('rejects truncation and trailing bytes for every value family', () {
      final truncated = <List<int>>[
        const [],
        [ValueTag.intValue],
        [ValueTag.uintValue],
        [ValueTag.time],
        [ValueTag.string, 1],
        [ValueTag.bytes, 1],
        [ValueTag.float32, 0, 0, 0],
        [ValueTag.float64, 0, 0, 0, 0, 0, 0, 0],
        [ValueTag.list, 1],
        [ValueTag.map, 1],
        [ValueTag.struct, 1],
      ];
      for (final bytes in truncated) {
        _expectMalformed(() => BinaryCodec.decode(bytes));
      }

      _expectMalformed(() => BinaryCodec.decode([ValueTag.nullValue, 0]));
      _expectMalformed(() => BinaryCodec.decode([ValueTag.trueValue, 0]));

      _expectMalformed(
        () => BinaryCodec.decode([
          ValueTag.string,
          ..._uvarint(_maxContainer + BigInt.one),
        ]),
      );
      _expectMalformed(
        () => BinaryCodec.decode([
          ValueTag.list,
          ..._uvarint(_maxContainer + BigInt.one),
        ]),
      );
    });

    test('enforces the maximum nesting depth at the boundary', () {
      Object? decoded = BinaryCodec.decode(_nestedLists(128));
      for (var i = 0; i < 128; i++) {
        expect(decoded, isA<List<Object?>>());
        decoded = (decoded as List<Object?>).single;
      }
      expect(decoded, isNull);
      _expectMalformed(() => BinaryCodec.decode(_nestedLists(129)));
    });
  });

  group('envelopes', () {
    test('round trips every frame code and every envelope field', () {
      for (final type in [
        FrameCode.connect,
        FrameCode.connected,
        FrameCode.request,
        FrameCode.response,
        FrameCode.error,
        FrameCode.event,
        FrameCode.connectionError,
      ]) {
        final input = BinaryEnvelope(
          type: type,
          version: 'v1',
          id: 'id-7',
          method: 'method.name',
          event: 'event.name',
          payload: Uint8List.fromList([0, 1, 255]),
          error: 'failure',
          errorCode: 'failure_code',
        );
        final output = BinaryEnvelope.decode(input.encode());
        expect(output.type, type);
        expect(output.version, 'v1');
        expect(output.id, 'id-7');
        expect(output.method, 'method.name');
        expect(output.event, 'event.name');
        expect(output.payload, [0, 1, 255]);
        expect(output.error, 'failure');
        expect(output.errorCode, 'failure_code');
      }
    });

    test(
        'rejects invalid frame codes, bad versions, truncation, and trailing bytes',
        () {
      _expectMalformed(() => BinaryEnvelope.decode([]));
      _expectMalformed(() => BinaryEnvelope.decode([2]));
      _expectMalformed(() => BinaryEnvelope.decode([1, 0]));
      _expectMalformed(() => BinaryEnvelope.decode([1, 8]));
      _expectMalformed(() => BinaryEnvelope.decode(_envelopeBytes()..add(0)));

      final complete = _envelopeBytes();
      for (var length = 0; length < complete.length; length++) {
        _expectMalformed(
            () => BinaryEnvelope.decode(complete.sublist(0, length)));
      }

      _expectMalformed(() => BinaryEnvelope(type: 0).encode());
      _expectMalformed(() => BinaryEnvelope(type: 8).encode());
    });
  });

  group('nulls, containers, and shared fixtures', () {
    test('preserves null and empty container behavior', () {
      expect(BinaryCodec.encode(null), [ValueTag.nullValue]);
      expect(BinaryCodec.decode([ValueTag.nullValue]), isNull);
      expect(BinaryCodec.decode(BinaryCodec.encode(<Object?>[])), <Object?>[]);
      expect(BinaryCodec.decode(BinaryCodec.encode(<String, Object?>{})),
          <String, Object?>{});
      expect(
        BinaryCodec.decode(BinaryCodec.encode(StructValue({}))),
        StructValue({}),
      );
      expect(
        BinaryCodec.decode(
          BinaryCodec.encode(<Object?>[
            null,
            <String, Object?>{'null': null}
          ]),
        ),
        [
          null,
          {'null': null}
        ],
      );
      _expectArgumentError(() => BinaryCodec.encode({1: 'not a string key'}));
    });

    test('decodes the shared Go and TypeScript fixture vectors', () {
      final vectors = <String, List<int>>{
        'int_minus_one': [3, 1],
        'int_64': [3, 128, 1],
        'uint_300': [4, 172, 2],
        'float32_one': [5, 0, 0, 128, 63],
        'float64_one': [6, 0, 0, 0, 0, 0, 0, 240, 63],
        'string_hello': [7, 5, 104, 101, 108, 108, 111],
        'bytes_binary': [8, 3, 0, 255, 127],
        'struct_nested': [
          9,
          4,
          1,
          7,
          5,
          104,
          101,
          108,
          108,
          111,
          2,
          3,
          37,
          3,
          10,
          3,
          0,
          2,
          6,
          0,
          0,
          0,
          0,
          0,
          0,
          248,
          63,
          4,
          8,
          3,
          1,
          2,
          255,
        ],
      };

      expect(BinaryCodec.decode(vectors['int_minus_one']!), -1);
      expect(BinaryCodec.decode(vectors['int_64']!), 64);
      expect(BinaryCodec.decode(vectors['uint_300']!),
          UIntValue(BigInt.from(300)));
      expect(BinaryCodec.decode(vectors['float32_one']!), Float32Value(1));
      expect(BinaryCodec.decode(vectors['float64_one']!), 1.0);
      expect(BinaryCodec.decode(vectors['string_hello']!), 'hello');
      expect(BinaryCodec.decode(vectors['bytes_binary']!), [0, 255, 127]);

      final struct =
          BinaryCodec.decode(vectors['struct_nested']!) as StructValue;
      expect(struct.fields[1], 'hello');
      expect(struct.fields[2], -19);
      expect(struct.fields[3], [null, true, 1.5]);
      expect(struct.fields[4], [1, 2, 255]);
    });
  });

  test('runtime reports malformed input with its own error type', () {
    expect(
      () => BinaryCodec.decode([
        ValueTag.string,
        1,
        0xff,
      ]),
      throwsA(isA<BinaryMalformedError>()),
    );
    expect(
      () => BinaryCodec.decode([
        ValueTag.nullValue,
        0,
      ]),
      throwsA(isA<BinaryMalformedError>()),
    );
  });
}
