package dart

import _ "embed"

// binaryRuntimeSource is the tested standalone codec embedded into every
// generated client. Generated clients therefore have no dependency on
// codegen/dart/binary_runtime.
//
//go:embed binary_runtime/lib/latch_binary_runtime.dart
var binaryRuntimeSource string

var runtimeBody = `import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:web_socket_channel/web_socket_channel.dart';

class LatchError extends Error {
  final String code;
  final String message;

  LatchError(this.code, this.message);

  @override
  String toString() => 'LatchError($code): $message';
}

class LatchDecodeException implements Exception {
  final String message;

  LatchDecodeException(this.message);

  @override
  String toString() => 'LatchDecodeException: $message';
}

class ClientOptions {
  final Uri url;

  const ClientOptions(this.url);
}

class HandshakeResult {
  final WebSocketChannel channel;
  final StreamSubscription<dynamic> subscription;
  final List<Uint8List> bufferedMessages;

  HandshakeResult(this.channel, this.subscription, this.bufferedMessages);
}

Uint8List _messageBytes(dynamic data) {
  if (data is Uint8List) return Uint8List.fromList(data);
  if (data is List<int>) return Uint8List.fromList(data);
  throw BinaryMalformedError('WebSocket message is not binary');
}

Future<HandshakeResult> connectSocket(Uri url, String version) async {
  final query = Map<String, String>.from(url.queryParameters);
  query['version'] = version;
  final channel = WebSocketChannel.connect(url.replace(queryParameters: query));
  await channel.ready;

  final bufferedMessages = <Uint8List>[];
  final opened = Completer<HandshakeResult>();
  var watchingFirstFrame = true;
  late StreamSubscription<dynamic> sub;

  void rejectOpen(Object error, StackTrace stack) {
    if (opened.isCompleted) return;
    try {
      channel.sink.close(1000);
    } catch (_) {}
    opened.completeError(error, stack);
  }

  sub = channel.stream.listen((dynamic data) {
    late final Uint8List bytes;
    try {
      bytes = _messageBytes(data);
      if (watchingFirstFrame) {
        final envelope = BinaryEnvelope.decode(bytes);
        watchingFirstFrame = false;
        if (envelope.type == FrameCode.connectionError) {
          rejectOpen(
            LatchError(
              'connect_rejected',
              envelope.error.isEmpty ? 'connection rejected' : envelope.error,
            ),
            StackTrace.current,
          );
          return;
        }
      }
    } catch (error, stack) {
      if (watchingFirstFrame) {
        rejectOpen(
          LatchError('connect_rejected', 'malformed connection frame'),
          stack,
        );
        return;
      }
      rethrow;
    }
    bufferedMessages.add(bytes);
    if (!opened.isCompleted) {
      opened.complete(HandshakeResult(channel, sub, bufferedMessages));
    }
  }, onError: (Object error, StackTrace stack) {
    if (watchingFirstFrame) {
      rejectOpen(
        LatchError('connect_rejected', 'connection failed'),
        stack,
      );
    }
  });

  Timer(Duration.zero, () {
    if (!opened.isCompleted) {
      watchingFirstFrame = false;
      opened.complete(HandshakeResult(channel, sub, bufferedMessages));
    }
  });
  return opened.future;
}

class _PendingRequest {
  final Completer<dynamic> completer;

  _PendingRequest(this.completer);
}

abstract class BaseConnection {
  final WebSocketChannel _channel;
  final StreamSubscription<dynamic> _subscription;
  int _nextId = 1;
  final Map<String, _PendingRequest> _pending = {};
  bool _closed = false;

  BaseConnection(HandshakeResult handshake)
      : _channel = handshake.channel,
        _subscription = handshake.subscription {
    _subscription
      ..onData(_handleMessage)
      ..onDone(_handleClose)
      ..onError((Object _, StackTrace __) {});

    if (handshake.bufferedMessages.isNotEmpty) {
      Timer(Duration.zero, () {
        for (final data in handshake.bufferedMessages) {
          _handleMessage(data);
        }
      });
    }
  }

  bool get closed => _closed;

  void close() {
    if (_closed) return;
    _channel.sink.close(1000);
    _handleClose();
  }

  Future<TResp> call<TResp>(
    String method,
    Object? payload,
    TResp Function(Object? raw) decode,
  ) {
    if (_closed) {
      return Future<TResp>.error(
        LatchError('connection_closed', 'the connection is closed'),
      );
    }
    final id = (_nextId++).toString();
    final completer = Completer<dynamic>();
    _pending[id] = _PendingRequest(completer);
    try {
      final request = BinaryEnvelope(
        type: FrameCode.request,
        id: id,
        method: method,
        payload: BinaryCodec.encode(payload),
      );
      _channel.sink.add(request.encode());
    } catch (error, stack) {
      _pending.remove(id);
      completer.completeError(error, stack);
    }
    return completer.future.then((raw) => decode(raw));
  }

  void dispatchEvent(Object? payload);

  void _handleMessage(dynamic data) {
    late final BinaryEnvelope env;
    try {
      env = BinaryEnvelope.decode(_messageBytes(data));
    } catch (_) {
      _failAllPending(
        LatchError('protocol_violation', 'malformed binary frame'),
      );
      try {
        _channel.sink.close(1002);
      } catch (_) {}
      _handleClose();
      return;
    }

    switch (env.type) {
      case FrameCode.response:
        final pending = _pending.remove(env.id);
        if (pending != null) {
          try {
            pending.completer.complete(
              env.payload.isEmpty ? null : BinaryCodec.decode(env.payload),
            );
          } catch (error, stack) {
            pending.completer.completeError(error, stack);
          }
        }
        break;
      case FrameCode.error:
        final pending = _pending.remove(env.id);
        pending?.completer.completeError(
          LatchError(
            env.errorCode.isEmpty ? 'internal_error' : env.errorCode,
            env.error.isEmpty ? 'internal error' : env.error,
          ),
        );
        break;
      case FrameCode.event:
        try {
          dispatchEvent(BinaryCodec.decode(env.payload));
        } catch (_) {}
        break;
      case FrameCode.connectionError:
        _failAllPending(LatchError(
          env.errorCode.isEmpty ? 'internal_error' : env.errorCode,
          env.error.isEmpty ? 'connection error' : env.error,
        ));
        try {
          _channel.sink.close(1000);
        } catch (_) {}
        _handleClose();
        break;
      default:
        break;
    }
  }

  void _handleClose() {
    if (_closed) return;
    _closed = true;
    _failAllPending(LatchError('connection_closed', 'the connection is closed'));
  }

  void _failAllPending(LatchError err) {
    for (final pending in _pending.values) {
      if (!pending.completer.isCompleted) pending.completer.completeError(err);
    }
    _pending.clear();
  }
}
` + "\n" + stripImports(binaryRuntimeSource)
