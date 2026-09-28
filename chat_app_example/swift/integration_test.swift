import Foundation

@main
struct SwiftClientIntegrationTest {
    static func require(_ condition: @autoclosure () -> Bool, _ message: String) {
        guard condition() else { fatalError("Swift client integration failed: \(message)") }
    }

    static func nextEvent(_ events: AsyncStream<Event>, matching kind: String) async throws -> Event {
        for await event in events {
            if event.kind == kind { return event }
        }
        throw LatchError(code: "event_stream_closed", message: "event stream closed before \(kind)")
    }

    static func testBinaryCodec() throws {
        let instant = Date(timeIntervalSince1970: 1_700_000_000.125)
        let values: [LatchValue] = [
            .null,
            .bool(false), .bool(true),
            .int(Int64.min), .int(-1), .int(0), .int(Int64.max),
            .uint(0), .uint(UInt64.max),
            .float32(-12.5), .float64(0.125),
            .string("hello 🌍"),
            .time(instant),
            .list([.string("x"), .int(9), .null]),
            .map(["alpha": .bool(true), "beta": .float64(3.5)]),
            .structure([1: .string("nested"), 4: .list([.uint(7)])]),
        ]
        for value in values {
            let encoded = try LatchBinary.encodeValue(value)
            let decoded = try LatchBinary.decodeValue(encoded)
            require(decoded == value, "binary value round trip for \(value)")
        }

        let malformedValues = [
            Data(),
            Data([0xff]),
            Data([7, 2, 0x61]),
            Data([7, 0x80, 0]),
            Data([7, 1, 0xff]),
            Data([10, 0x81, 0x80, 0x80, 0x08]),
            Data([0, 0]),
        ]
        for malformed in malformedValues {
            do {
                _ = try LatchBinary.decodeValue(malformed)
                fatalError("expected malformed binary value rejection")
            } catch is LatchError {
                continue
            }
        }
    }

    static func main() async throws {
        try testBinaryCodec()
        guard let rawURL = ProcessInfo.processInfo.environment["SERVER_URL"],
              let url = URL(string: rawURL) else {
            fatalError("SERVER_URL must contain the example WebSocket URL")
        }

        let client = LatchClient(url: url)
        let connection = try await client.connect()
        defer { Task { await connection.close() } }

        let eventStream = connection.events
        let presence = Task { try await nextEvent(eventStream, matching: "presence") }
        let joined = try await connection.chatJoinRoom(
            JoinRoomRequest(room: "general", userId: "swift-user")
        )
        require(joined.room == "general", "join response room")
        require(joined.memberIds.contains("swift-user"), "join response member list")
        let presenceEvent = try await presence.value
        require(presenceEvent.presence?.userId == "swift-user", "presence event payload")

        let messageEvent = Task { try await nextEvent(eventStream, matching: "message") }
        let sent = try await connection.chatSendMessage(
            SendMessageRequest(room: "general", senderId: "swift-user", text: "swift says hello")
        )
        require(sent.message.text == "swift says hello", "send response text")
        require(sent.message.id > 0, "send response integer field")
        require(abs(sent.message.sentAt.timeIntervalSinceNow) < 30, "send response timestamp")

        let event = try await messageEvent.value
        require(event.message?.message.text == "swift says hello", "message event payload")

        let history = try await connection.chatHistory(HistoryRequest(room: "general"))
        require(history.messages.contains(where: { $0.text == "swift says hello" }), "history list response")
        let rooms = try await connection.chatListRooms(ListRoomsRequest())
        require(rooms.rooms == ["general", "random"], "list response")

        do {
            _ = try await connection.chatSendMessage(
                SendMessageRequest(room: "", senderId: "swift-user", text: "")
            )
            fatalError("expected server RPC error")
        } catch let error as LatchError {
            require(error.code == "invalid_request", "RPC error code")
            require(error.message == "room, senderId, and text are required", "RPC error message")
        }

        await connection.close()
        do {
            _ = try await connection.chatListRooms(ListRoomsRequest())
            fatalError("RPC after close should fail")
        } catch let error as LatchError {
            require(error.code == "connection_closed", "closed connection error")
        }
        print("Swift client integration passed")
    }
}
