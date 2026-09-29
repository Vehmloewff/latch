import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:chat_app_client/client.dart';
import 'package:stream_channel/stream_channel.dart';
import 'package:test/test.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

class FakeWebSocketSink implements WebSocketSink {
  final List<Object?> sent = [];
  int? closeCode;
  String? closeReason;
  bool closed = false;

  @override
  Future<void> get done => Future<void>.value();

  @override
  void add(Object? data) {
    if (closed) throw StateError('fake socket is closed');
    sent.add(data);
  }

  @override
  void addError(Object error, [StackTrace? stackTrace]) {}

  @override
  Future<void> addStream(Stream<Object?> stream) async {
    await for (final value in stream) {
      add(value);
    }
  }

  @override
  Future<void> close([int? code, String? reason]) async {
    closed = true;
    closeCode = code;
    closeReason = reason;
  }
}

class FakeWebSocketChannel with StreamChannelMixin implements WebSocketChannel {
  final StreamController<dynamic> _incoming =
      StreamController<dynamic>(sync: true);
  late final FakeWebSocketSink fakeSink = FakeWebSocketSink();

  @override
  Future<void> get ready => Future<void>.value();

  @override
  WebSocketSink get sink => fakeSink;

  @override
  Stream<dynamic> get stream => _incoming.stream;

  @override
  String? get protocol => null;

  @override
  int? get closeCode => fakeSink.closeCode;

  @override
  String? get closeReason => fakeSink.closeReason;

  void receive(Object? data) => _incoming.add(data);

  Future<void> remoteClose() => _incoming.close();

  Future<void> dispose() async {
    await _incoming.close();
  }
}

Future<ConnectedLatchClient> connectedFakeClient(
  FakeWebSocketChannel channel, {
  void Function(Event)? onEvent,
  void Function()? onClose,
}) async {
  final subscription = channel.stream.listen(null);
  return ConnectedLatchClient(HandshakeResult(channel, subscription, []),
      onEvent ?? (_) {}, onClose ?? () {});
}

Future<void> expectLatchError(Future<dynamic> future, String code) async {
  await expectLater(
    future,
    throwsA(
      predicate<Object?>((error) => error is LatchError && error.code == code),
    ),
  );
}

Uint8List responseFrame(String id, Object? payload) => BinaryEnvelope(
      type: FrameCode.response,
      id: id,
      payload: BinaryCodec.encode(payload),
    ).encode();

