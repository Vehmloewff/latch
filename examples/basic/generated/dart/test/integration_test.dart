// Hand-written (not generated) integration test for the generated Dart
// client. The server's WebSocket URL comes from the LATCHWIRE_WS_URL
// environment variable so this can run against either the standalone
// example server or an ephemeral httptest server spun up by
// tests/integration/dart_test.go.
import 'dart:io';

import 'package:basic_client/basic_client.dart';
import 'package:test/test.dart';

void main() {
  final url = Uri.parse(Platform.environment['LATCHWIRE_WS_URL'] ?? 'ws://127.0.0.1:8080/ws');

  test('generated Dart client integration', () async {
    final client = BasicClient(ClientOptions(url));
    final conn = await client.connect(ConnectParams(token: 'secret'));

    final welcome = await conn.events.messageReceived.first;
    expect(welcome.room, 'lobby');
    expect(welcome.text, 'welcome');

    final presence = await conn.events.presenceChanged.first;
    expect(presence.userId, 'self');
    expect(presence.online, isTrue);

    final result = await conn.room.subscribe(SubscribeRequest(room: 'general'));
    expect(result.ok, isTrue);

    final listed = await conn.room.list(ListRoomsRequest());
    expect(listed.rooms, hasLength(3));

    // Nested types + optional/nullable fields.
    final profile = await conn.profile.get(ProfileGetRequest(userId: 'alice'));
    expect(profile.profile.name, 'User alice');
    expect(profile.profile.address.city, 'Springfield');
    expect(profile.profile.nickname, isNotNull);
    expect(profile.profile.address.zip, isNotNull);

    final noZipProfile = await conn.profile.get(ProfileGetRequest(userId: 'no-zip'));
    expect(noZipProfile.profile.address.zip, isNull);

    // Application error.
    await expectLater(
      conn.profile.get(ProfileGetRequest(userId: 'missing')),
      throwsA(isA<LatchwireError>().having((e) => e.code, 'code', 'not_found')),
    );

    // Concurrent calls: fire many requests at once and verify every
    // response matches its own request despite handlers executing
    // concurrently.
    final ids = List.generate(10, (i) => 'user-$i');
    final profiles = await Future.wait(ids.map((id) => conn.profile.get(ProfileGetRequest(userId: id))));
    for (var i = 0; i < ids.length; i++) {
      expect(profiles[i].profile.name, 'User user-$i');
    }

    final badClient = BasicClient(ClientOptions(url));
    await expectLater(
      badClient.connect(ConnectParams(token: '')),
      throwsA(isA<LatchwireError>().having((e) => e.code, 'code', 'invalid_connect_payload')),
    );

    conn.close();
  }, timeout: const Timeout(Duration(seconds: 10)));
}
