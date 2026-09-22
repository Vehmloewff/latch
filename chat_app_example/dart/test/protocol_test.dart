import 'package:chat_app_client/client.dart';
import 'package:test/test.dart';

void main() {
  test('binary protocol round-trips a chat message event', () {
    final original = Event(
      kind: 'message',
      message: MessageReceived(
        message: ChatMessage(
          id: 7,
          room: 'general',
          senderId: 'alice',
          text: 'hello',
          sentAt: DateTime.fromMillisecondsSinceEpoch(123000, isUtc: true),
        ),
      ),
    );
    final encoded = BinaryCodec.encode(original.toBinary());
    final decoded = Event.fromBinary(BinaryCodec.decode(encoded));
    expect(decoded.kind, original.kind);
    expect(decoded.message!.message.text, 'hello');
    expect(decoded.message!.message.senderId, 'alice');
    expect(decoded.message!.message.sentAt, original.message!.message.sentAt);
  });

  test('binary protocol preserves presence events and field values', () {
    final original = Event(
      kind: 'presence',
      presence: PresenceChanged(room: 'general', userId: 'bob', online: true),
    );
    final decoded = Event.fromBinary(
        BinaryCodec.decode(BinaryCodec.encode(original.toBinary())));
    expect(decoded.kind, 'presence');
    expect(decoded.presence!.room, 'general');
    expect(decoded.presence!.userId, 'bob');
    expect(decoded.presence!.online, isTrue);
  });
}