void main() {
  test('generated client connects, adds version, and replays an early event',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));

    final serverTask = () async {
      final request = await server.first;
      expect(request.uri.queryParameters['version'], '1');
      expect(request.uri.queryParameters['existing'], 'yes');
      final socket = await WebSocketTransformer.upgrade(request);
      socket.add(BinaryEnvelope(
        type: FrameCode.event,
        event: 'chat.presence',
        payload: BinaryCodec.encode(StructValue({
          1: 'presence',
          3: StructValue({1: 'general', 2: 'bob', 3: true}),
        })),
      ).encode());
    }();

    final eventFuture = Completer<Event>();
    final states = <ConnectionState>[];
    final latch = LatchClient(
        ClientOptions(Uri.parse(
          'ws://127.0.0.1:${server.port}/chat?existing=yes',
        )),
        onEvent: eventFuture.complete,
        onConnectionStateChange: states.add);
    final connecting = latch.connect();
    expect(states, [ConnectionState.connecting]);
    final client = await connecting;
    expect(states, [ConnectionState.connecting, ConnectionState.connected]);
    final event = await eventFuture.future;
    expect(event.kind, 'presence');
    expect(event.presence!.room, 'general');
    expect(event.presence!.userId, 'bob');
    expect(event.presence!.online, isTrue);
    client.close();
    client.close();
    expect(states, [
      ConnectionState.connecting,
      ConnectionState.connected,
      ConnectionState.offline
    ]);
    await serverTask;
  });

  test('generated request and response use binary envelopes and typed methods',
      () async {
    final channel = FakeWebSocketChannel();
    final client = await connectedFakeClient(channel);
    addTearDown(channel.dispose);

    final response = client.chatHistory(HistoryRequest(room: 'general'));
    expect(channel.fakeSink.sent, hasLength(1));
    final request =
        BinaryEnvelope.decode(channel.fakeSink.sent.single as List<int>);
    expect(request.type, FrameCode.request);
    expect(request.id, '1');
    expect(request.method, 'chat_history');
    final requestPayload = BinaryCodec.decode(request.payload) as StructValue;
    expect(requestPayload.fields[1], 'general');

    channel.receive(responseFrame(
      request.id,
      StructValue({1: <Object?>[]}),
    ));
    expect((await response).messages, isEmpty);
  });

  test('error frames reject the matching generated request', () async {
    final channel = FakeWebSocketChannel();
    final client = await connectedFakeClient(channel);
    addTearDown(channel.dispose);

    final response = client.chatListRooms(ListRoomsRequest());
    final request =
        BinaryEnvelope.decode(channel.fakeSink.sent.single as List<int>);
    channel.receive(BinaryEnvelope(
      type: FrameCode.error,
      id: request.id,
      error: 'room unavailable',
      errorCode: 'room_unavailable',
    ).encode());

    await expectLater(
      response,
      throwsA(
        predicate<Object?>((error) =>
            error is LatchError &&
            error.code == 'room_unavailable' &&
            error.message == 'room unavailable'),
      ),
    );
  });

  test('malformed frames fail pending requests and close the channel',
      () async {
    final channel = FakeWebSocketChannel();
    final client = await connectedFakeClient(channel);
    addTearDown(channel.dispose);

    final response = client.chatHistory(HistoryRequest(room: 'general'));
    channel.receive(<int>[1, FrameCode.response]);

    await expectLatchError(response, 'protocol_violation');
    expect(client.closed, isTrue);
    expect(channel.fakeSink.closeCode, 1002);
    await expectLatchError(
      client.chatHistory(HistoryRequest(room: 'general')),
      'connection_closed',
    );
  });

  test('events dispatch and local or remote close fail pending requests',
      () async {
    final channel = FakeWebSocketChannel();
    final eventFuture = Completer<Event>();
    final client =
        await connectedFakeClient(channel, onEvent: eventFuture.complete);
    addTearDown(channel.dispose);
    channel.receive(BinaryEnvelope(
      type: FrameCode.event,
      event: 'chat.presence',
      payload: BinaryCodec.encode(StructValue({
        1: 'presence',
        3: StructValue({1: 'general', 2: 'alice', 3: false}),
      })),
    ).encode());
    final event = await eventFuture.future;
    expect(event.presence!.userId, 'alice');
    expect(event.presence!.online, isFalse);

    final pending = client.chatJoinRoom(
      JoinRoomRequest(room: 'general', userId: 'alice'),
    );
    final pendingError = expectLatchError(pending, 'connection_closed');
    client.close();
    await pendingError;
    expect(channel.fakeSink.closeCode, 1000);

    final remoteChannel = FakeWebSocketChannel();
    final remoteClient = await connectedFakeClient(remoteChannel);
    addTearDown(remoteChannel.dispose);
    final remotePending = remoteClient.chatListRooms(ListRoomsRequest());
    final remotePendingError =
        expectLatchError(remotePending, 'connection_closed');
    await remoteChannel.remoteClose();
    await remotePendingError;
    expect(remoteClient.closed, isTrue);
  });

  test('remote close reports offline once', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    final socketReady = Completer<WebSocket>();
    final serverTask = () async {
      final request = await server.first;
      socketReady.complete(await WebSocketTransformer.upgrade(request));
    }();
    final states = <ConnectionState>[];
    final offline = Completer<void>();
    final latch = LatchClient(
        ClientOptions(Uri.parse('ws://127.0.0.1:${server.port}/chat')),
        onEvent: (_) {}, onConnectionStateChange: (state) {
      states.add(state);
      if (state == ConnectionState.offline) offline.complete();
    });
    final client = await latch.connect();
    expect(states, [ConnectionState.connecting, ConnectionState.connected]);
    await (await socketReady.future).close();
    await offline.future;
    expect(client.closed, isTrue);
    client.close();
    expect(states, [
      ConnectionState.connecting,
      ConnectionState.connected,
      ConnectionState.offline
    ]);
    await serverTask;
  });

  test('connect rejects a connection_error received during setup', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));

    final serverTask = () async {
      final request = await server.first;
      final socket = await WebSocketTransformer.upgrade(request);
      socket.add(BinaryEnvelope(
        type: FrameCode.connectionError,
        error: 'bad token',
        errorCode: 'unauthorized',
      ).encode());
    }();

    final states = <ConnectionState>[];
    final latch = LatchClient(
        ClientOptions(Uri.parse('ws://127.0.0.1:${server.port}/chat')),
        onEvent: (_) {},
        onConnectionStateChange: states.add);
    final connecting = latch.connect();
    expect(states, [ConnectionState.connecting]);
    await expectLater(
      connecting,
      throwsA(
        predicate<Object?>((error) =>
            error is LatchError &&
            error.code == 'connect_rejected' &&
            error.message == 'bad token'),
      ),
    );
    expect(states, [ConnectionState.connecting, ConnectionState.offline]);
    await serverTask;
  });
}
