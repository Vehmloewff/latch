// Compile-time type-safety fixture (spec section 48). Dart has no
// "@ts-expect-error"-style pragma, so this file is never included in a
// normal `dart analyze` run of the package — it is intentionally invalid
// Dart, and tests/integration/dart_test.go runs `dart analyze` on this
// file alone, asserting that every line below produces the expected
// analyzer error. If a generated API ever stopped being genuinely typed
// (e.g. a field silently became `dynamic`), the corresponding assertion
// here would stop finding an error and the test would fail.
import 'package:basic_client/client.dart';

Future<void> shouldFailToAnalyze(LatchClient client) async {
  final conn = await client.connect();

  // Missing required argument.
  await conn.roomSubscribe(SubscribeRequest());

  // Wrong argument type: room must be String, not int.
  await conn.roomSubscribe(SubscribeRequest(room: 1));

  // SubscribeResponse has no "nonexistent" field.
  final result = await conn.roomSubscribe(SubscribeRequest(room: 'general'));
  print(result.nonexistent);

  // MessageReceived has no "nonexistent" field.
  conn.events.listen((event) {
    print(event.nonexistent);
  });

  // No such top-level method.
  await conn.doesNotExist();
}
