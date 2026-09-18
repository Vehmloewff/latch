package dart

// runtimeBody is the language runtime shared by every generated Dart
// client: WebSocket transport (via package:web_socket_channel, which
// works across the Dart VM, Flutter, and web), the connect handshake,
// request-ID correlation, and event dispatch. It never depends on any
// particular protocol, so it is emitted verbatim rather than templated.
const runtimeBody = `import 'dart:async';
import 'dart:convert';

import 'package:web_socket_channel/web_socket_channel.dart';

/// Thrown for every RPC rejection and connect failure.
class LatchwireError extends Error {
  final String message;

  LatchwireError(this.message);

  @override
  String toString() => 'LatchwireError: $message';
}

/// Thrown when a value received from the server does not match a
/// generated enum's known wire values. See docs/design-notes.md
/// ("Dart unknown enum values").
class LatchwireDecodeException implements Exception {
  final String message;

  LatchwireDecodeException(this.message);

  @override
  String toString() => 'LatchwireDecodeException: $message';
}

class ClientOptions {
  final Uri url;

  const ClientOptions(this.url);
}

/// The live channel plus any frames that arrived after "connected" but
/// before the caller (a generated Connected*Client's constructor) could
/// install its own message handler - e.g. an event sent from OnConnect can
/// share the same underlying read as the handshake response. BaseConnection
/// replays these, in order, so no frame is ever silently dropped by the
/// handshake race.
class HandshakeResult {
  final WebSocketChannel channel;
  final StreamSubscription<dynamic> subscription;
  final List<String> bufferedMessages;

  HandshakeResult(this.channel, this.subscription, this.bufferedMessages);
}

/// Performs the WebSocket connect + Latchwire handshake, completing only
/// after the server's "connected" frame arrives (or failing with the
/// connection's connection_error). Used by every generated Client.connect().
///
/// package:web_socket_channel's channel.stream is single-subscription, so
/// only one Dart Stream.listen() call is ever legal on it - unlike a
/// browser WebSocket's onmessage, which can simply be reassigned. This
/// keeps the ONE subscription created here alive for the connection's
/// entire lifetime and hands it, not the raw stream, to HandshakeResult;
/// BaseConnection takes over by replacing this subscription's callbacks
/// (subscription.onData/onDone/onError) rather than calling listen() again.
Future<HandshakeResult> connectSocket(
  Uri url,
  String protocolName,
  String protocolVersion,
  Map<String, dynamic> payload,
) async {
  final channel = WebSocketChannel.connect(url);
  await channel.ready;

  final bufferedMessages = <String>[];
  final completer = Completer<HandshakeResult>();
  var settled = false;

  late StreamSubscription<dynamic> sub;
  sub = channel.stream.listen(
    (dynamic data) {
      if (settled) {
        bufferedMessages.add(data as String);
        return;
      }

      Map<String, dynamic> env;
      try {
        env = jsonDecode(data as String) as Map<String, dynamic>;
      } catch (_) {
        settled = true;
        channel.sink.close(1002, 'malformed handshake response');
        completer.completeError(
          LatchwireError('malformed response during connect'),
        );
        return;
      }

      if (env['type'] == 'connected') {
        settled = true;
        completer.complete(HandshakeResult(channel, sub, bufferedMessages));
      } else if (env['type'] == 'connection_error') {
        settled = true;
        channel.sink.close();
        completer.completeError(LatchwireError(
          env['error'] as String? ?? 'connection rejected',
        ));
      }
    },
    onError: (Object err, StackTrace st) {
      if (settled) return;
      settled = true;
      completer.completeError(LatchwireError(err.toString()));
    },
    onDone: () {
      if (settled) return;
      settled = true;
      completer.completeError(
        LatchwireError('connection closed before the handshake completed'),
      );
    },
  );

  channel.sink.add(jsonEncode({
    'type': 'connect',
    'protocol': protocolName,
    'version': protocolVersion,
    'payload': payload,
  }));

  return completer.future;
}

class _PendingRequest {
  final Completer<dynamic> completer;

  _PendingRequest(this.completer);
}

/// Base class for every generated "Connected*Client". Handles the
/// WebSocket message loop, request/response correlation by ID, and
/// dispatch of connection_error / close conditions. Generated subclasses
/// add typed method namespaces (backed by call()) and typed event streams
/// (backed by dispatchEvent()).
abstract class BaseConnection {
  final WebSocketChannel _channel;
  final StreamSubscription<dynamic> _subscription;
  int _nextId = 1;
  final Map<String, _PendingRequest> _pending = {};
  bool _closed = false;

  BaseConnection(HandshakeResult handshake)
      : _channel = handshake.channel,
        _subscription = handshake.subscription {
    // Take over the single subscription connectSocket already created
    // (see HandshakeResult's doc comment) by replacing its callbacks,
    // rather than calling channel.stream.listen() again - which would
    // throw "Stream has already been listened to".
    _subscription
      ..onData(_handleMessage)
      ..onDone(_handleClose)
      ..onError((Object _, StackTrace __) {});

    // See docs/design-notes.md ("TypeScript event callback scheduling"),
    // which applies identically here: replay is deferred with a real Timer
    // (Dart's macrotask/event-queue task), not scheduleMicrotask, so it
    // reliably runs after the caller's own code immediately following an
    // awaited client.connect(...) call - most importantly a synchronous
    // events.x.listen(...) call in the same microtask chain.
    if (handshake.bufferedMessages.isNotEmpty) {
      Timer(Duration.zero, () {
        for (final data in handshake.bufferedMessages) {
          _handleMessage(data);
        }
      });
    }
  }

  /// True once the connection has closed, for any reason.
  bool get closed => _closed;

  /// Closes the connection. Safe to call more than once.
  void close() {
    if (_closed) return;
    _channel.sink.close(1000);
    _handleClose();
  }

  /// @internal used by generated method namespaces.
  Future<TResp> call<TResp>(
    String method,
    Map<String, dynamic> payload,
    TResp Function(dynamic raw) decode,
  ) {
    if (_closed) {
      return Future<TResp>.error(LatchwireError('the connection is closed'));
    }
    final id = (_nextId++).toString();
    final completer = Completer<dynamic>();
    _pending[id] = _PendingRequest(completer);
    _channel.sink.add(jsonEncode({'type': 'request', 'id': id, 'method': method, 'payload': payload}));
    return completer.future.then((raw) => decode(raw));
  }

  /// Implemented by the generated subclass to route "event" frames to the
  /// right typed stream.
  void dispatchEvent(String? event, dynamic payload);

  void _handleMessage(dynamic data) {
    Map<String, dynamic> env;
    try {
      env = jsonDecode(data as String) as Map<String, dynamic>;
    } catch (_) {
      return;
    }

    switch (env['type']) {
      case 'response':
        final id = env['id'] as String?;
        final pending = id != null ? _pending.remove(id) : null;
        pending?.completer.complete(env['payload']);
        break;
      case 'error':
        final id = env['id'] as String?;
        final pending = id != null ? _pending.remove(id) : null;
        pending?.completer.completeError(LatchwireError(
          env['error'] as String? ?? 'internal error',
        ));
        break;
      case 'event':
        dispatchEvent(env['event'] as String?, env['payload']);
        break;
      case 'connection_error':
        _failAllPending(LatchwireError(
          env['error'] as String? ?? 'connection error',
        ));
        _handleClose();
        break;
      default:
        break;
    }
  }

  void _handleClose() {
    if (_closed) return;
    _closed = true;
    _failAllPending(LatchwireError('the connection is closed'));
  }

  void _failAllPending(LatchwireError err) {
    for (final pending in _pending.values) {
      if (!pending.completer.isCompleted) {
        pending.completer.completeError(err);
      }
    }
    _pending.clear();
  }
}
`
