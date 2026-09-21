import 'dart:typed_data';

import 'package:latch_binary_runtime/latch_binary_runtime.dart';
import 'package:test/test.dart';

void main() {
  test('round trips all value kinds', () {
    final input = StructValue({
      1: UIntValue(BigInt.parse('18446744073709551615')),
      2: true,
      3: 'hello 👋',
      4: Float32Value(1.5),
      5: DateTime.fromMicrosecondsSinceEpoch(123456, isUtc: true),
      6: [-1, 0, 100000],
      7: {'x': 'y'},
      8: Uint8List.fromList([0, 1, 255]),
    });

    final output = BinaryCodec.decode(BinaryCodec.encode(input)) as StructValue;
    expect(output.fields[1], UIntValue(BigInt.parse('18446744073709551615')));
    expect(output.fields[2], isTrue);
    expect(output.fields[3], 'hello 👋');
    expect((output.fields[4] as Float32Value).value, closeTo(1.5, 0.00001));
    expect(
      output.fields[5],
      DateTime.fromMicrosecondsSinceEpoch(123456, isUtc: true),
    );
    expect(output.fields[6], [-1, 0, 100000]);
    expect(output.fields[7], {'x': 'y'});
    expect(output.fields[8], orderedEquals([0, 1, 255]));
  });

  test('uses exact envelope layout and frame codes', () {
    final envelope = BinaryEnvelope(
      type: FrameCode.response,
      id: '42',
      payload: Uint8List.fromList([1, 2, 3]),
      errorCode: 'x',
    );
    expect(envelope.encode(), [
      1,
      4,
      0,
      2,
      52,
      50,
      0,
      0,
      3,
      1,
      2,
      3,
      0,
      1,
      120,
    ]);
    final decoded = BinaryEnvelope.decode(envelope.encode());
    expect(decoded.type, FrameCode.response);
    expect(decoded.id, '42');
    expect(decoded.payload, [1, 2, 3]);
    expect(decoded.errorCode, 'x');
  });

  test('encodes signed and unsigned varints', () {
    expect(BinaryCodec.encode(-1), [ValueTag.intValue, 1]);
    expect(BinaryCodec.encode(64), [ValueTag.intValue, 128, 1]);
    expect(BinaryCodec.encode(UIntValue(300)), [
      ValueTag.uintValue,
      172,
      2,
    ]);
  });

  test('skips unknown struct fields by decoding the numeric struct', () {
    final value = BinaryCodec.decode(
      BinaryCodec.encode(
        StructValue({
          1: 7,
          99: ['ignored'],
        }),
      ),
    ) as StructValue;
    expect(value.fields[1], 7);
    expect(value.fields[99], ['ignored']);
  });

  test('matches shared Go/TypeScript canonical vectors', () {
    expect(BinaryCodec.encode(-1), [3, 1]);
    expect(BinaryCodec.encode(64), [3, 128, 1]);
    expect(BinaryCodec.encode(UIntValue(300)), [4, 172, 2]);
    expect(BinaryCodec.encode(Float32Value(1)), [5, 0, 0, 128, 63]);
    expect(BinaryCodec.encode(1.0), [6, 0, 0, 0, 0, 0, 0, 240, 63]);
    expect(BinaryCodec.encode('hello'), [7, 5, 104, 101, 108, 108, 111]);
    expect(BinaryCodec.encode(Uint8List.fromList([0, 255, 127])), [8, 3, 0, 255, 127]);
    final envelope = BinaryEnvelope(type: FrameCode.response, id: '42', payload: Uint8List.fromList([1, 2, 3]), errorCode: 'x');
    expect(envelope.encode(), [1, 4, 0, 2, 52, 50, 0, 0, 3, 1, 2, 3, 0, 1, 120]);
  });

  test('rejects malformed and trailing data', () {
    expect(
      () => BinaryCodec.decode([ValueTag.string, 255]),
      throwsA(isA<BinaryMalformedError>()),
    );
    expect(
      () => BinaryCodec.decode([ValueTag.nullValue, 0]),
      throwsA(isA<BinaryMalformedError>()),
    );
    expect(
      () => BinaryEnvelope.decode([1, FrameCode.response]),
      throwsA(isA<BinaryMalformedError>()),
    );
  });
}
