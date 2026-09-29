use chat_app_client::{
    Event, HistoryRequest, JoinRoomRequest, LatchClient, ListRoomsRequest, SendMessageRequest,
};
use std::sync::mpsc::{self, Receiver};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

fn next_event(events: &Receiver<Event>) -> Event {
    events
        .recv_timeout(Duration::from_secs(5))
        .expect("timed out waiting for chat event")
}

#[test]
fn typed_chat_rpc_events_and_wire_error() {
    let Ok(url) = std::env::var("SERVER_URL") else {
        eprintln!("skipping live chat server test: SERVER_URL is not set");
        return;
    };
    if url.is_empty() {
        eprintln!("skipping live chat server test: SERVER_URL is empty");
        return;
    }

    // Isolate history and membership from other language tests sharing this server.
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock before Unix epoch")
        .as_nanos();
    let room = format!("rust-{}-{nonce}", std::process::id());
    let (bob_tx, bob_events) = mpsc::channel();
    let bob = LatchClient::connect(
        &url,
        move |event| {
            let _ = bob_tx.send(event);
        },
        |_| {},
    )
    .expect("connect Bob");
    let (alice_tx, alice_events) = mpsc::channel();
    let alice = LatchClient::connect(
        &url,
        move |event| {
            let _ = alice_tx.send(event);
        },
        |_| {},
    )
    .expect("connect Alice");

    let rooms = bob
        .chat_list_rooms(ListRoomsRequest {})
        .expect("list rooms");
    assert_eq!(rooms.rooms, ["general", "random"]);

    let invalid = bob
        .chat_join_room(JoinRoomRequest {
            room: String::new(),
            user_id: "bob".into(),
        })
        .expect_err("empty room must be rejected");
    assert_eq!(invalid.code, "invalid_request");
    assert_eq!(invalid.message, "room and userId are required");

    let joined = bob
        .chat_join_room(JoinRoomRequest {
            room: room.clone(),
            user_id: "bob".into(),
        })
        .expect("Bob joins");
    assert_eq!(joined.room, room);
    assert_eq!(joined.member_ids, ["bob"]);
    let presence = next_event(&bob_events);
    assert_eq!(presence.kind, "presence");
    assert!(presence.message.is_none());
    let presence = presence.presence.expect("Bob's presence payload");
    assert_eq!(
        (
            presence.room.as_str(),
            presence.user_id.as_str(),
            presence.online
        ),
        (room.as_str(), "bob", true)
    );

    let joined = alice
        .chat_join_room(JoinRoomRequest {
            room: room.clone(),
            user_id: "alice".into(),
        })
        .expect("Alice joins");
    assert_eq!(joined.room, room);
    assert_eq!(joined.member_ids.len(), 2);
    assert!(joined.member_ids.contains(&"bob".to_string()));
    assert!(joined.member_ids.contains(&"alice".to_string()));
    for events in [&bob_events, &alice_events] {
        let event = next_event(events);
        assert_eq!(event.kind, "presence");
        assert!(event.message.is_none());
        let presence = event.presence.expect("Alice's presence payload");
        assert_eq!(
            (
                presence.room.as_str(),
                presence.user_id.as_str(),
                presence.online
            ),
            (room.as_str(), "alice", true)
        );
    }

    let sent = alice
        .chat_send_message(SendMessageRequest {
            room: room.clone(),
            sender_id: "alice".into(),
            text: "Hi Bob, welcome!".into(),
        })
        .expect("send message")
        .message;
    assert!(sent.id > 0);
    assert_eq!(
        (
            sent.room.as_str(),
            sent.sender_id.as_str(),
            sent.text.as_str()
        ),
        (room.as_str(), "alice", "Hi Bob, welcome!")
    );
    assert!(sent.sent_at.0 > 0);
    for events in [&bob_events, &alice_events] {
        let event = next_event(events);
        assert_eq!(event.kind, "message");
        assert!(event.presence.is_none());
        assert_eq!(event.message.expect("message payload").message, sent);
    }
    let history = bob
        .chat_history(HistoryRequest { room })
        .expect("load history");
    assert_eq!(history.messages, [sent]);

    bob.close();
    alice.close();
}
