import 'dart:io';

import 'package:chat_app_client/client.dart';

final serverUrl =
    Uri.parse(Platform.environment['SERVER_URL'] ?? 'ws://127.0.0.1:8080/ws');

String describeEvent(Event event) {
  if (event.message != null) {
    final message = event.message!.message;
    return 'message from ${message.senderId}: ${message.text}';
  }
  if (event.presence != null) {
    final presence = event.presence!;
    return '${presence.userId} is online in #${presence.room}';
  }
  return event.kind;
}

// Story: Bob has opened a chat app, and Alice is already using the same room.
// Two connections model two browser tabs, phones, or users and let us watch
// the server broadcast events to the correct clients.
Future<void> main() async {
  // Bob has opened the chat app and connected to the server.
  final bob = await LatchClient(ClientOptions(serverUrl)).connect();
  print('Bob connected');

  // Alice opens her own independent connection.
  final alice = await LatchClient(ClientOptions(serverUrl)).connect();
  print('Alice connected');

  // A real chat UI would update its timeline and member list from this stream.
  final bobEvents = bob.events.listen((event) {
    print('Bob received event: ${describeEvent(event)}');
  });
  final aliceEvents = alice.events.listen((event) {
    print('Alice received event: ${describeEvent(event)}');
  });

  try {
    // Bob asks which rooms are available before rendering the room picker.
    final rooms = await bob.chatListRooms(ListRoomsRequest());
    print('Bob sees rooms: ${rooms.rooms}');

    // Bob joins #general, subscribing this connection to that room's events.
    final bobJoin =
        await bob.chatJoinRoom(JoinRoomRequest(room: 'general', userId: 'bob'));
    print('Bob joined: ${bobJoin.memberIds}');

    // Alice joins the same room from her separate connection.
    final aliceJoin = await alice
        .chatJoinRoom(JoinRoomRequest(room: 'general', userId: 'alice'));
    print('Alice joined: ${aliceJoin.memberIds}');

    // Alice sends a message. The response goes to Alice, and the server emits
    // a message event to both Alice and Bob for their chat timelines.
    final sent = await alice.chatSendMessage(SendMessageRequest(
      room: 'general',
      senderId: 'alice',
      text: 'Hi Bob, welcome!',
    ));
    print('Alice sent: ${sent.message.text}');

    // Bob can restore the conversation from history after reconnecting or
    // after mounting a chat screen that missed earlier events.
    final history = await bob.chatHistory(HistoryRequest(room: 'general'));
    print('Bob loads history: ${history.messages.length} message(s)');
  } finally {
    await bobEvents.cancel();
    await aliceEvents.cancel();
    bob.close();
    alice.close();
    print('Both chat connections closed');
  }
}
