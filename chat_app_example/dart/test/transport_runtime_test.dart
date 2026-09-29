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
    expect(client.closed, isFalse);
    expect(channel.fakeSink.closeCode, 1002);
    client.close();
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
    expect(remoteClient.closed, isFalse);
    remoteClient.close();
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
    expect(client.closed, isFalse);
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
    await serverTask;
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(states, [ConnectionState.connecting, ConnectionState.offline]);
    latch.close();
    await expectLatchError(connecting, 'connection_closed');
  });

  test(
      'initial transient failure retries and keeps the same client after reconnect',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    final states = <ConnectionState>[];
    final offline = Completer<void>();
    final latch = LatchClient(
        ClientOptions(Uri.parse('ws://127.0.0.1:${server.port}/chat')),
        onEvent: (_) {}, onConnectionStateChange: (state) {
      states.add(state);
      if (state == ConnectionState.offline && !offline.isCompleted)
        offline.complete();
    });
    final requests = StreamIterator<HttpRequest>(server);
    addTearDown(requests.cancel);
    final connecting = latch.connect();
    expect(await requests.moveNext(), isTrue);
    final rejected = await WebSocketTransformer.upgrade(requests.current);
    await rejected.close();
    await offline.future.timeout(const Duration(seconds: 4));
    expect(states, contains(ConnectionState.offline));
    expect(
        await requests.moveNext().timeout(const Duration(seconds: 4)), isTrue);
    final accepted = await WebSocketTransformer.upgrade(requests.current);
    final client = await connecting;
    expect(states.last, ConnectionState.connected);
    await accepted.close();
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(client.closed, isFalse);
    latch.close();
    expect(client.closed, isTrue);
  });

  test('explicit close cancels an initial connection waiting for upgrade',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    final request = Completer<HttpRequest>();
    final subscription = server.listen(request.complete);
    addTearDown(subscription.cancel);
    final states = <ConnectionState>[];
    final latch = LatchClient(
      ClientOptions(Uri.parse('ws://127.0.0.1:${server.port}/chat')),
      onEvent: (_) {},
      onConnectionStateChange: states.add,
    );
    final connecting = latch.connect();
    final pendingRequest =
        await request.future.timeout(const Duration(seconds: 4));
    latch.close();
    await expectLatchError(
        connecting.timeout(const Duration(seconds: 4)), 'connection_closed');
    await pendingRequest.response.close();
    expect(states, [ConnectionState.connecting, ConnectionState.offline]);
  });

  test('offline queue flushes in order without replaying sent calls', () async {
    final first = FakeWebSocketChannel();
    final second = FakeWebSocketChannel();
    final events = <Event>[];
    final client = await connectedFakeClient(first, onEvent: events.add);
    addTearDown(first.dispose);
    addTearDown(second.dispose);
    final sent = client.chatListRooms(ListRoomsRequest());
    final failed = expectLatchError(sent, 'connection_closed');
    await first.remoteClose();
    await failed;
    expect(client.closed, isFalse);
    final a = client.chatListRooms(ListRoomsRequest());
    final b = client.chatListRooms(ListRoomsRequest());
    client.reconnect(HandshakeResult(second, second.stream.listen(null), []));
    final ids = second.fakeSink.sent
        .map((raw) => BinaryEnvelope.decode(raw as List<int>).id)
        .toList();
    expect(ids, ['2', '3']);
    for (final id in ids) {
      second.receive(responseFrame(id, StructValue({1: <Object?>[]})));
    }
    await Future.wait([a, b]);
    second.receive(BinaryEnvelope(
      type: FrameCode.event,
      event: 'chat.presence',
      payload: BinaryCodec.encode(StructValue({
        1: 'presence',
        3: StructValue({1: 'general', 2: 'bob', 3: true}),
      })),
    ).encode());
    expect(events.single.presence!.userId, 'bob');
    await second.remoteClose();
    final queued = client.chatListRooms(ListRoomsRequest());
    final rejected = expectLatchError(queued, 'connection_closed');
    client.close();
    await rejected;
  });
}
